package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/gygo/internal/model"
	"github.com/odin/gygo/internal/pipeline"
	"github.com/odin/gygo/internal/scraper"
)

// pickIDs 解析批量请求：显式 ids 优先；否则按状态（缺省时用 fallback）取库里的记录。
func (s *Server) pickIDs(ids []int64, all bool, raw []string, fallback model.Status) ([]int64, error) {
	if len(ids) > 0 {
		return ids, nil
	}
	statuses := make([]model.Status, 0, len(raw))
	for _, r := range raw {
		if r = strings.TrimSpace(r); r != "" {
			statuses = append(statuses, model.Status(r))
		}
	}
	if !all && len(statuses) == 0 {
		return nil, nil
	}
	if len(statuses) == 0 {
		statuses = []model.Status{fallback}
	}
	return s.store.IDsByStatuses(statuses)
}

func (s *Server) scrape(c *gin.Context) {
	if s.pipe == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "刮削服务未启动"})
		return
	}
	var req struct {
		IDs      []int64  `json:"ids"`
		All      bool     `json:"all"`
		Statuses []string `json:"statuses"`
	}
	_ = c.ShouldBindJSON(&req)
	// 默认只处理待刮削（pending）；显式传 statuses 时可重试 failed / unmatched 等
	ids, err := s.pickIDs(req.IDs, req.All, req.Statuses, model.StatusPending)
	if err != nil {
		serverError(c, err)
		return
	}
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有符合状态的记录"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"queued": s.pipe.Enqueue("scrape", ids)})
}

// tmdbLookup 查询 TMDB 条目预览，供「补 ID」时先核对再刮削。
// 支持两种模式：给 tmdb_id 精确查；给 query 按片名搜出候选列表。
// 该接口只读 TMDB，不写任何库表。
func (s *Server) tmdbLookup(c *gin.Context) {
	var req struct {
		TMDBID    int64  `json:"tmdb_id"`
		MediaType string `json:"media_type"`
		Query     string `json:"query"`
		Year      int    `json:"year"`
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

	// 复用管道里的客户端：同一份限流器与连接池，避免预览请求绕开限流
	if s.pipe == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "刮削服务未启动"})
		return
	}
	client := s.pipe.Scraper()
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
			serverError(c, err)
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
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"mode": "search", "items": items})
}

// setTMDB 手动指定 TMDB ID 与类型，重置为待刮削并立即入队。
func (s *Server) setTMDB(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
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
		serverError(c, err)
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
	ids, err := s.pickIDs(req.IDs, req.Scraped, req.Raw, model.StatusScraped)
	if err != nil {
		serverError(c, err)
		return
	}
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有待推送的记录"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"queued": s.pipe.Enqueue("push", ids)})
}

func (s *Server) taskStatus(c *gin.Context) {
	if s.pipe == nil {
		c.JSON(http.StatusOK, pipeline.Progress{})
		return
	}
	// 刮削与推送各自有独立进度，带 kind 时只返回那一类
	if kind := strings.TrimSpace(c.Query("kind")); kind != "" {
		c.JSON(http.StatusOK, s.pipe.StatusOf(kind))
		return
	}
	c.JSON(http.StatusOK, s.pipe.Status())
}

// cancelTasks 停止排队中的任务，kind 留空表示刮削和推送都取消。
func (s *Server) cancelTasks(c *gin.Context) {
	if s.pipe == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "任务服务未启动"})
		return
	}
	var req struct {
		Kind string `json:"kind"`
	}
	_ = c.ShouldBindJSON(&req)
	kind := strings.TrimSpace(req.Kind)
	n := s.pipe.Cancel(kind)
	c.JSON(http.StatusOK, gin.H{"canceled": n, "kind": kind})
}

// enqueueOne 把单条记录加入指定类型的队列。
func (s *Server) enqueueOne(c *gin.Context, kind string) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if s.pipe == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "任务服务未启动"})
		return
	}
	s.pipe.Enqueue(kind, []int64{id})
	c.JSON(http.StatusOK, gin.H{"queued": 1})
}

func (s *Server) scrapeOne(c *gin.Context) { s.enqueueOne(c, "scrape") }

func (s *Server) pushOne(c *gin.Context) { s.enqueueOne(c, "push") }
