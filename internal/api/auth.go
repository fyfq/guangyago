package api

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/auth"
	"github.com/odin/guangya-go/internal/model"
)

// loginGuard 按客户端 IP 统计最近失败次数，连续失败后短暂锁定。
type loginGuard struct {
	mu      sync.Mutex
	entries map[string]*loginAttempt
}

type loginAttempt struct {
	fails int
	until time.Time
}

const (
	loginMaxFails = 5
	loginLockFor  = 5 * time.Minute
)

func newLoginGuard() *loginGuard {
	return &loginGuard{entries: map[string]*loginAttempt{}}
}

func (g *loginGuard) blocked(key string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.entries[key]
	return ok && now.Before(a.until)
}

func (g *loginGuard) fail(key string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// 临时条目过多时直接清空，这个限流只为挡住暴力尝试，不需要精确统计
	if len(g.entries) > 1024 {
		g.entries = map[string]*loginAttempt{}
	}
	a, ok := g.entries[key]
	if !ok {
		a = &loginAttempt{}
		g.entries[key] = a
	}
	if !a.until.IsZero() && now.After(a.until) {
		a.fails = 0
		a.until = time.Time{}
	}
	a.fails++
	if a.fails >= loginMaxFails {
		a.fails = 0
		a.until = now.Add(loginLockFor)
	}
}

func (g *loginGuard) reset(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, key)
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
func secureCookie(c *gin.Context) bool {
	return c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

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
	ip := c.ClientIP()
	if s.loginGuard.blocked(ip, now) {
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
		s.loginGuard.fail(ip, now)
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
	s.loginGuard.reset(ip)

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, token, int(auth.SessionTTL.Seconds()), "/", "", secureCookie(c), true)
	u.LastLoginAt = &now
	c.JSON(http.StatusOK, u)
}

func (s *Server) logout(c *gin.Context) {
	if token, err := c.Cookie(auth.CookieName); err == nil && token != "" {
		_ = s.store.DeleteSession(auth.HashToken(token))
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CookieName, "", -1, "/", "", secureCookie(c), true)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码至少 8 位"})
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
	c.SetCookie(auth.CookieName, "", -1, "/", "", secureCookie(c), true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

func validPassword(p string) bool { return len(p) >= 8 && len(p) <= 128 }

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
		c.JSON(http.StatusBadRequest, gin.H{"error": "密码至少 8 位"})
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
	if target.Role == model.RoleAdmin && target.Enabled && (!enabled || role != model.RoleAdmin) {
		others, err := s.store.EnabledAdminCount(id)
		if err != nil {
			serverError(c, err)
			return
		}
		if others == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少要保留一个启用的管理员"})
			return
		}
	}

	if err := s.store.UpdateUser(id, displayName, role, enabled); err != nil {
		serverError(c, err)
		return
	}
	// 停用后立即踢掉该账号的所有会话
	if !enabled {
		_ = s.store.DeleteUserSessions(id)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "密码至少 8 位"})
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
		others, err := s.store.EnabledAdminCount(id)
		if err != nil {
			serverError(c, err)
			return
		}
		if others == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少要保留一个启用的管理员"})
			return
		}
	}
	if err := s.store.DeleteUser(id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}
