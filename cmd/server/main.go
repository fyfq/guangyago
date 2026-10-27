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
				added, dup := 0, 0
				for _, e := range entries {
					l := &model.Link{
						ShareID: e.ShareID, URL: e.URL, Code: e.Code, Source: e.Source,
						RawTitle: e.RawTitle, Title: e.Title, Year: e.Year, TMDBID: e.TMDBID,
						Status: model.StatusPending,
					}
					ok, err := st.Create(l)
					if err != nil {
						continue
					}
					if ok {
						added++
						if cfg.Pipeline.AutoPush {
							pipe.Enqueue("scrape", []int64{l.ID})
						}
					} else {
						dup++
					}
				}
				return fmt.Sprintf("已录入 %d 条，重复 %d 条", added, dup)
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
	r.Use(gin.Logger(), gin.Recovery())
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

	inserted, skipped := 0, 0
	for _, e := range entries {
		l := &model.Link{
			ShareID:     e.ShareID,
			URL:         e.URL,
			Code:        e.Code,
			Source:      e.Source,
			RawTitle:    e.RawTitle,
			Title:       e.Title,
			Year:        e.Year,
			TMDBID:      e.TMDBID,
			SharedAt:    e.SharedAt,
			ShareStatus: e.ShareStatus,
			Status:      model.StatusPending,
		}
		if e.ShareStatus != "" && e.ShareStatus != "成功" {
			l.Status = model.StatusReview
		}
		ok, err := st.Create(l)
		if err != nil {
			errs = append(errs, parser.RowError{Line: e.Line, Raw: e.URL, Err: err.Error()})
			continue
		}
		if ok {
			inserted++
		} else {
			skipped++
		}
	}

	fmt.Printf("解析 %d 条：新增 %d，重复跳过 %d，失败 %d\n", len(entries)+len(errs), inserted, skipped, len(errs))
	for _, e := range errs {
		if e.Line > 0 {
			fmt.Printf("  第 %d 行失败：%s\n", e.Line, e.Err)
		} else {
			fmt.Printf("  失败：%s\n", e.Err)
		}
	}
	return nil
}
