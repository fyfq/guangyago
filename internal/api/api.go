package api

import (
	"html"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/notifier"
	"github.com/odin/guangya-go/internal/parser"
	"github.com/odin/guangya-go/internal/pipeline"
	"github.com/odin/guangya-go/internal/store"
)

// Server 提供链接管理的 HTTP 接口。
type Server struct {
	store *store.Store
	pipe  *pipeline.Pipeline
	cfg   *config.Config
	// configMu 保护 cfg 的并发读写，避免 settings/setup 与请求 handler 之间的数据竞争。
	configMu   sync.RWMutex
	configPath string
	// reload 在配置变更后重建刮削与推送组件
	reload func(*config.Config)
	// loginGuard 记录登录失败次数，防止暴力猜密码
	loginGuard *loginGuard
}

func New(s *store.Store) *Server {
	return &Server{store: s, loginGuard: newLoginGuard()}
}

// LockConfig 加写锁保护 cfg 的并发修改。
func (s *Server) LockConfig() { s.configMu.Lock() }

// UnlockConfig 释放写锁。
func (s *Server) UnlockConfig() { s.configMu.Unlock() }

// RLockConfig 加读锁保护 cfg 的并发读取。
func (s *Server) RLockConfig() { s.configMu.RLock() }

// RUnlockConfig 释放读锁。
func (s *Server) RUnlockConfig() { s.configMu.RUnlock() }

// WithRuntime 注入管道、配置与热更新回调。
func (s *Server) WithRuntime(p *pipeline.Pipeline, cfg *config.Config, path string, reload func(*config.Config)) *Server {
	s.pipe, s.cfg, s.configPath, s.reload = p, cfg, path, reload
	return s
}

// Register 挂载路由。
func (s *Server) Register(r *gin.Engine) {
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// SEO: robots.txt 和 sitemap.xml
	r.GET("/robots.txt", s.robotsTXT)
	r.GET("/sitemap.xml", s.sitemapXML)
	r.GET("/api/structured-data", s.structuredData)
	r.GET("/api/structured-data/:id", s.itemStructuredData)

	// 公开接口：健康检查、登录、资源门户、安装向导
	r.POST("/api/auth/login", s.login)
	r.GET("/api/setup/status", s.setupStatus)
	r.POST("/api/setup", s.setup)
	portal := r.Group("/api/portal")
	{
		portal.GET("/links", s.portalLinks)
		portal.GET("/stats", s.portalStats)
	}

	api := r.Group("/api", s.requireAuth())
	{
		api.GET("/auth/me", s.me)
		api.POST("/auth/logout", s.logout)
		api.POST("/auth/password", s.changePassword)

		api.GET("/links", s.listLinks)
		api.GET("/links/:id", s.getLink)
		api.GET("/links/:id/caption", s.caption)
		api.POST("/links/:id/tmdb", s.setTMDB)
		api.DELETE("/links/:id", s.deleteLink)
		api.POST("/links/text", s.importText)
		api.POST("/links/import", s.importAuto)
		api.GET("/links/export", s.exportLinks)
		api.POST("/links/batch-delete", s.batchDelete)
		api.GET("/stats", s.stats)
		api.POST("/scrape", s.scrape)
		api.POST("/push", s.push)
		api.GET("/tasks", s.taskStatus)
		api.POST("/tasks/cancel", s.cancelTasks)
		api.POST("/links/:id/scrape", s.scrapeOne)
		api.POST("/links/:id/push", s.pushOne)
		// TMDB 条目预览：补 ID 时先查询确认，再决定是否刮削
		api.GET("/tmdb/lookup", s.tmdbLookup)
		api.POST("/tmdb/lookup", s.tmdbLookup)

		// 密钥、代理与账号管理只对管理员开放，运营改不了这些配置
		admin := api.Group("", s.requireAdmin())
		{
			admin.GET("/settings", s.settings)
			admin.POST("/settings", s.updateSettings)
			admin.POST("/settings/test", s.testConfig)

			users := admin.Group("/users")
			users.GET("", s.listUsers)
			users.POST("", s.createUser)
			users.PATCH("/:id", s.updateUser)
			users.POST("/:id/password", s.resetUserPassword)
			users.DELETE("/:id", s.deleteUser)
		}
	}
}

// serverError 把内部错误写进日志，只向客户端返回通用提示，避免泄露实现细节。
func serverError(c *gin.Context, err error) {
	slog.Error("请求处理失败", "path", c.FullPath(), "err", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
}

// parseID 读取路径里的 :id，非法时直接回 400。
func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法 ID"})
		return 0, false
	}
	return id, true
}

// applyStatusFilter 解析 status 查询参数：支持单值、逗号分隔多值，
// 以及 scraped_set / pending_set / push_set 三个语义集合。
func applyStatusFilter(f *model.ListFilter, raw string) {
	switch raw = strings.TrimSpace(raw); raw {
	case "", "all":
	case "scraped_set":
		f.Statuses = model.ScrapedOnly
	case "pending_set":
		f.Statuses = model.PendingScrape
	case "push_set":
		f.Statuses = model.PushQueue
	default:
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				f.Statuses = append(f.Statuses, model.Status(p))
			}
		}
		if len(f.Statuses) == 1 {
			f.Status = f.Statuses[0]
			f.Statuses = nil
		}
	}
}

func (s *Server) listLinks(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	// 列表是分页接口，limit 上限由这里收口；导出与批量任务走各自的内部调用
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	f := model.ListFilter{
		Keyword: c.Query("q"),
		Limit:   limit,
		Offset:  offset,
	}
	applyStatusFilter(&f, c.Query("status"))
	links, total, err := s.store.List(f)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": total, "items": links})
}

func (s *Server) getLink(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	l, err := s.store.Get(id)
	if err != nil {
		serverError(c, err)
		return
	}
	if l == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "记录不存在"})
		return
	}
	c.JSON(http.StatusOK, l)
}

// caption 返回该记录推送到频道的卡片文案，供界面预览，保证与实际推送一致。
func (s *Server) caption(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	l, err := s.store.Get(id)
	if err != nil {
		serverError(c, err)
		return
	}
	if l == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "记录不存在"})
		return
	}
	htmlText := notifier.Caption(l)
	c.JSON(http.StatusOK, gin.H{"html": htmlText, "text": plainText(htmlText)})
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

// plainText 去掉 HTML 标签，得到纯文本版本。
func plainText(s string) string {
	return html.UnescapeString(tagRe.ReplaceAllString(strings.ReplaceAll(s, "<br>", "\n"), ""))
}

func (s *Server) deleteLink(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := s.store.Delete(id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

type importRequest struct {
	Text string `json:"text"`
	CSV  string `json:"csv"`
}

func (s *Server) importText(c *gin.Context) {
	var req importRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text 不能为空"})
		return
	}
	entries, errs := parser.ParseText(req.Text)
	c.JSON(http.StatusOK, s.Save(entries, errs))
}

// importAuto 同时支持 multipart 文件上传与直接提交文本/CSV 内容。
func (s *Server) importAuto(c *gin.Context) {
	// 限制请求体上限，避免误传大文件把内存吃满
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<20)
	if f, err := c.FormFile("file"); err == nil {
		fh, err := f.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "文件读取失败"})
			return
		}
		defer fh.Close()
		entries, errs := parser.ParseCSV(fh)
		c.JSON(http.StatusOK, s.Save(entries, errs))
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil || len(strings.TrimSpace(string(body))) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 file 或请求体"})
		return
	}
	content := string(body)
	if looksLikeCSV(content) {
		entries, errs := parser.ParseCSV(strings.NewReader(content))
		c.JSON(http.StatusOK, s.Save(entries, errs))
		return
	}
	entries, errs := parser.ParseText(content)
	c.JSON(http.StatusOK, s.Save(entries, errs))
}

func looksLikeCSV(s string) bool {
	first := s
	if i := strings.IndexByte(s, '\n'); i > 0 {
		first = s[:i]
	}
	return strings.Contains(first, "链接") || strings.Count(first, ",") >= 2
}

// ImportResult 汇总一次导入的结果，命令行与 HTTP 接口共用。
type ImportResult struct {
	Total    int               `json:"total"`
	Inserted int               `json:"inserted"`
	Skipped  int               `json:"skipped"`
	Failed   []parser.RowError `json:"failed"`
	IDs      []int64           `json:"ids"`
}

// Save 把解析结果写入数据库，跳过 share_id 重复的记录。
func (s *Server) Save(entries []parser.Entry, errs []parser.RowError) ImportResult {
	res := ImportResult{Total: len(entries) + len(errs), Failed: errs}
	if res.Failed == nil {
		res.Failed = []parser.RowError{}
	}
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
		}
		switch {
		case e.ShareStatus != "" && e.ShareStatus != "成功":
			l.Status = model.StatusReview
		default:
			l.Status = model.StatusPending
		}
		ok, err := s.store.Create(l)
		if err != nil {
			res.Failed = append(res.Failed, parser.RowError{Line: e.Line, Raw: e.URL, Err: err.Error()})
			continue
		}
		if !ok {
			res.Skipped++
			continue
		}
		res.Inserted++
		res.IDs = append(res.IDs, l.ID)
	}
	return res
}
