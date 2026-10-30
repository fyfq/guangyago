package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/auth"
	"github.com/odin/guangya-go/internal/model"
)

func TestSetupFlow(t *testing.T) {
	e := newAPIEnvWithAdmin(t, false)

	rec := e.callAnon(http.MethodGet, "/api/setup/status", nil)
	requireStatus(t, rec, http.StatusOK)
	var status struct {
		Installed bool `json:"installed"`
	}
	decodeInto(t, rec, &status)
	if status.Installed {
		t.Fatal("空库不应标记为已安装")
	}

	cases := []struct {
		name string
		body any
	}{
		{"非法 JSON", "{"},
		{"用户名过短", map[string]any{"username": "ab", "password": "Abcdef12!"}},
		{"弱密码", map[string]any{"username": "root", "password": "short"}},
		{"显示名过长", map[string]any{"username": "root", "password": "Abcdef12!", "display_name": strings.Repeat("长", 41)}},
		{"对外地址缺协议", map[string]any{"username": "root", "password": "Abcdef12!", "public_url": "gy.example.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			requireStatus(t, e.callAnon(http.MethodPost, "/api/setup", c.body), http.StatusBadRequest)
		})
	}

	rec = e.callAnon(http.MethodPost, "/api/setup", map[string]any{
		"username":     "root",
		"password":     "Abcdef12!",
		"public_url":   "https://gy.example.com/",
		"tmdb_api_key": "tmdb-key",
	})
	requireStatus(t, rec, http.StatusOK)
	var created struct {
		OK     bool  `json:"ok"`
		UserID int64 `json:"user_id"`
	}
	decodeInto(t, rec, &created)
	if !created.OK || created.UserID == 0 {
		t.Fatalf("安装结果异常: %+v", created)
	}

	users, err := e.st.ListUsers()
	if err != nil {
		t.Fatalf("读取账号失败: %v", err)
	}
	if len(users) != 1 || users[0].Role != model.RoleAdmin {
		t.Fatalf("应创建一个管理员账号，实际 %+v", users)
	}
	if e.cfg.TMDB.APIKey != "tmdb-key" {
		t.Errorf("TMDB Key 未写入配置: %q", e.cfg.TMDB.APIKey)
	}
	if e.cfg.Server.PublicURL != "https://gy.example.com" {
		t.Errorf("对外访问地址未写入配置或未去掉结尾斜杠: %q", e.cfg.Server.PublicURL)
	}
	if e.reloads != 1 {
		t.Errorf("保存密钥后应触发一次组件重建，实际 %d", e.reloads)
	}
	if _, err := os.Stat(e.cfgPath); err != nil {
		t.Errorf("配置文件未落盘: %v", err)
	}

	rec = e.callAnon(http.MethodGet, "/api/setup/status", nil)
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &status)
	if !status.Installed {
		t.Error("创建管理员后应标记为已安装")
	}

	rec = e.callAnon(http.MethodPost, "/api/setup", map[string]any{
		"username": "root2",
		"password": "Abcdef12!",
	})
	requireStatus(t, rec, http.StatusForbidden)
}

func TestLoginFlow(t *testing.T) {
	e := newAPIEnv(t)

	requireStatus(t, e.callAnon(http.MethodGet, "/api/auth/me", nil), http.StatusUnauthorized)
	requireStatus(t, e.callAnon(http.MethodPost, "/api/auth/login", "{bad"), http.StatusBadRequest)
	requireStatus(t, e.callAnon(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin"}), http.StatusBadRequest)

	rec := e.callAnon(http.MethodPost, "/api/auth/login", map[string]string{
		"username": "admin", "password": "WrongPass1!",
	})
	requireStatus(t, rec, http.StatusUnauthorized)
	if !strings.Contains(rec.Body.String(), "用户名或密码错误") {
		t.Errorf("错误提示不应泄露账号状态: %s", rec.Body.String())
	}

	rec = e.callAnon(http.MethodPost, "/api/auth/login", map[string]string{
		"username": "nobody", "password": "Abcdef12!",
	})
	requireStatus(t, rec, http.StatusUnauthorized)

	rec = e.callAnon(http.MethodPost, "/api/auth/login", map[string]string{
		"username": "admin", "password": "Abcdef12!",
	})
	requireStatus(t, rec, http.StatusOK)

	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			session = c
		}
	}
	if session == nil || session.Value == "" {
		t.Fatal("登录成功未下发会话 Cookie")
	}
	if !session.HttpOnly {
		t.Error("会话 Cookie 必须是 HttpOnly")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("会话 Cookie 应为 SameSite=Lax，实际 %v", session.SameSite)
	}
	if session.Secure {
		t.Error("secure_cookie=false 时不应带 Secure")
	}

	rec = e.callAs(http.MethodGet, "/api/auth/me", nil, session.Value)
	requireStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"username":"admin"`) {
		t.Errorf("me 应返回当前账号: %s", rec.Body.String())
	}

	// 过期会话视同未登录
	expired, expiredHash, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("生成令牌失败: %v", err)
	}
	if err := e.st.CreateSession(e.admin.ID, expiredHash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("写入过期会话失败: %v", err)
	}
	requireStatus(t, e.callAs(http.MethodGet, "/api/auth/me", nil, expired), http.StatusUnauthorized)

	requireStatus(t, e.callAs(http.MethodPost, "/api/auth/logout", nil, session.Value), http.StatusOK)
	requireStatus(t, e.callAs(http.MethodGet, "/api/auth/me", nil, session.Value), http.StatusUnauthorized)
}

func TestLoginLockout(t *testing.T) {
	e := newAPIEnv(t)
	wrong := map[string]string{"username": "admin", "password": "WrongPass1!"}
	for i := 0; i < loginMaxFails; i++ {
		requireStatus(t, e.callAnon(http.MethodPost, "/api/auth/login", wrong), http.StatusUnauthorized)
	}

	rec := e.callAnon(http.MethodPost, "/api/auth/login", map[string]string{
		"username": "admin", "password": "Abcdef12!",
	})
	requireStatus(t, rec, http.StatusTooManyRequests)
	if !strings.Contains(rec.Body.String(), "尝试次数过多") {
		t.Errorf("锁定提示异常: %s", rec.Body.String())
	}
}

func TestChangePassword(t *testing.T) {
	e := newAPIEnv(t)
	token := e.login("admin", "Abcdef12!")

	requireStatus(t, e.callAs(http.MethodPost, "/api/auth/password", map[string]string{
		"old_password": "Abcdef12!", "new_password": "short",
	}, token), http.StatusBadRequest)
	requireStatus(t, e.callAs(http.MethodPost, "/api/auth/password", map[string]string{
		"old_password": "Wrong123!", "new_password": "NewPass123!",
	}, token), http.StatusBadRequest)
	requireStatus(t, e.callAs(http.MethodPost, "/api/auth/password", map[string]string{
		"old_password": "Abcdef12!", "new_password": "NewPass123!",
	}, token), http.StatusOK)

	// 改密后旧会话立即失效
	requireStatus(t, e.callAs(http.MethodGet, "/api/auth/me", nil, token), http.StatusUnauthorized)
	requireStatus(t, e.callAnon(http.MethodPost, "/api/auth/login", map[string]string{
		"username": "admin", "password": "NewPass123!",
	}), http.StatusOK)
	requireStatus(t, e.callAnon(http.MethodPost, "/api/auth/login", map[string]string{
		"username": "admin", "password": "Abcdef12!",
	}), http.StatusUnauthorized)
}

func TestChangePasswordInvalidJSON(t *testing.T) {
	e := newAPIEnv(t)
	token := e.login("admin", "Abcdef12!")
	requireStatus(t, e.callAs(http.MethodPost, "/api/auth/password", "{", token), http.StatusBadRequest)
}

// currentUser 只认 *model.User；上下文里塞了别的类型应安全返回 nil。
func TestCurrentUserWrongType(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(ctxUserKey, "not-a-user")
	if u := currentUser(c); u != nil {
		t.Errorf("类型不符时应返回 nil，实际 %+v", u)
	}
}

func TestLoginGuardPruneCache(t *testing.T) {
	now := time.Now()
	g := &loginGuard{cache: map[string]*loginAttempt{
		"expired": {fails: 5, until: now.Add(-2 * loginCacheTTL)},
		"active":  {fails: 5, until: now.Add(time.Minute)},
		"perm":    {fails: 5},
		"nil":     nil,
	}, ttl: loginCacheTTL}

	g.pruneCache(now)

	if _, ok := g.cache["expired"]; ok {
		t.Error("过期条目应被清理")
	}
	if _, ok := g.cache["nil"]; ok {
		t.Error("空条目应被清理")
	}
	if !aCacheTime(nil).IsZero() {
		t.Error("nil 登录尝试应返回零值时间")
	}
	for _, k := range []string{"active", "perm"} {
		if _, ok := g.cache[k]; !ok {
			t.Errorf("%q 不应被清理", k)
		}
	}
}

// 非管理员、或已禁用的管理员，删除时走普通分支而不是最后一个管理员保护。
func TestDeleteUserUnprotectedBranch(t *testing.T) {
	e := newAPIEnv(t)

	op := e.createUser("op2", "Abcdef12!", model.RoleOperator)
	requireStatus(t, e.call(http.MethodDelete, "/api/users/"+idPath(op.ID), nil), http.StatusOK)

	adm := e.createUser("admin2", "Abcdef12!", model.RoleAdmin)
	if err := e.st.UpdateUser(adm.ID, "", model.RoleAdmin, false); err != nil {
		t.Fatalf("禁用管理员失败: %v", err)
	}
	requireStatus(t, e.call(http.MethodDelete, "/api/users/"+idPath(adm.ID), nil), http.StatusOK)
}

func TestUsersCRUD(t *testing.T) {
	e := newAPIEnv(t)

	rec := e.call(http.MethodGet, "/api/users", nil)
	requireStatus(t, rec, http.StatusOK)
	var list struct {
		Total int          `json:"total"`
		Items []model.User `json:"items"`
	}
	decodeInto(t, rec, &list)
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("账号列表异常: %+v", list)
	}

	cases := []struct {
		name string
		body any
		want int
	}{
		{"非法 JSON", "{", http.StatusBadRequest},
		{"用户名过短", map[string]any{"username": "ab", "password": "Abcdef12!"}, http.StatusBadRequest},
		{"弱密码", map[string]any{"username": "operator1", "password": "12345678"}, http.StatusBadRequest},
		{"角色错误", map[string]any{"username": "operator1", "password": "Abcdef12!", "role": "root"}, http.StatusBadRequest},
		{"显示名过长", map[string]any{"username": "operator1", "password": "Abcdef12!", "display_name": strings.Repeat("长", 41)}, http.StatusBadRequest},
		{"用户名重复", map[string]any{"username": "admin", "password": "Abcdef12!"}, http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			requireStatus(t, e.call(http.MethodPost, "/api/users", c.body), c.want)
		})
	}

	rec = e.call(http.MethodPost, "/api/users", map[string]any{
		"username": "operator1", "password": "Abcdef12!",
	})
	requireStatus(t, rec, http.StatusOK)
	var operator model.User
	decodeInto(t, rec, &operator)
	if operator.Role != model.RoleOperator || !operator.Enabled {
		t.Fatalf("默认角色应为运营且启用: %+v", operator)
	}
	opID := idPath(operator.ID)
	meID := idPath(e.admin.ID)

	requireStatus(t, e.call(http.MethodPatch, "/api/users/abc", map[string]any{}), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPatch, "/api/users/9999", map[string]any{"display_name": "x"}), http.StatusNotFound)
	requireStatus(t, e.call(http.MethodPatch, "/api/users/"+meID, map[string]any{"enabled": false}), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPatch, "/api/users/"+meID, map[string]any{"role": model.RoleOperator}), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPatch, "/api/users/"+opID, map[string]any{"role": "root"}), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPatch, "/api/users/"+opID, map[string]any{
		"display_name": strings.Repeat("长", 41),
	}), http.StatusBadRequest)

	rec = e.call(http.MethodPatch, "/api/users/"+opID, map[string]any{
		"display_name": "运营一号", "role": model.RoleAdmin, "enabled": true,
	})
	requireStatus(t, rec, http.StatusOK)
	var updated model.User
	decodeInto(t, rec, &updated)
	if updated.DisplayName != "运营一号" || updated.Role != model.RoleAdmin {
		t.Fatalf("账号更新未生效: %+v", updated)
	}

	requireStatus(t, e.call(http.MethodPost, "/api/users/abc/password", map[string]any{"password": "Abcdef12!"}), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPost, "/api/users/9999/password", map[string]any{"password": "Abcdef12!"}), http.StatusNotFound)
	requireStatus(t, e.call(http.MethodPost, "/api/users/"+opID+"/password", map[string]any{"password": "short"}), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPost, "/api/users/"+opID+"/password", map[string]any{"password": "Reset123!"}), http.StatusOK)

	requireStatus(t, e.call(http.MethodDelete, "/api/users/abc", nil), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodDelete, "/api/users/9999", nil), http.StatusNotFound)
	requireStatus(t, e.call(http.MethodDelete, "/api/users/"+meID, nil), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodDelete, "/api/users/"+opID, nil), http.StatusOK)
	requireStatus(t, e.call(http.MethodDelete, "/api/users/"+opID, nil), http.StatusNotFound)
}

func TestOperatorForbidden(t *testing.T) {
	e := newAPIEnv(t)
	operator := e.createUser("operator", "Abcdef12!", model.RoleOperator)
	token := e.newSession(operator.ID)

	requireStatus(t, e.callAs(http.MethodGet, "/api/settings", nil, token), http.StatusForbidden)
	requireStatus(t, e.callAs(http.MethodGet, "/api/users", nil, token), http.StatusForbidden)
	// 普通资源接口运营仍可访问
	requireStatus(t, e.callAs(http.MethodGet, "/api/links", nil, token), http.StatusOK)
}
