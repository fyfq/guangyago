package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/pipeline"
	"github.com/odin/guangya-go/internal/store"
)

type settingsView struct {
	TMDBConfigured     bool   `json:"tmdb_configured"`
	TelegramConfigured bool   `json:"telegram_configured"`
	ChatID             string `json:"chat_id"`
	AutoPush           bool   `json:"auto_push"`
	Workers            int    `json:"workers"`
	Proxy              struct {
		Enable      bool   `json:"enable"`
		Addr        string `json:"addr"`
		Username    string `json:"username"`
		HasPassword bool   `json:"has_password"`
	} `json:"proxy"`
	RateLimit struct {
		TMDB struct {
			Enable    bool    `json:"enable"`
			PerSecond float64 `json:"per_second"`
			Burst     int     `json:"burst"`
			Shared    bool    `json:"shared_credentials"`
		} `json:"tmdb"`
		Guangya struct {
			Enable      bool `json:"enable"`
			Concurrency int  `json:"concurrency"`
		} `json:"guangya"`
	} `json:"rate_limit"`
	SEO struct {
		GoogleVerification string `json:"google_verification"`
		BaseURL            string `json:"base_url"`
		SiteName           string `json:"site_name"`
	} `json:"seo"`
}

func TestSettingsAPI(t *testing.T) {
	e := newAPIEnv(t)

	rec := e.call(http.MethodGet, "/api/settings", nil)
	requireStatus(t, rec, http.StatusOK)
	var before settingsView
	decodeInto(t, rec, &before)
	if before.TMDBConfigured || before.TelegramConfigured {
		t.Fatal("初始配置不应标记为已填写密钥")
	}
	if before.SEO.SiteName != "光鸭 GO" {
		t.Errorf("默认站点名异常: %q", before.SEO.SiteName)
	}

	requireStatus(t, e.call(http.MethodPost, "/api/settings", "{bad"), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPost, "/api/settings", map[string]any{
		"seo": map[string]any{"google_verification": "not a token!"},
	}), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPost, "/api/settings", map[string]any{
		"seo": map[string]any{"base_url": "ftp://gy.example.com"},
	}), http.StatusBadRequest)

	rec = e.call(http.MethodPost, "/api/settings", map[string]any{
		"tmdb_api_key": "tmdb-key",
		"tg_token":     "1:abc",
		"chat_id":      " -100123 ",
		"auto_push":    false,
		"proxy": map[string]any{
			"enable": true, "addr": "socks5://127.0.0.1:1080", "username": "u", "password": "p",
		},
		"rate_limit": map[string]any{
			"tmdb":     map[string]any{"enable": true, "per_second": 5.5, "burst": 0, "shared_credentials": true},
			"telegram": map[string]any{"enable": true, "interval_ms": 1500, "channel_only": true},
			"guangya":  map[string]any{"enable": true, "concurrency": 3},
		},
		"seo": map[string]any{
			"google_verification": "abcdefghij",
			"base_url":            "https://gy.example.com/",
			"site_name":           " 光鸭站点 ",
		},
		"save_to_file": true,
	})
	requireStatus(t, rec, http.StatusOK)

	if e.cfg.TMDB.APIKey != "tmdb-key" || e.cfg.Telegram.Token != "1:abc" {
		t.Errorf("密钥未写入配置: %+v", e.cfg)
	}
	if string(e.cfg.Telegram.ChatID) != "-100123" {
		t.Errorf("chat_id 未去除空白: %q", string(e.cfg.Telegram.ChatID))
	}
	if e.cfg.Pipeline.AutoPush {
		t.Error("auto_push=false 未生效")
	}
	if e.cfg.Rate.TMDB.Burst != 1 {
		t.Errorf("burst 下限应夹到 1，实际 %d", e.cfg.Rate.TMDB.Burst)
	}
	if e.cfg.SEO.BaseURL != "https://gy.example.com" {
		t.Errorf("base_url 应去掉尾部斜杠，实际 %q", e.cfg.SEO.BaseURL)
	}
	if e.cfg.SEO.SiteName != "光鸭站点" {
		t.Errorf("site_name 应去除空白，实际 %q", e.cfg.SEO.SiteName)
	}
	if e.reloads != 1 {
		t.Errorf("保存配置后应重建一次组件，实际 %d", e.reloads)
	}

	rec = e.call(http.MethodGet, "/api/settings", nil)
	requireStatus(t, rec, http.StatusOK)
	var after settingsView
	decodeInto(t, rec, &after)
	if !after.TMDBConfigured || !after.TelegramConfigured {
		t.Error("写入密钥后应标记为已配置")
	}
	if after.ChatID != "-100123" || after.AutoPush {
		t.Errorf("配置回读不一致: %+v", after)
	}
	if !after.Proxy.Enable || !after.Proxy.HasPassword {
		t.Errorf("代理配置未回读: %+v", after.Proxy)
	}
	if after.RateLimit.TMDB.Burst != 1 || after.RateLimit.Guangya.Concurrency != 3 {
		t.Errorf("限流配置未回读: %+v", after.RateLimit)
	}
	if after.SEO.GoogleVerification != "abcdefghij" || after.SEO.BaseURL != "https://gy.example.com" {
		t.Errorf("SEO 配置未回读: %+v", after.SEO)
	}

	// burst 上限同样收口
	rec = e.call(http.MethodPost, "/api/settings", map[string]any{
		"rate_limit": map[string]any{"tmdb": map[string]any{"burst": 200}},
	})
	requireStatus(t, rec, http.StatusOK)
	if e.cfg.Rate.TMDB.Burst != 100 {
		t.Errorf("burst 上限应夹到 100，实际 %d", e.cfg.Rate.TMDB.Burst)
	}
	if e.reloads != 2 {
		t.Errorf("第二次保存应再重建一次，实际 %d", e.reloads)
	}

	requireStatus(t, e.call(http.MethodPost, "/api/settings/test", map[string]any{"target": "nope"}), http.StatusBadRequest)
	// 三项连通性测试都用空凭据或关闭代理，避免测试访问外网
	proxyOff := false
	requireStatus(t, e.call(http.MethodPost, "/api/settings/test", map[string]any{
		"target": "proxy", "proxy_enable": proxyOff,
	}), http.StatusOK)
	requireStatus(t, e.call(http.MethodPost, "/api/settings/test", map[string]any{
		"target": "tmdb", "tmdb_api_key": "  ",
	}), http.StatusOK)
	requireStatus(t, e.call(http.MethodPost, "/api/settings/test", map[string]any{
		"target": "telegram", "tg_token": "  ",
	}), http.StatusOK)
}

func TestSettingsWithoutRuntime(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s := New(st)
	r := gin.New()
	s.Register(r)
	e := &apiEnv{t: t, st: st, srv: s, r: r}
	e.admin = e.createUser("admin", "Abcdef12!", model.RoleAdmin)
	e.token = e.newSession(e.admin.ID)

	rec := e.call(http.MethodGet, "/api/settings", nil)
	requireStatus(t, rec, http.StatusOK)
	var view map[string]any
	decodeInto(t, rec, &view)
	if len(view) != 0 {
		t.Errorf("未加载配置时应返回空对象，实际 %v", view)
	}

	requireStatus(t, e.call(http.MethodPost, "/api/settings", map[string]any{}), http.StatusServiceUnavailable)
	requireStatus(t, e.call(http.MethodPost, "/api/settings/test", map[string]any{"target": "proxy"}), http.StatusServiceUnavailable)

	// 任务接口在 pipeline 缺失时统一 503
	requireStatus(t, e.call(http.MethodPost, "/api/scrape", map[string]any{}), http.StatusServiceUnavailable)
	requireStatus(t, e.call(http.MethodPost, "/api/push", map[string]any{}), http.StatusServiceUnavailable)
	requireStatus(t, e.call(http.MethodPost, "/api/tasks/cancel", map[string]any{}), http.StatusServiceUnavailable)
	requireStatus(t, e.call(http.MethodPost, "/api/links/1/scrape", nil), http.StatusServiceUnavailable)
	requireStatus(t, e.call(http.MethodPost, "/api/links/1/push", nil), http.StatusServiceUnavailable)
	requireStatus(t, e.call(http.MethodGet, "/api/tmdb/lookup", nil), http.StatusServiceUnavailable)

	rec = e.call(http.MethodGet, "/api/tasks", nil)
	requireStatus(t, rec, http.StatusOK)
	var progress pipeline.Progress
	decodeInto(t, rec, &progress)
	if progress.Running {
		t.Errorf("没有 pipeline 时进度应为零值: %+v", progress)
	}

	// 手动指定 TMDB ID 只依赖数据库，不受 pipeline 影响
	l := e.seedLink("no-runtime", "无运行时")
	rec = e.call(http.MethodPost, "/api/links/"+idPath(l.ID)+"/tmdb", map[string]any{"tmdb_id": 3})
	requireStatus(t, rec, http.StatusOK)
	got, err := e.st.Get(l.ID)
	if err != nil || got == nil {
		t.Fatalf("读取记录失败: %v", err)
	}
	if got.TMDBID != 3 || got.MediaType != model.MediaMovie {
		t.Errorf("默认应写入 movie 类型: %+v", got)
	}
}

func TestSettingsSiteSubtitle(t *testing.T) {
	e := newAPIEnv(t)
	for _, value := range []string{" 精选分享 ", "", "<script>测试</script>"} {
		rec := e.call(http.MethodPost, "/api/settings", map[string]any{
			"seo": map[string]any{"site_subtitle": value}, "save_to_file": true,
		})
		requireStatus(t, rec, http.StatusOK)
		saved, err := config.Load(e.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.TrimSpace(value)
		if saved.SEO.SiteSubtitle != want {
			t.Fatalf("副标题未持久化: %q", saved.SEO.SiteSubtitle)
		}
		rec = e.call(http.MethodGet, "/api/branding", nil)
		requireStatus(t, rec, http.StatusOK)
		var brand brandingPayload
		decodeInto(t, rec, &brand)
		if brand.SiteSubtitle != want {
			t.Fatalf("门户副标题不一致: %q", brand.SiteSubtitle)
		}
		requireStatus(t, e.call(http.MethodPost, "/api/settings", map[string]any{"seo": map[string]any{"site_name": "站点"}}), http.StatusOK)
		if e.cfg.SEO.SiteSubtitle != want {
			t.Fatal("省略副标题时不应覆盖")
		}
	}
}

// 反代/隧道下这个地址决定提交类接口能不能过同源校验，必须能改、能落盘、能回显。
func TestSettingsPublicURL(t *testing.T) {
	e := newAPIEnv(t)
	requireStatus(t, e.call(http.MethodPost, "/api/settings", map[string]any{
		"public_url":   "https://gy.example.com/",
		"save_to_file": true,
	}), http.StatusOK)

	saved, err := config.Load(e.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Server.PublicURL != "https://gy.example.com" {
		t.Fatalf("对外地址未持久化或未去掉结尾斜杠: %q", saved.Server.PublicURL)
	}

	rec := e.call(http.MethodGet, "/api/settings", nil)
	requireStatus(t, rec, http.StatusOK)
	var got struct {
		PublicURL string `json:"public_url"`
	}
	decodeInto(t, rec, &got)
	if got.PublicURL != "https://gy.example.com" {
		t.Fatalf("GET /api/settings 未回显对外地址: %+v", got)
	}

	requireStatus(t, e.call(http.MethodPost, "/api/settings", map[string]any{"public_url": "gy.example.com"}), http.StatusBadRequest)
}
