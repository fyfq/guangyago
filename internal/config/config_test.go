package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChatID_Any(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"", nil},
		{"0", nil},
		{"  ", nil},
		{"-1001234567890", int64(-1001234567890)},
		{"@channel", "@channel"},
		{"channel", "@channel"},
		{"  @chan  ", "@chan"},
	}
	for _, c := range cases {
		got := ChatID(c.in).Any()
		if got != c.want {
			t.Errorf("ChatID(%q).Any() = %#v, 期望 %#v", c.in, got, c.want)
		}
	}
}

func TestTelegramRate_EffectiveIntervalMS(t *testing.T) {
	// 默认值（未设置 IntervalMS 时）
	if got := (TelegramRate{}).EffectiveIntervalMS(); got != DefaultTelegramIntervalMS {
		t.Errorf("默认间隔应为 %d，实际 %d", DefaultTelegramIntervalMS, got)
	}
	// 群组下限：3100 已在 3000ms 以上，保持原值
	if got := (TelegramRate{IntervalMS: 2500}).EffectiveIntervalMS(); got != telegramGroupMinIntervalMS {
		t.Errorf("群组场景应夹到 %d，实际 %d", telegramGroupMinIntervalMS, got)
	}
	// 频道场景下限 1000
	if got := (TelegramRate{IntervalMS: 800, ChannelOnly: true}).EffectiveIntervalMS(); got != telegramMinIntervalMS {
		t.Errorf("频道场景应夹到 %d，实际 %d", telegramMinIntervalMS, got)
	}
	// 共享凭据 ×1.5
	if got := (TelegramRate{IntervalMS: 3100, SharedCredentials: true}).EffectiveIntervalMS(); got != 4650 {
		t.Errorf("共享凭据应放慢 1.5 倍，期望 4650 实际 %d", got)
	}
	// 付费广播不夹下限
	if got := (TelegramRate{IntervalMS: 200, PaidBroadcast: true}).EffectiveIntervalMS(); got != 200 {
		t.Errorf("付费广播应保留原值，实际 %d", got)
	}
}

func TestTMDBRate_EffectivePerSecond(t *testing.T) {
	if got := (TMDBRate{}).EffectivePerSecond(); got != DefaultTMDBPerSecond {
		t.Errorf("默认速率应为 %v，实际 %v", float64(DefaultTMDBPerSecond), got)
	}
	// 共享凭据减半
	if got := (TMDBRate{PerSecond: 10, SharedCredentials: true}).EffectivePerSecond(); got != 5 {
		t.Errorf("共享凭据应减半，实际 %v", got)
	}
	// 下限 0.1
	if got := (TMDBRate{PerSecond: 0.01}).EffectivePerSecond(); got != 0.1 {
		t.Errorf("低于 0.1 应夹到 0.1，实际 %v", got)
	}
	// 软阈值 40
	if got := (TMDBRate{PerSecond: 500}).EffectivePerSecond(); got != tmdbSoftCeiling {
		t.Errorf("超软阈值应夹到 %d，实际 %v", tmdbSoftCeiling, got)
	}
}

func TestGuangyaRate_EffectiveConcurrency(t *testing.T) {
	if got := (GuangyaRate{}).EffectiveConcurrency(); got != DefaultGuangyaConcurrency {
		t.Errorf("默认并发应为 %d，实际 %d", DefaultGuangyaConcurrency, got)
	}
	if got := (GuangyaRate{Concurrency: 5}).EffectiveConcurrency(); got != 5 {
		t.Errorf("显式并发应透传，实际 %d", got)
	}
}

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Server.Addr != ":8080" {
		t.Errorf("默认端口异常: %q", cfg.Server.Addr)
	}
	if cfg.Pipeline.Workers != 2 {
		t.Errorf("默认工作线程数异常: %d", cfg.Pipeline.Workers)
	}
	if cfg.Rate.TMDB.PerSecond != DefaultTMDBPerSecond {
		t.Errorf("默认 TMDB 速率异常: %v", cfg.Rate.TMDB.PerSecond)
	}
	if cfg.SEO.SiteName != "光鸭 GO" {
		t.Errorf("默认站点名异常: %q", cfg.SEO.SiteName)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("TMDB_API_KEY", "tmdb-from-env")
	t.Setenv("TG_BOT_TOKEN", "tg-from-env")
	t.Setenv("GUANGYA_COOKIE", "cookie-from-env")
	t.Setenv("PROXY_ADDR", "127.0.0.1:7890")
	t.Setenv("PROXY_USERNAME", "u")
	t.Setenv("PROXY_PASSWORD", "p")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TMDB.APIKey != "tmdb-from-env" {
		t.Errorf("TMDB Key 未从 env 读取: %q", cfg.TMDB.APIKey)
	}
	if cfg.Telegram.Token != "tg-from-env" {
		t.Errorf("TG Token 未从 env 读取: %q", cfg.Telegram.Token)
	}
	if cfg.Netdisk.Cookie != "cookie-from-env" {
		t.Errorf("Cookie 未从 env 读取: %q", cfg.Netdisk.Cookie)
	}
	if !cfg.Proxy.Enable || cfg.Proxy.Addr != "127.0.0.1:7890" ||
		cfg.Proxy.Username != "u" || cfg.Proxy.Password != "p" {
		t.Errorf("Proxy 未从 env 完整装配: %+v", cfg.Proxy)
	}
}

func TestLoad_NonexistentFileIsFine(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "not-here.yaml"))
	if err != nil {
		t.Fatalf("不存在的配置文件不应报错: %v", err)
	}
	if cfg.Server.Addr != ":8080" {
		t.Errorf("应回落到默认配置，实际: %q", cfg.Server.Addr)
	}
}

func TestSave_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	cfg := Default()
	cfg.TMDB.APIKey = "saved-key"
	cfg.Server.SecureCookie = boolPtr(true)
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("配置文件权限 %o，期望 0600", perm)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TMDB.APIKey != "saved-key" {
		t.Errorf("回读 API Key 不一致: %q", loaded.TMDB.APIKey)
	}
	if loaded.Server.SecureCookie == nil || *loaded.Server.SecureCookie != true {
		t.Errorf("回读 secure_cookie 应为 true: %+v", loaded.Server.SecureCookie)
	}
}

func boolPtr(v bool) *bool { return &v }
