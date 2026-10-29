package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/api"
	"github.com/odin/guangya-go/internal/auth"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/notifier"
	"github.com/odin/guangya-go/internal/parser"
	"github.com/odin/guangya-go/internal/pipeline"
	"github.com/odin/guangya-go/internal/scraper"
	"github.com/odin/guangya-go/internal/store"
	"github.com/odin/guangya-go/internal/web"
)

func main() {
	var (
		configPath = flag.String("config", "config.yaml", "配置文件路径")
		addr       = flag.String("addr", "", "覆盖监听地址，如 :8080")
		importPath = flag.String("import", "", "导入 CSV 或分享文本后退出")
		noBot      = flag.Bool("no-bot", false, "不启动 Telegram Bot 接收")
		verbose    = flag.Bool("v", false, "输出调试日志")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	if *addr != "" {
		cfg.Server.Addr = *addr
	}

	st, err := store.Open(cfg.Storage.DSN)
	if err != nil {
		slog.Error("数据库打开失败", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if *importPath != "" {
		if err := runImport(st, *importPath); err != nil {
			slog.Error("导入失败", "err", err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := ensureAdmin(st); err != nil {
		slog.Error("初始化管理员失败", "err", err)
	}

	// 运行期可替换的组件（改密钥或代理后重建）
	var (
		pipe       *pipeline.Pipeline
		tgNotifier *notifier.Notifier
		botStop    context.CancelFunc
	)

	// startBot 以 long-polling 启动 Bot；配置变更时先停掉旧实例再重建。
	startBot := func() {
		if botStop != nil {
			botStop()
			botStop = nil
		}
		if !cfg.Telegram.EnableBot || *noBot || tgNotifier == nil {
			return
		}
		bctx, cancel := context.WithCancel(ctx)
		botStop = cancel
		go func() {
			slog.Info("Telegram Bot 已启动，私聊发送链接即可录入")
			tgNotifier.RunBot(bctx, func(text string) string {
				entries, errs := parser.ParseText(text)
				if len(entries) == 0 {
					if len(errs) > 0 {
						return "没识别到光鸭链接，发我分享内容就行。"
					}
					return ""
				}
				res := api.New(st).Save(entries, errs)
				if len(res.IDs) > 0 && cfg.Pipeline.AutoPush {
					pipe.Enqueue("scrape", res.IDs)
				}
				return fmt.Sprintf("已录入 %d 条，重复 %d 条", res.Inserted, res.Skipped)
			})
		}()
	}

	build := func(c *config.Config) {
		sc, err := scraper.New(c.TMDB, c.Storage.PosterDir, &c.Proxy, &c.Rate.TMDB)
		if err != nil {
			slog.Error("TMDB 客户端初始化失败", "err", err)
		} else if sc == nil {
			slog.Warn("未配置 TMDB API Key，刮削功能不可用")
		}
		nt, err := notifier.New(c.Telegram, &c.Proxy, &c.Rate.Telegram)
		if err != nil {
			slog.Warn("Telegram 初始化失败", "err", err)
		} else if nt == nil {
			slog.Warn("未配置 Telegram Bot Token，推送功能不可用")
		}
		tgNotifier = nt
		if pipe == nil {
			pipe = pipeline.New(st, sc, nt)
			pipe.Start(c.Pipeline.Workers)
		} else {
			pipe.UpdateComponents(sc, nt)
		}
		pipe.SetGuangyaLimit(c.Rate.Guangya.EffectiveConcurrency())
		startBot()
	}
	build(cfg)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// 不信任任何代理头，ClientIP 直接取 TCP 地址，
	// 否则客户端可伪造 X-Forwarded-For 绕过登录失败限流。
	if err := r.SetTrustedProxies(nil); err != nil {
		slog.Warn("设置可信代理失败", "err", err)
	}
	r.Use(gin.Logger(), gin.Recovery(), securityHeaders(), limitBody())
	api.New(st).WithRuntime(pipe, cfg, *configPath, func(c *config.Config) {
		build(c)
		slog.Info("配置已更新并重新加载")
	}).Register(r)

	// 前端构建产物托管：非 /api 路径一律回落到 index.html
	if fsys := web.FS(); fsys != nil {
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
				return
			}
			if f, err := fsys.Open(strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")); err == nil {
				_ = f.Close()
				c.FileFromFS(c.Request.URL.Path, fsys)
				return
			}
			// 找不到对应静态文件时回落到首页，交给前端路由
			c.FileFromFS("/", fsys)
		})
	}

	srv := &http.Server{Addr: cfg.Server.Addr, Handler: r}
	go func() {
		slog.Info("服务已启动", "addr", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("服务异常退出", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("关闭服务时出错", "err", err)
	}
	slog.Info("已退出")
}

// securityHeaders 补齐基础安全响应头，并禁止缓存接口响应。
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self'; font-src 'self' data:; object-src 'none'; "+
				"base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		c.Next()
	}
}

const maxJSONBody = 1 << 20

// limitBody 给普通接口设请求体上限；导入接口在 handler 里另有 32MB 限制。
func limitBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil && c.Request.URL.Path != "/api/links/import" {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxJSONBody)
		}
		c.Next()
	}
}

// ensureAdmin 在用户表为空时创建初始管理员。
// 默认密码仅用于首次启动，登录后台后应立即修改。
func ensureAdmin(st *store.Store) error {
	n, err := st.CountUsers()
	if err != nil || n > 0 {
		return err
	}
	username := strings.TrimSpace(os.Getenv("ADMIN_USERNAME"))
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("ADMIN_PASSWORD")
	useDefault := password == ""
	if useDefault {
		password = "admin123"
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	u := &model.User{
		Username:    username,
		DisplayName: "管理员",
		Role:        model.RoleAdmin,
		Enabled:     true,
	}
	if err := st.CreateUser(u, hash); err != nil {
		return err
	}
	if useDefault {
		slog.Warn("已创建初始管理员，请登录后立即修改密码", "username", username, "password", password)
	} else {
		slog.Info("已创建初始管理员", "username", username)
	}
	return nil
}

// runImport 解析 CSV 或分享文本并落库，打印统计。
func runImport(st *store.Store, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	var entries []parser.Entry
	var errs []parser.RowError
	if strings.Contains(content, "链接") && strings.Count(strings.SplitN(content, "\n", 2)[0], ",") >= 2 {
		entries, errs = parser.ParseCSV(strings.NewReader(content))
	} else {
		entries, errs = parser.ParseText(content)
	}

	res := api.New(st).Save(entries, errs)

	fmt.Printf("解析 %d 条：新增 %d，重复跳过 %d，失败 %d\n", len(entries)+len(errs), res.Inserted, res.Skipped, len(res.Failed))
	for _, e := range res.Failed {
		if e.Line > 0 {
			fmt.Printf("  第 %d 行失败：%s\n", e.Line, e.Err)
		} else {
			fmt.Printf("  失败：%s\n", e.Err)
		}
	}
	return nil
}
