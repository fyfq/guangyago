package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/gygo/internal/auth"
	"github.com/odin/gygo/internal/model"
	"github.com/odin/gygo/internal/store"
)

const (
	// visitorCookieName 是门户访客标识：只用于按天去重 UV，不含任何账号信息。
	visitorCookieName = "gy_vid"
	// visitorCookieMaxAge 让老访客在一年内保持同一个标识。
	visitorCookieMaxAge = 365 * 24 * 60 * 60
)

// visitorIDRe 限定访客标识字符集，防止客户端塞入超长或异常值。
var visitorIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// statsEnabled 读取运行期配置；配置未加载时按开启处理。
func (s *Server) statsEnabled() bool {
	s.configMu.RLock()
	enabled := s.cfg.StatsEnabled()
	s.configMu.RUnlock()
	return enabled
}

// visitorID 返回当前访客标识：优先读 Cookie，没有就生成一个并下发。
// 不落 IP/UA，Cookie 丢失时自然按新访客计。
func (s *Server) visitorID(c *gin.Context) string {
	if v, err := c.Cookie(visitorCookieName); err == nil && visitorIDRe.MatchString(v) {
		return v
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		slog.Error("生成访客标识失败", "err", err)
		return ""
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(visitorCookieName, id, visitorCookieMaxAge, "/", "", s.secureCookie(c), true)
	return id
}

// recordPortalVisit 统计门户整页访问（PV/UV）。只在首页路径记录，
// 后台、登录页与未知路径都不计入。
func (s *Server) recordPortalVisit(c *gin.Context) {
	if !s.statsEnabled() || strings.TrimRight(c.Request.URL.Path, "/") != "" {
		return
	}
	visitor := s.visitorID(c)
	if visitor == "" {
		return
	}
	if err := s.store.RecordVisit(store.DayKey(time.Now()), auth.HashToken(visitor)); err != nil {
		slog.Warn("门户访问统计写入失败", "err", err)
	}
}

// recordPortalAction 统计一次资源互动（浏览/复制/打开）；写入失败只记日志，不影响响应。
func (s *Server) recordPortalAction(linkID int64, action string) {
	if !s.statsEnabled() {
		return
	}
	if err := s.store.RecordAction(linkID, store.DayKey(time.Now()), action); err != nil {
		slog.Warn("资源互动统计写入失败", "link_id", linkID, "action", action, "err", err)
	}
}

// portalTraffic 返回后台统计页数据：只接受 7/30/90 天窗口，其余退回 30 天。
func (s *Server) portalTraffic(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days != 7 && days != 30 && days != 90 {
		days = 30
	}
	data, err := s.store.PortalTraffic(days, time.Now())
	if err != nil {
		serverError(c, err)
		return
	}
	for i := range data.Top {
		t := &data.Top[i]
		t.PosterURL = s.publicPoster(&model.PublicLink{
			MediaType: t.MediaType,
			TMDBID:    t.TMDBID,
			PosterURL: t.PosterURL,
		})
	}
	c.JSON(http.StatusOK, data)
}

// portalTrafficLink 返回后台统计展开用的资源详情，Links 里带各自的累计互动次数。
func (s *Server) portalTrafficLink(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	item, err := s.store.GetPublic(id)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在"})
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	ids := make([]int64, 0, len(item.Links))
	for _, l := range item.Links {
		ids = append(ids, l.ID)
	}
	stats, err := s.store.ShareActions(ids)
	if err != nil {
		serverError(c, err)
		return
	}
	for i := range item.Links {
		st := stats[item.Links[i].ID]
		item.Links[i].Browses = st.Browses
		item.Links[i].Copies = st.Copies
		item.Links[i].Opens = st.Opens
	}
	item.PosterURL = s.publicPoster(item)
	c.JSON(http.StatusOK, item)
}
