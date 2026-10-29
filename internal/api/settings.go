package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/netutil"
	"github.com/odin/guangya-go/internal/notifier"
	"github.com/odin/guangya-go/internal/scraper"
)

// settings 返回当前关键配置是否已就绪（不返回密钥明文）。
func (s *Server) settings(c *gin.Context) {
	cfg := s.cfg
	if cfg == nil {
		c.JSON(http.StatusOK, gin.H{})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tmdb_configured":     cfg.TMDB.APIKey != "",
		"telegram_configured": cfg.Telegram.Token != "",
		"chat_id":             string(cfg.Telegram.ChatID),
		"auto_push":           cfg.Pipeline.AutoPush,
		"workers":             cfg.Pipeline.Workers,
		"proxy": gin.H{
			"enable":       cfg.Proxy.Enable,
			"addr":         cfg.Proxy.Addr,
			"username":     cfg.Proxy.Username,
			"has_password": cfg.Proxy.Password != "",
		},
		// 限流参数：默认值即官方建议值，界面据此回显
		"rate_limit": gin.H{
			"tmdb": gin.H{
				"enable":             cfg.Rate.TMDB.Enable,
				"per_second":         cfg.Rate.TMDB.PerSecond,
				"burst":              cfg.Rate.TMDB.Burst,
				"shared_credentials": cfg.Rate.TMDB.SharedCredentials,
			},
			"telegram": gin.H{
				"enable":             cfg.Rate.Telegram.Enable,
				"interval_ms":        cfg.Rate.Telegram.IntervalMS,
				"channel_only":       cfg.Rate.Telegram.ChannelOnly,
				"shared_credentials": cfg.Rate.Telegram.SharedCredentials,
				"paid_broadcast":     cfg.Rate.Telegram.PaidBroadcast,
			},
			"guangya": gin.H{
				"enable":      cfg.Rate.Guangya.Enable,
				"concurrency": cfg.Rate.Guangya.Concurrency,
			},
		},
	})
}

type settingsRequest struct {
	TMDBKey    string `json:"tmdb_api_key"`
	TGToken    string `json:"tg_token"`
	ChatID     string `json:"chat_id"`
	AutoPush   *bool  `json:"auto_push"`
	SaveToFile bool   `json:"save_to_file"`
	Proxy      *struct {
		Enable   *bool  `json:"enable"`
		Addr     string `json:"addr"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"proxy"`
	RateLimit *struct {
		TMDB *struct {
			Enable            *bool    `json:"enable"`
			PerSecond         *float64 `json:"per_second"`
			Burst             *int     `json:"burst"`
			SharedCredentials *bool    `json:"shared_credentials"`
		} `json:"tmdb"`
		Telegram *struct {
			Enable            *bool `json:"enable"`
			IntervalMS        *int  `json:"interval_ms"`
			ChannelOnly       *bool `json:"channel_only"`
			SharedCredentials *bool `json:"shared_credentials"`
			PaidBroadcast     *bool `json:"paid_broadcast"`
		} `json:"telegram"`
		Guangya *struct {
			Enable      *bool `json:"enable"`
			Concurrency *int  `json:"concurrency"`
		} `json:"guangya"`
	} `json:"rate_limit"`
}

// updateSettings 更新运行期配置，可选写回配置文件。
func (s *Server) updateSettings(c *gin.Context) {
	var req settingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
		return
	}
	cfg := s.cfg
	if cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "配置未加载"})
		return
	}
	if req.TMDBKey != "" {
		cfg.TMDB.APIKey = req.TMDBKey
	}
	if req.TGToken != "" {
		cfg.Telegram.Token = req.TGToken
	}
	if req.ChatID != "" {
		cfg.Telegram.ChatID = config.ChatID(strings.TrimSpace(req.ChatID))
	}
	if req.AutoPush != nil {
		cfg.Pipeline.AutoPush = *req.AutoPush
	}
	if p := req.Proxy; p != nil {
		if p.Enable != nil {
			cfg.Proxy.Enable = *p.Enable
		}
		if p.Addr != "" {
			cfg.Proxy.Addr = p.Addr
		}
		if p.Username != "" {
			cfg.Proxy.Username = p.Username
		}
		if p.Password != "" {
			cfg.Proxy.Password = p.Password
		}
	}
	if r := req.RateLimit; r != nil {
		if t := r.TMDB; t != nil {
			if t.Enable != nil {
				cfg.Rate.TMDB.Enable = *t.Enable
			}
			if t.PerSecond != nil {
				cfg.Rate.TMDB.PerSecond = *t.PerSecond
			}
			if t.Burst != nil {
				cfg.Rate.TMDB.Burst = *t.Burst
			}
			if t.SharedCredentials != nil {
				cfg.Rate.TMDB.SharedCredentials = *t.SharedCredentials
			}
		}
		if g := r.Telegram; g != nil {
			if g.Enable != nil {
				cfg.Rate.Telegram.Enable = *g.Enable
			}
			if g.IntervalMS != nil {
				cfg.Rate.Telegram.IntervalMS = *g.IntervalMS
			}
			if g.ChannelOnly != nil {
				cfg.Rate.Telegram.ChannelOnly = *g.ChannelOnly
			}
			if g.SharedCredentials != nil {
				cfg.Rate.Telegram.SharedCredentials = *g.SharedCredentials
			}
			if g.PaidBroadcast != nil {
				cfg.Rate.Telegram.PaidBroadcast = *g.PaidBroadcast
			}
		}
		if y := r.Guangya; y != nil {
			if y.Enable != nil {
				cfg.Rate.Guangya.Enable = *y.Enable
			}
			if y.Concurrency != nil {
				cfg.Rate.Guangya.Concurrency = *y.Concurrency
			}
		}
	}
	if req.SaveToFile && s.configPath != "" {
		if err := config.Save(s.configPath, cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "写入配置文件失败: " + err.Error()})
			return
		}
	}
	if s.reload != nil {
		s.reload(cfg)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type testRequest struct {
	Target    string `json:"target"`
	TMDBKey   string `json:"tmdb_api_key"`
	TGToken   string `json:"tg_token"`
	ChatID    string `json:"chat_id"`
	ProxyAddr string `json:"proxy_addr"`
	ProxyUser string `json:"proxy_username"`
	ProxyPass string `json:"proxy_password"`
	ProxyOn   *bool  `json:"proxy_enable"`
}

// testConfig 验证密钥或代理是否配置正确，不写回任何配置。
func (s *Server) testConfig(c *gin.Context) {
	var req testRequest
	_ = c.ShouldBindJSON(&req)
	cfg := s.cfg
	if cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "配置未加载"})
		return
	}

	proxy := cfg.Proxy
	if req.ProxyAddr != "" {
		proxy.Addr = req.ProxyAddr
	}
	if req.ProxyUser != "" {
		proxy.Username = req.ProxyUser
	}
	if req.ProxyPass != "" {
		proxy.Password = req.ProxyPass
	}
	if req.ProxyOn != nil {
		proxy.Enable = *req.ProxyOn
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	switch req.Target {
	case "tmdb":
		key := req.TMDBKey
		if key == "" {
			key = cfg.TMDB.APIKey
		}
		c.JSON(http.StatusOK, scraper.TestConnection(ctx, key, &proxy))
	case "telegram":
		tg := cfg.Telegram
		if req.TGToken != "" {
			tg.Token = req.TGToken
		}
		if req.ChatID != "" {
			tg.ChatID = config.ChatID(strings.TrimSpace(req.ChatID))
		}
		c.JSON(http.StatusOK, notifier.TestTelegram(ctx, tg, &proxy))
	case "proxy":
		c.JSON(http.StatusOK, netutil.TestProxy(ctx, proxy.Addr, proxy.Username, proxy.Password, proxy.Enable))
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "未知测试目标: " + req.Target})
	}
}
