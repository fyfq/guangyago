package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
)

func init() { gin.SetMode(gin.TestMode) }

func TestValidPassword(t *testing.T) {
	cases := []struct {
		p    string
		want bool
	}{
		{"", false},
		{"short1", false},                     // 7 字节
		{"abcdefgh", false},                   // 单一类别
		{"12345678", false},                   // 黑名单
		{"PASSWORD1", false},                  // 黑名单（大小写不敏感）
		{"admin123", false},                   // 黑名单
		{"hunter2-hunter2", true},             // lower + digit + symbol
		{"Abcdefg1", true},                    // 3 类
		{"Abcdefgh", true},                    // upper + lower
		{strings.Repeat("A1", 64), true},      // 128 字节
		{strings.Repeat("A1", 65), false},     // 129 字节超限
	}
	for _, c := range cases {
		if got := validPassword(c.p); got != c.want {
			t.Errorf("validPassword(%q) = %v, 期望 %v", trunc(c.p), got, c.want)
		}
	}
}

func TestPasswordHintFor(t *testing.T) {
	if hint := passwordHintFor("abc"); hint != "密码至少 8 位" {
		t.Errorf("短密码提示异常: %q", hint)
	}
	if hint := passwordHintFor(strings.Repeat("A1", 65)); hint != "密码过长" {
		t.Errorf("长密码提示异常: %q", hint)
	}
	if hint := passwordHintFor("password1"); !strings.Contains(hint, "常见") {
		t.Errorf("弱口令提示应含'常见'字样: %q", hint)
	}
}

func TestSanitizeCanonicalPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "/"},
		{"/", "/"},
		{"/admin/users", "/admin/users"},
		{"/a b/c", "/ab/c"},
		{`/"><script>`, "/script"},                // 尖括号/引号丢弃，字母保留
		{"/a%20b", "/a20b"},                        // % 不在白名单被丢弃，数字保留
		{"/~user_1.0-v", "/~user_1.0-v"},           // RFC 3986 未保留字符全通过
		{"foo", "/foo"},                            // 无 / 前缀时补齐
	}
	for _, c := range cases {
		if got := sanitizeCanonicalPath(c.in); got != c.want {
			t.Errorf("sanitizeCanonicalPath(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeCanonicalQuery(t *testing.T) {
	cases := []struct {
		name string
		in   map[string][]string
		want string
	}{
		{"empty", map[string][]string{}, ""},
		{"only-allowed", map[string][]string{"id": {"42"}, "sort": {"new"}}, "?id=42&sort=new"},
		{"drop-unknown", map[string][]string{"session_id": {"abc"}}, ""},
		{"strip-chars", map[string][]string{"q": {`he"<llo`}}, "?q=hello"},
		{"empty-value", map[string][]string{"id": {""}}, ""},
		{"first-wins", map[string][]string{"id": {"1", "2"}}, "?id=1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeCanonicalQuery(c.in); got != c.want {
				t.Errorf("sanitizeCanonicalQuery(%v) = %q, 期望 %q", c.in, got, c.want)
			}
		})
	}
}

func TestInjectHeadTags(t *testing.T) {
	html := "<html><head><title>x</title></head><body/></html>"
	got := injectHeadTags(html, `<meta name="a" content="b"/>`)
	if !strings.Contains(got, `<meta name="a" content="b"/>`) {
		t.Error("标签未注入")
	}
	if strings.Index(got, `<meta name="a"`) >= strings.Index(got, "</head>") {
		t.Error("标签应位于 </head> 之前")
	}
	if injectHeadTags(html, "") != html {
		t.Error("空 tags 应原样返回")
	}
	loose := "<html><body/></html>"
	if injectHeadTags(loose, "x") != loose {
		t.Error("无 head 时应原样返回")
	}
}

func TestGoogleMetaTag_Whitelist(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	s.cfg.SEO.GoogleVerification = "abc"
	if got := s.googleMetaTag(); got != "" {
		t.Errorf("短 token 应拒绝，实际: %q", got)
	}
	s.cfg.SEO.GoogleVerification = "a\"><script>alert(1)</script>"
	if got := s.googleMetaTag(); got != "" {
		t.Errorf("非白名单字符应拒绝，实际: %q", got)
	}
	s.cfg.SEO.GoogleVerification = strings.Repeat("A", 42)
	got := s.googleMetaTag()
	if !strings.Contains(got, `<meta name="google-site-verification" content="`) {
		t.Errorf("合法 token 未生成 meta 标签: %q", got)
	}
}

func TestRequireSameOrigin(t *testing.T) {
	s := &Server{}
	r := gin.New()
	r.Use(s.requireSameOrigin())
	r.POST("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Host = "app.local"
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET 应通过，实际 %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/x", nil)
	req.Host = "app.local"
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("无 Origin/Referer 的 POST 应放行，实际 %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/x", nil)
	req.Host = "app.local"
	req.Header.Set("Origin", "http://app.local")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("同源 Origin 应放行，实际 %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/x", nil)
	req.Host = "app.local"
	req.Header.Set("Origin", "http://evil.example")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("跨源 Origin 应 403，实际 %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/x", nil)
	req.Host = "app.local"
	req.Header.Set("Referer", "http://evil.example/page")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("跨源 Referer 应 403，实际 %d", rec.Code)
	}
}

func TestSecureCookie(t *testing.T) {
	trueVal, falseVal := true, false
	cases := []struct {
		name       string
		override   *bool
		tlsConn    bool
		fwdProto   string
		wantSecure bool
	}{
		{"默认无 TLS", nil, false, "", false},
		{"默认 TLS 直连", nil, true, "", true},
		{"反代 https", nil, false, "https", true},
		{"显式覆盖 true", &trueVal, false, "", true},
		{"显式覆盖 false", &falseVal, true, "https", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{cfg: &config.Config{}}
			s.cfg.Server.SecureCookie = c.override
			req := httptest.NewRequest("GET", "/", nil)
			if c.tlsConn {
				req.TLS = &tls.ConnectionState{}
			}
			if c.fwdProto != "" {
				req.Header.Set("X-Forwarded-Proto", c.fwdProto)
			}
			// secureCookie 只读 Request，不触碰 Writer；构造最小 gin.Context 即可。
			c2 := &gin.Context{Request: req}
			if got := s.secureCookie(c2); got != c.wantSecure {
				t.Errorf("secureCookie()=%v 期望 %v", got, c.wantSecure)
			}
		})
	}
}

func TestApplyStatusFilter(t *testing.T) {
	cases := []struct {
		raw     string
		wantOne model.Status
		want    []model.Status
	}{
		{"", "", nil},
		{"all", "", nil},
		{"pending", "pending", nil},
		{"pending,scraped", "", []model.Status{"pending", "scraped"}},
		{"scraped_set", "", model.ScrapedOnly},
		{"pending_set", "", model.PendingScrape},
		{"push_set", "", model.PushQueue},
	}
	for _, c := range cases {
		f := model.ListFilter{}
		applyStatusFilter(&f, c.raw)
		if f.Status != c.wantOne {
			t.Errorf("raw=%q: Status=%q 期望 %q", c.raw, f.Status, c.wantOne)
		}
		if c.want == nil {
			if len(f.Statuses) != 0 {
				t.Errorf("raw=%q: Statuses 应为空，实际 %v", c.raw, f.Statuses)
			}
			continue
		}
		if len(f.Statuses) != len(c.want) {
			t.Errorf("raw=%q: Statuses=%v 期望 %v", c.raw, f.Statuses, c.want)
			continue
		}
		for i, v := range c.want {
			if f.Statuses[i] != v {
				t.Errorf("raw=%q: Statuses[%d]=%q 期望 %q", c.raw, i, f.Statuses[i], v)
			}
		}
	}
}

func TestPlainText_Strip(t *testing.T) {
	got := plainText(`<b>标题</b><br>副标题 &amp; tail`)
	if strings.Contains(got, "<") || strings.Contains(got, "&amp;") {
		t.Errorf("plainText 未清理标签/实体: %q", got)
	}
	if !strings.Contains(got, "标题") || !strings.Contains(got, "tail") {
		t.Errorf("plainText 应保留文本主体: %q", got)
	}
}

func TestLooksLikeCSV(t *testing.T) {
	if !looksLikeCSV("链接,提取码\nhttps://x") {
		t.Error("首行含'链接'应识别为 CSV")
	}
	if !looksLikeCSV("a,b,c") {
		t.Error("首行 2+ 逗号应识别为 CSV")
	}
	if looksLikeCSV("光鸭云盘用户给你分享了《X》") {
		t.Error("纯分享文本不应识别为 CSV")
	}
}

func TestLoginGuard_ObserveBlocksAfterLock(t *testing.T) {
	g := newLoginGuard()
	now := time.Now()
	g.cache[keyIP("1.2.3.4")] = &loginAttempt{fails: loginMaxFails, until: now.Add(loginLockFor)}
	if blocked := g.observe(nil, keyIP("1.2.3.4"), now); !blocked {
		t.Error("缓存中标记锁定后 observe 应返回 true")
	}
	// 锁定到期后 observe 应返回 false。
	// 注意 observe 内部会 load，命中缓存的 TTL 判断走 aCacheTime==until 分支；
	// 这里 until 已过期，直接检查现在时间。
	g.cache[keyIP("1.2.3.4")] = &loginAttempt{fails: 5, until: now.Add(-time.Second)}
	if blocked := g.observe(nil, keyIP("1.2.3.4"), now); blocked {
		t.Error("锁定到期后 observe 应返回 false")
	}
	if blocked := g.observe(nil, "", now); blocked {
		t.Error("空 key 应直接返回 false")
	}
}

func trunc(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}
