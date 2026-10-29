package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/auth"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/pipeline"
	"github.com/odin/guangya-go/internal/store"
)

// apiEnv 是一套真实路由 + 内存数据库的测试环境，
// 直接走 Server.Register 挂出来的路由，覆盖中间件与状态码契约。
type apiEnv struct {
	t       *testing.T
	st      *store.Store
	srv     *Server
	r       *gin.Engine
	admin   *model.User
	token   string
	cfg     *config.Config
	cfgPath string
	pipe    *pipeline.Pipeline
	reloads int
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	return newAPIEnvWithAdmin(t, true)
}

// newAPIEnvWithAdmin 可选择不预置管理员，供安装向导测试使用。
func newAPIEnvWithAdmin(t *testing.T, withAdmin bool) *apiEnv {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	env := &apiEnv{
		t:       t,
		st:      st,
		cfg:     config.Default(),
		cfgPath: filepath.Join(t.TempDir(), "config.yaml"),
	}
	secure := false
	env.cfg.Server.SecureCookie = &secure
	env.pipe = pipeline.New(st, nil, nil)
	t.Cleanup(env.pipe.Stop)

	env.srv = New(st).WithRuntime(env.pipe, env.cfg, env.cfgPath, func(*config.Config) { env.reloads++ })
	env.r = gin.New()
	env.srv.Register(env.r)

	if withAdmin {
		env.admin = env.createUser("admin", "Abcdef12!", model.RoleAdmin)
		env.token = env.newSession(env.admin.ID)
	}
	return env
}

func (e *apiEnv) createUser(username, password, role string) *model.User {
	e.t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		e.t.Fatalf("生成密码哈希失败: %v", err)
	}
	u := &model.User{Username: username, DisplayName: username, Role: role, Enabled: true}
	if err := e.st.CreateUser(u, hash); err != nil {
		e.t.Fatalf("创建账号失败: %v", err)
	}
	return u
}

func (e *apiEnv) newSession(userID int64) string {
	e.t.Helper()
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		e.t.Fatalf("生成会话令牌失败: %v", err)
	}
	if err := e.st.CreateSession(userID, hash, time.Now().Add(time.Hour)); err != nil {
		e.t.Fatalf("写入会话失败: %v", err)
	}
	return token
}

func (e *apiEnv) call(method, path string, body any) *httptest.ResponseRecorder {
	return e.callAs(method, path, body, e.token)
}

func (e *apiEnv) callAnon(method, path string, body any) *httptest.ResponseRecorder {
	return e.callAs(method, path, body, "")
}

func (e *apiEnv) callAs(method, path string, body any, token string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := newJSONRequest(e.t, method, path, body)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	}
	return e.serve(req)
}

func (e *apiEnv) serve(req *http.Request) *httptest.ResponseRecorder {
	e.t.Helper()
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

// newJSONRequest 按 body 类型构造请求：string/[]byte 原样发送，其余序列化为 JSON。
func newJSONRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	case []byte:
		rdr = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func requireStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("状态码 = %d，期望 %d，响应: %s", rec.Code, want, rec.Body.String())
	}
}

func decodeInto(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v，响应: %s", err, rec.Body.String())
	}
}

func idPath(id int64) string { return strconv.FormatInt(id, 10) }

// login 走真实登录接口，返回下发的会话令牌。
func (e *apiEnv) login(username, password string) string {
	e.t.Helper()
	rec := e.callAnon(http.MethodPost, "/api/auth/login", map[string]string{
		"username": username,
		"password": password,
	})
	requireStatus(e.t, rec, http.StatusOK)
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName && c.Value != "" {
			return c.Value
		}
	}
	e.t.Fatal("登录响应缺少会话 Cookie")
	return ""
}

// seedLink 写一条待处理记录。
func (e *apiEnv) seedLink(shareID, rawTitle string) *model.Link {
	e.t.Helper()
	l := &model.Link{
		ShareID:  shareID,
		URL:      "https://guangyapan.com/s/" + shareID + "_x",
		RawTitle: rawTitle,
		Title:    rawTitle,
		Status:   model.StatusPending,
	}
	ok, err := e.st.Create(l)
	if err != nil || !ok {
		e.t.Fatalf("写入链接失败: ok=%v err=%v", ok, err)
	}
	return l
}

// seedPublishedLink 模拟走完刮削 + 推送的记录，只在门户可见。
func (e *apiEnv) seedPublishedLink(shareID string, scraped *model.Link) *model.Link {
	e.t.Helper()
	l := &model.Link{
		ShareID:  shareID,
		URL:      "https://guangyapan.com/s/" + shareID + "_x",
		RawTitle: scraped.Title,
		Title:    scraped.Title,
		SharedAt: scraped.SharedAt,
		Status:   model.StatusPending,
	}
	ok, err := e.st.Create(l)
	if err != nil || !ok {
		e.t.Fatalf("写入链接失败: ok=%v err=%v", ok, err)
	}
	if err := e.st.UpdateScraped(l.ID, scraped); err != nil {
		e.t.Fatalf("写入刮削结果失败: %v", err)
	}
	if err := e.st.UpdatePushed(l.ID, 42); err != nil {
		e.t.Fatalf("写入推送结果失败: %v", err)
	}
	return l
}
