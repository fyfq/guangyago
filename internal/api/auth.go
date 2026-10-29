package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/auth"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/store"
)

// loginGuard 按维度（IP / 用户名）统计最近失败次数，连续失败后短暂锁定。
// 状态持久化到 SQLite，服务重启不会重置；进程内再做一层短 TTL 缓存以省去热路径查询。
type loginGuard struct {
	mu    sync.Mutex
	cache map[string]*loginAttempt
	ttl   time.Duration
}

type loginAttempt struct {
	fails int
	until time.Time
}

const (
	loginMaxFails = 5
	loginLockFor  = 5 * time.Minute
	loginCacheTTL = 30 * time.Second
)

func newLoginGuard() *loginGuard {
	return &loginGuard{cache: map[string]*loginAttempt{}, ttl: loginCacheTTL}
}

// keyIP / keyUser 是两个维度的记录主键；用户名统一小写以合并大小写变体。
func keyIP(ip string) string     { return "ip:" + ip }
func keyUser(name string) string { return "u:" + strings.ToLower(strings.TrimSpace(name)) }

// load 从进程缓存读取，缺失时懒查 DB；查询失败视作未锁定，避免因数据库故障阻断登录。
func (g *loginGuard) load(store *store.Store, key string, now time.Time) *loginAttempt {
	g.mu.Lock()
	if a, ok := g.cache[key]; ok && now.Sub(aCacheTime(a)) < g.ttl {
		g.mu.Unlock()
		return a
	}
	g.mu.Unlock()

	att, err := store.LoginAttemptGet(key)
	a := &loginAttempt{fails: att.Fails, until: att.Until}
	if err != nil {
		slog.Warn("读取登录尝试记录失败", "key", key, "err", err)
	}
	g.mu.Lock()
	g.cache[key] = a
	g.mu.Unlock()
	return a
}

// loginAttempt 不携带时间戳字段，这里用 until 与 fails 推断新鲜度：
// 简化处理，缓存命中即直接返回；进程重启或调用 fail/reset 会主动刷新。
func aCacheTime(a *loginAttempt) time.Time {
	if a == nil {
		return time.Time{}
	}
	return a.until
}

// observe 判断给定 key 是否处于锁定状态；同时把状态同步进缓存。
func (g *loginGuard) observe(st *store.Store, key string, now time.Time) bool {
	if key == "" {
		return false
	}
	a := g.load(st, key, now)
	return now.Before(a.until)
}

// recordFail 累加一次失败；达到阈值时进入锁定。
func (g *loginGuard) recordFail(st *store.Store, key string, now time.Time) {
	if key == "" {
		return
	}
	if err := st.LoginAttemptFail(key, now, loginMaxFails, loginLockFor); err != nil {
		slog.Error("记录登录失败次数出错", "key", key, "err", err)
		return
	}
	g.mu.Lock()
	// 让下一次 load 立刻刷新
	delete(g.cache, key)
	g.mu.Unlock()
}

// reset 在登录成功后清除指定 key 的锁定与计数。
func (g *loginGuard) reset(st *store.Store, key string) {
	if key == "" {
		return
	}
	if err := st.LoginAttemptReset(key); err != nil {
		slog.Warn("清除登录尝试记录失败", "key", key, "err", err)
	}
	g.mu.Lock()
	delete(g.cache, key)
	g.mu.Unlock()
}

// pruneCache 定期回收过期的进程内缓存，避免因用户暴增导致 map 无界增长。
func (g *loginGuard) pruneCache(now time.Time) {
	g.mu.Lock()
	for k, a := range g.cache {
		if a == nil || (!a.until.IsZero() && now.After(a.until.Add(g.ttl))) {
			delete(g.cache, k)
		}
	}
	g.mu.Unlock()
}

const ctxUserKey = "auth.user"

// requireAuth 校验会话 Cookie，把当前账号放进上下文。
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(auth.CookieName)
		if err != nil || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
			return
		}
		u, err := s.store.SessionUser(auth.HashToken(token), time.Now())
		if err != nil {
			slog.Error("读取会话失败", "err", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}
		if u == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "登录已失效"})
			return
		}
		c.Set(ctxUserKey, u)
		c.Next()
	}
}

// requireAdmin 只允许管理员访问用户管理等敏感接口。
func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if u := currentUser(c); u == nil || !u.IsAdmin() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			return
		}
		c.Next()
	}
}

func currentUser(c *gin.Context) *model.User {
	if v, ok := c.Get(ctxUserKey); ok {
		if u, ok := v.(*model.User); ok {
			return u
		}
	}
	return nil
}

// secureCookie 在 HTTPS（或反代声明 HTTPS）下要求 Cookie 只走加密连接。
// 见 (*Server).secureCookie，优先读取 server.secure_cookie 配置。

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入用户名和密码"})
		return
	}

	now := time.Now()
	ipKey := keyIP(c.ClientIP())
	userKey := keyUser(username)
	if s.loginGuard.observe(s.store, ipKey, now) || s.loginGuard.observe(s.store, userKey, now) {
		slog.Warn("登录限流触发", "ip", c.ClientIP(), "username", username)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试次数过多，请 5 分钟后再试"})
		return
	}

	u, hash, err := s.store.UserByUsername(username)
	if err != nil {
		serverError(c, err)
		return
	}
	// 账号不存在、被停用、密码错误统一返回同一个提示，不泄露账号状态
	ok := auth.VerifyPasswordOrDummy(hash, req.Password)
	if u == nil || !u.Enabled || !ok {
		slog.Warn("登录失败", "username", username, "ip", c.ClientIP())
		s.loginGuard.recordFail(s.store, ipKey, now)
		s.loginGuard.recordFail(s.store, userKey, now)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成登录凭证失败"})
		return
	}
	expires := now.Add(auth.SessionTTL)
	if err := s.store.CreateSession(u.ID, tokenHash, expires); err != nil {
		serverError(c, err)
		return
	}
	_ = s.store.TouchLastLogin(u.ID)
	_ = s.store.DeleteExpiredSessions(now)
	_ = s.store.LoginAttemptCleanup(now)
	s.loginGuard.reset(s.store, ipKey)
	s.loginGuard.reset(s.store, userKey)
	s.loginGuard.pruneCache(now)

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, token, int(auth.SessionTTL.Seconds()), "/", "", s.secureCookie(c), true)
	u.LastLoginAt = &now
	slog.Info("登录成功", "username", u.Username, "ip", c.ClientIP())
	c.JSON(http.StatusOK, u)
}

func (s *Server) logout(c *gin.Context) {
	if token, err := c.Cookie(auth.CookieName); err == nil && token != "" {
		_ = s.store.DeleteSession(auth.HashToken(token))
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, "", -1, "/", "", s.secureCookie(c), true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) me(c *gin.Context) {
	c.JSON(http.StatusOK, currentUser(c))
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (s *Server) changePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	if !validPassword(req.NewPassword) {
		c.JSON(http.StatusBadRequest, gin.H{"error": passwordHintFor(req.NewPassword)})
		return
	}
	u := currentUser(c)
	hash, err := s.store.UserPasswordHash(u.ID)
	if err != nil {
		serverError(c, err)
		return
	}
	if !auth.VerifyPassword(hash, req.OldPassword) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前密码不正确"})
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}
	if err := s.store.UpdateUserPassword(u.ID, newHash); err != nil {
		serverError(c, err)
		return
	}
	// 改密后所有会话作废，前端会跳回登录页重新登录
	_ = s.store.DeleteUserSessions(u.ID)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, "", -1, "/", "", s.secureCookie(c), true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// secureCookie 优先按配置决定是否强制 Secure；未显式配置时回退到 TLS / X-Forwarded-Proto 判定。
func (s *Server) secureCookie(c *gin.Context) bool {
	s.configMu.RLock()
	var override *bool
	if s.cfg != nil {
		override = s.cfg.Server.SecureCookie
	}
	s.configMu.RUnlock()
	if override != nil {
		return *override
	}
	return c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

// validOrigin 用 Origin / Referer 兜底校验，防止 SameSite=Lax 之外场景下的跨站 POST。
// 请求不带 Origin 且没有 Referer 时（同源 curl / 内部工具）视为通过，避免误伤。
func (s *Server) validOrigin(c *gin.Context) bool {
	host := c.Request.Host
	if origin := strings.TrimSpace(c.GetHeader("Origin")); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, host) {
			return false
		}
		return true
	}
	if ref := strings.TrimSpace(c.GetHeader("Referer")); ref != "" {
		u, err := url.Parse(ref)
		if err != nil || !strings.EqualFold(u.Host, host) {
			return false
		}
		return true
	}
	return true
}

// requireSameOrigin 拦截所有状态变更接口（POST / PUT / DELETE / PATCH）的跨站请求。
// 浏览器发跨源 XHR 必带 Origin，同源自 API 天然通过。
func (s *Server) requireSameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if !s.validOrigin(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "跨站请求已被拒绝"})
			return
		}
		c.Next()
	}
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

// 常见弱密码黑名单（小写比较），只拦最典型的模式，避免过度打扰运营。
var weakPasswords = map[string]struct{}{
	"password": {}, "password1": {}, "password12": {}, "password123": {}, "password1234": {},
	"12345678": {}, "123456789": {}, "1234567890": {}, "qwertyui": {}, "qwertyuiop": {},
	"abc12345": {}, "iloveyou": {}, "admin123": {}, "administrator": {}, "guangya123": {},
}

// validPassword 至少 8 字节、最多 128 字节，排除常见弱口令，
// 并要求字符类别（小写 / 大写 / 数字 / 符号）里至少覆盖两类。
func validPassword(p string) bool {
	if len(p) < 8 || len(p) > 128 {
		return false
	}
	if _, bad := weakPasswords[strings.ToLower(p)]; bad {
		return false
	}
	var lower, upper, digit, symbol bool
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			symbol = true
		}
	}
	classes := 0
	for _, on := range []bool{lower, upper, digit, symbol} {
		if on {
			classes++
		}
	}
	return classes >= 2
}

// passwordHintFor 汇总给用户的具体原因。
func passwordHintFor(p string) string {
	if len(p) < 8 {
		return "密码至少 8 位"
	}
	if len(p) > 128 {
		return "密码过长"
	}
	if _, bad := weakPasswords[strings.ToLower(p)]; bad {
		return "密码过于常见，请换一个"
	}
	return "密码需要包含大小写字母、数字、符号中的至少两类"
}

func validRole(role string) bool {
	return role == model.RoleAdmin || role == model.RoleOperator
}

func (s *Server) listUsers(c *gin.Context) {
	users, err := s.store.ListUsers()
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": len(users), "items": users})
}

type createUserRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

func (s *Server) createUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if !usernameRe.MatchString(req.Username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名 3-32 位，仅限字母、数字、下划线、点和短横线"})
		return
	}
	if !validPassword(req.Password) {
		c.JSON(http.StatusBadRequest, gin.H{"error": passwordHintFor(req.Password)})
		return
	}
	if req.Role == "" {
		req.Role = model.RoleOperator
	}
	if !validRole(req.Role) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "角色不正确"})
		return
	}
	if len(req.DisplayName) > 40 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "显示名过长"})
		return
	}

	existing, _, err := s.store.UserByUsername(req.Username)
	if err != nil {
		serverError(c, err)
		return
	}
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "用户名已存在"})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}
	u := &model.User{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Role:        req.Role,
		Enabled:     true,
	}
	if err := s.store.CreateUser(u, hash); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

type updateUserRequest struct {
	DisplayName *string `json:"display_name"`
	Role        *string `json:"role"`
	Enabled     *bool   `json:"enabled"`
}

func (s *Server) updateUser(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	target, err := s.store.UserByID(id)
	if err != nil {
		serverError(c, err)
		return
	}
	if target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}

	me := currentUser(c)
	displayName := target.DisplayName
	role := target.Role
	enabled := target.Enabled
	if req.DisplayName != nil {
		displayName = strings.TrimSpace(*req.DisplayName)
		if len(displayName) > 40 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "显示名过长"})
			return
		}
	}
	if req.Role != nil {
		if !validRole(*req.Role) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "角色不正确"})
			return
		}
		role = *req.Role
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if id == me.ID && (!enabled || role != target.Role) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能停用自己或修改自己的角色"})
		return
	}

	if err := s.store.UpdateUserGuarded(id, displayName, role, enabled, true); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		serverError(c, err)
		return
	}
	target.DisplayName, target.Role, target.Enabled = displayName, role, enabled
	c.JSON(http.StatusOK, target)
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

func (s *Server) resetUserPassword(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	if !validPassword(req.Password) {
		c.JSON(http.StatusBadRequest, gin.H{"error": passwordHintFor(req.Password)})
		return
	}
	target, err := s.store.UserByID(id)
	if err != nil {
		serverError(c, err)
		return
	}
	if target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}
	if err := s.store.UpdateUserPassword(id, hash); err != nil {
		serverError(c, err)
		return
	}
	_ = s.store.DeleteUserSessions(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) deleteUser(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	me := currentUser(c)
	if id == me.ID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能删除当前登录的账号"})
		return
	}
	target, err := s.store.UserByID(id)
	if err != nil {
		serverError(c, err)
		return
	}
	if target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}
	if target.Role == model.RoleAdmin && target.Enabled {
		if err := s.store.DeleteUserGuarded(id, true); err != nil {
			if errors.Is(err, store.ErrLastAdmin) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": id})
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}
