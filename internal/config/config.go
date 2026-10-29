package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ProxyConfig struct {
	Enable   bool   `yaml:"enable"`
	Addr     string `yaml:"addr"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Storage  StorageConfig  `yaml:"storage"`
	TMDB     TMDBConfig     `yaml:"tmdb"`
	Telegram TelegramConfig `yaml:"telegram"`
	Netdisk  NetdiskConfig  `yaml:"netdisk"`
	Pipeline PipelineConfig `yaml:"pipeline"`
	Proxy    ProxyConfig    `yaml:"proxy"`
	Rate     RateConfig     `yaml:"rate_limit"`
	SEO      SEOConfig      `yaml:"seo"`
}

// SEOConfig 收录与搜索引擎站长验证相关配置。
type SEOConfig struct {
	// GoogleVerification 是 Google Search Console「HTML 标签」方式给出的 token，
	// 会被注入成 <meta name="google-site-verification" content="...">，用于站点所有权验证。
	GoogleVerification string `yaml:"google_verification"`
	// BaseURL 对外访问的根地址（如 https://gy.example.com）。
	// 留空则按请求头推断；反向代理后建议固定，否则 sitemap 里的域名可能不准。
	BaseURL string `yaml:"base_url"`
	// SiteName 出现在结构化数据与站点地图说明中。
	SiteName string `yaml:"site_name"`
	// LogoFile / IconFile 是后台上传的自定义 logo、站点图标在 branding 目录下的文件名
	//（如 logo.png）。只存文件名：静态路由按白名单精确匹配后回源，避免拼接任意路径。
	// 留空表示用前端内置的默认标识。
	LogoFile string `yaml:"logo_file"`
	IconFile string `yaml:"icon_file"`
}

// BrandingDir 返回自定义品牌素材（logo、站点图标）的落盘目录。
func (c *Config) BrandingDir() string {
	dir := "data"
	if c != nil && strings.TrimSpace(c.Storage.DataDir) != "" {
		dir = c.Storage.DataDir
	}
	return filepath.Join(dir, "branding")
}

// RateConfig 汇总各三方服务的限流参数。
// 默认值一律取官方建议值：宁可慢一点，也不要撞上 429。
type RateConfig struct {
	TMDB     TMDBRate     `yaml:"tmdb"`
	Telegram TelegramRate `yaml:"telegram"`
	Guangya  GuangyaRate  `yaml:"guangya"`
}

// TMDBRate 对应 TMDB 官方说明：
// 旧的 40 次/10 秒限制已于 2019 年取消，目前保留一个约 40 次/秒的
// 上层保护阈值（软限制，随时可能调整，收到 429 就应当退让）。
// 默认只用 4 次/秒，留出十倍余量，批量刮削也不会触线。
type TMDBRate struct {
	Enable bool `yaml:"enable"`
	// PerSecond 每秒请求数上限。
	PerSecond float64 `yaml:"per_second"`
	// Burst 允许的瞬时突发数，吸收短时抖动。
	Burst int `yaml:"burst"`
	// SharedCredentials 表示同一个 Key 还被别的应用共用，此时自动再降一半。
	SharedCredentials bool `yaml:"shared_credentials"`
}

// TelegramRate 对应官方 Bot API 的限制：
//   - 同一会话不要超过 1 条/秒；
//   - 群发/群组场景不超过 20 条/分钟。
//
// 默认 3100ms 一条 ≈ 19 条/分钟，压在 20 条/分钟以内。
type TelegramRate struct {
	Enable bool `yaml:"enable"`
	// IntervalMS 两次发送之间的最小间隔（毫秒）。
	IntervalMS int `yaml:"interval_ms"`
	// ChannelOnly 推送目标只有频道（不含群组）时可打开，
	// 下限放宽到 1 条/秒，官方建议的频道节奏是 1.1 秒。
	ChannelOnly bool `yaml:"channel_only"`
	// SharedCredentials 同一个 Bot Token 还被别的应用共用，自动再降一档（×1.5 间隔）。
	SharedCredentials bool `yaml:"shared_credentials"`
	// PaidBroadcast 是否启用付费广播（可放宽上限）。默认 false，即不放宽。
	PaidBroadcast bool `yaml:"paid_broadcast"`
}

// GuangyaRate 对应光鸭网盘。官方尚未公布明确配额，
// 因此只做保守的并发控制：分享检测同时最多 2 个。
type GuangyaRate struct {
	Enable bool `yaml:"enable"`
	// Concurrency 分享检测并发上限。
	Concurrency int `yaml:"concurrency"`
}

// 官方口径的安全下限，任何用户配置都会被夹到这些值之上（付费广播除外）。
const (
	// telegramMinIntervalMS 同一会话 1 条/秒的硬下限。
	telegramMinIntervalMS = 1000
	// telegramGroupMinIntervalMS 群组 20 条/分钟换算出的下限。
	telegramGroupMinIntervalMS = 3000
	// tmdbSoftCeiling 是 TMDB 的上层保护阈值（约 40 次/秒，官方称随时可能调整）。
	tmdbSoftCeiling = 40
)

// EffectiveIntervalMS 计算 Telegram 实际生效的发送间隔：
// 先按官方下限夹一次，共享凭据再放慢，付费广播则不再夹下限。
func (r TelegramRate) EffectiveIntervalMS() int {
	ms := r.IntervalMS
	if ms <= 0 {
		ms = DefaultTelegramIntervalMS
	}
	if !r.PaidBroadcast {
		floor := telegramGroupMinIntervalMS
		if r.ChannelOnly {
			floor = telegramMinIntervalMS
		}
		if ms < floor {
			ms = floor
		}
	}
	if r.SharedCredentials {
		// 同一凭据被别的应用共用，再放慢 50%
		ms = int(float64(ms) * 1.5)
	}
	return ms
}

// EffectivePerSecond 计算 TMDB 实际生效的每秒请求数：
// 共享凭据时减半，并夹在 [0.1, 软阈值] 之间。
func (r TMDBRate) EffectivePerSecond() float64 {
	rate := r.PerSecond
	if rate <= 0 {
		rate = DefaultTMDBPerSecond
	}
	if r.SharedCredentials {
		rate /= 2
	}
	if rate < 0.1 {
		rate = 0.1
	}
	if rate > tmdbSoftCeiling {
		rate = tmdbSoftCeiling
	}
	return rate
}

// EffectiveConcurrency 返回光鸭分享检测的并发上限。
func (r GuangyaRate) EffectiveConcurrency() int {
	if r.Concurrency <= 0 {
		return DefaultGuangyaConcurrency
	}
	return r.Concurrency
}

// 官方建议的默认值，Default() 与「恢复默认」共用一份。
const (
	DefaultTMDBPerSecond      = 4    // TMDB：官方保护阈值约 40/s，默认只用 4/s
	DefaultTMDBBurst          = 4    // 允许的瞬时突发
	DefaultTelegramIntervalMS = 3100 // Telegram：≈19 条/分钟，低于群组 20 条/分钟
	DefaultGuangyaConcurrency = 2    // 光鸭：无官方配额，保守并发 2
)

type ServerConfig struct {
	Addr     string `yaml:"addr"`
	BasePath string `yaml:"base_path"`
	// SecureCookie 强制给会话 Cookie 加 Secure 标记。反代或纯 HTTP 场景可显式设为 false；
	// 未设置时（zero value）按请求 TLS / X-Forwarded-Proto 判定。
	SecureCookie *bool `yaml:"secure_cookie,omitempty"`
}

type StorageConfig struct {
	DSN       string `yaml:"dsn"`
	DataDir   string `yaml:"data_dir"`
	PosterDir string `yaml:"poster_dir"`
}

type TMDBConfig struct {
	APIKey      string  `yaml:"api_key"`
	Language    string  `yaml:"language"`
	ImageBase   string  `yaml:"image_base"`
	PosterSize  string  `yaml:"poster_size"`
	MinScore    float64 `yaml:"min_score"`
	CachePoster bool    `yaml:"cache_poster"`
}

// ChatID 支持 Telegram 的两种写法：数字 ID（-100...）与频道用户名（@xxx，@ 可省略）。
// 用字符串承载，避免 YAML 里数字与字符串类型不一致导致解析失败。
type ChatID string

// UnmarshalYAML 同时接受 `chat_id: -1001234567890` 与 `chat_id: "@channel"`。
func (c *ChatID) UnmarshalYAML(n *yaml.Node) error {
	*c = ChatID(strings.TrimSpace(n.Value))
	return nil
}

// Any 返回可直接交给 Telegram API 的值：纯数字给 int64，用户名给带 @ 的字符串，未设置返回 nil。
func (c ChatID) Any() any {
	s := strings.TrimSpace(string(c))
	if s == "" || s == "0" {
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if !strings.HasPrefix(s, "@") {
		s = "@" + s
	}
	return s
}

type TelegramConfig struct {
	Token     string `yaml:"token"`
	ChatID    ChatID `yaml:"chat_id"`
	EnableBot bool   `yaml:"enable_bot"`
	Silent    bool   `yaml:"silent"`
}

type NetdiskConfig struct {
	GuangyaHosts []string `yaml:"guangya_hosts"`
	UserAgent    string   `yaml:"user_agent"`
	Cookie       string   `yaml:"cookie"`
	Timeout      int      `yaml:"timeout"`
}

type PipelineConfig struct {
	Workers    int  `yaml:"workers"`
	MaxRetry   int  `yaml:"max_retry"`
	RetryDelay int  `yaml:"retry_delay"`
	AutoPush   bool `yaml:"auto_push"`
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{Addr: ":8080"},
		Storage: StorageConfig{
			DSN:       "data/guangya.db",
			DataDir:   "data",
			PosterDir: "data/posters",
		},
		TMDB: TMDBConfig{
			Language:    "zh-CN",
			ImageBase:   "https://image.tmdb.org/t/p",
			PosterSize:  "w500",
			MinScore:    0.6,
			CachePoster: true,
		},
		Telegram: TelegramConfig{EnableBot: true},
		Netdisk: NetdiskConfig{
			GuangyaHosts: []string{"guangya"},
			UserAgent:    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36",
			Timeout:      20,
		},
		Pipeline: PipelineConfig{
			Workers:    2,
			MaxRetry:   3,
			RetryDelay: 5,
			AutoPush:   true,
		},
		Proxy: ProxyConfig{Enable: false},
		SEO:   SEOConfig{SiteName: "光鸭 GO"},
		Rate: RateConfig{
			TMDB: TMDBRate{
				Enable:    true,
				PerSecond: DefaultTMDBPerSecond,
				Burst:     DefaultTMDBBurst,
			},
			Telegram: TelegramRate{
				Enable:     true,
				IntervalMS: DefaultTelegramIntervalMS,
			},
			Guangya: GuangyaRate{
				Enable:      true,
				Concurrency: DefaultGuangyaConcurrency,
			},
		},
	}
}

// Load 读取配置文件并应用环境变量覆盖：
// TMDB_API_KEY / TG_BOT_TOKEN / TG_CHAT_ID / GUANGYA_COOKIE
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			if err := yaml.Unmarshal(b, cfg); err != nil {
				return nil, err
			}
		}
	}
	if v := os.Getenv("TMDB_API_KEY"); v != "" {
		cfg.TMDB.APIKey = v
	}
	if v := os.Getenv("TG_BOT_TOKEN"); v != "" {
		cfg.Telegram.Token = v
	}
	if v := os.Getenv("GUANGYA_COOKIE"); v != "" {
		cfg.Netdisk.Cookie = v
	}
	if v := os.Getenv("PROXY_ADDR"); v != "" {
		cfg.Proxy.Enable = true
		cfg.Proxy.Addr = v
	}
	if v := os.Getenv("SEO_BASE_URL"); v != "" {
		cfg.SEO.BaseURL = strings.TrimRight(strings.TrimSpace(v), "/")
	}
	if v := os.Getenv("PROXY_USERNAME"); v != "" {
		cfg.Proxy.Username = v
		cfg.Proxy.Password = os.Getenv("PROXY_PASSWORD")
	}
	return cfg, nil
}

// Save 把当前配置写回文件，用于界面上保存密钥。
func Save(path string, cfg *Config) error {
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func (c *Config) NetdiskTimeout() time.Duration {
	if c.Netdisk.Timeout <= 0 {
		return 20 * time.Second
	}
	return time.Duration(c.Netdisk.Timeout) * time.Second
}
