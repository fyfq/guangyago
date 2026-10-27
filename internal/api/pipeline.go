package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/pipeline"
	"github.com/odin/guangya-go/internal/scraper"
)

// Server 通过组合获得管道能力。
func (s *Server) WithPipeline(p *pipeline.Pipeline) *Server {
	s.pipe = p
	return s
}

func (s *Server) scrape(c *gin.Context) {
	if s.pipe == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "刮削服务未启动"})
		return
	}
	var req struct {
		IDs      []int64 `json:"ids"`
		All      bool    `json:"all"`
		Statuses []model.Status
		Raw      []string `json:"statuses"`
	}
	_ = c.ShouldBindJSON(&req)
	// 默认只处理待刮削（pending）；显式传 statuses 时可重试 failed / unmatched 等
	statuses := req.Statuses
	for _, r := range req.Raw {
		if r = strings.TrimSpace(r); r != "" {
			statuses = append(statuses, model.Status(r))
		}
	}
	ids := req.IDs
	if len(ids) == 0 {
		if req.All || len(statuses) > 0 {
			if len(statuses) == 0 {
				statuses = []model.Status{model.StatusPending}
			}
			links, _, err := s.store.List(model.ListFilter{Statuses: statuses, Limit: 10000})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			for _, l := range links {
				ids = append(ids, l.ID)
			}
		}
	}
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有符合状态的记录"})
		return
	}
	n := s.pipe.Enqueue("scrape", ids)
	c.JSON(http.StatusOK, gin.H{"queued": n})
}

// tmdbLookup 查询 TMDB 条目预览，供「补 ID」时先核对再刮削。
// 支持两种模式：给 tmdb_id 精确查；给 query 按片名搜出候选列表。
// 该接口只读 TMDB，不写任何库表。
func (s *Server) tmdbLookup(c *gin.Context) {
	var req struct {
		TMDBID    int64  `json:"tmdb_id" form:"tmdb_id"`
		MediaType string `json:"media_type" form:"media_type"`
		Query     string `json:"query" form:"query"`
		Year      int    `json:"year" form:"year"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.TMDBID == 0 {
		if v := c.Query("tmdb_id"); v != "" {
			req.TMDBID, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	if req.Query == "" {
		req.Query = c.Query("query")
	}
	if req.MediaType == "" {
		req.MediaType = c.Query("media_type")
	}

	cfg := s.cfg
	if cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "配置未加载"})
		return
	}
	client, err := scraper.New(cfg.TMDB, cfg.Storage.PosterDir, &cfg.Proxy, &cfg.Rate.TMDB)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if client == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "还没有配置 TMDB API Key，无法查询"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// 精确 ID 查询
	if req.TMDBID > 0 {
		one, err := client.LookupByID(ctx, req.TMDBID, req.MediaType)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"mode": "id", "item": one, "items": []*scraper.Candidate{one}})
		return
	}

	// 片名搜索
	if strings.TrimSpace(req.Query) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要提供 tmdb_id 或 query"})
		return
	}
	items, err := client.LookupByTitle(ctx, strings.TrimSpace(req.Query), req.Year, 8)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"mode": "search", "items": items})
}

// setTMDB 手动指定 TMDB ID 与类型，重置为待刮削并立即入队。
func (s *Server) setTMDB(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法 ID"})
		return
	}
	var req struct {
		TMDBID    int64  `json:"tmdb_id"`
		MediaType string `json:"media_type"`
		Scrape    bool   `json:"scrape"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.TMDBID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要有效的 tmdb_id"})
		return
	}
	mt := model.MediaType(req.MediaType)
	if mt != model.MediaTV {
		mt = model.MediaMovie
	}
	if err := s.store.SetTMDBID(id, req.TMDBID, mt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if req.Scrape && s.pipe != nil {
		s.pipe.Enqueue("scrape", []int64{id})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) push(c *gin.Context) {
	if s.pipe == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "推送服务未启动"})
		return
	}
	var req struct {
		IDs     []int64  `json:"ids"`
		Scraped bool     `json:"scraped"`
		Raw     []string `json:"statuses"`
	}
	_ = c.ShouldBindJSON(&req)
	// 默认推送已刮削待推送的记录；显式传 statuses 时可重推 push_failed
	statuses := []model.Status{}
	for _, r := range req.Raw {
		if r = strings.TrimSpace(r); r != "" {
			statuses = append(statuses, model.Status(r))
		}
	}
	ids := req.IDs
	if len(ids) == 0 {
		if req.Scraped || len(statuses) > 0 {
			if len(statuses) == 0 {
				statuses = []model.Status{model.StatusScraped}
			}
			links, _, err := s.store.List(model.ListFilter{Statuses: statuses, Limit: 10000})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			for _, l := range links {
				ids = append(ids, l.ID)
			}
		}
	}
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有待推送的记录"})
		return
	}
	n := s.pipe.Enqueue("push", ids)
	c.JSON(http.StatusOK, gin.H{"queued": n})
}

func (s *Server) taskStatus(c *gin.Context) {
	if s.pipe == nil {
		c.JSON(http.StatusOK, gin.H{"running": false})
		return
	}
	c.JSON(http.StatusOK, s.pipe.Status())
}

func (s *Server) scrapeOne(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法 ID"})
		return
	}
	s.pipe.Enqueue("scrape", []int64{id})
	c.JSON(http.StatusOK, gin.H{"queued": 1})
}

func (s *Server) pushOne(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法 ID"})
		return
	}
	s.pipe.Enqueue("push", []int64{id})
	c.JSON(http.StatusOK, gin.H{"queued": 1})
}
