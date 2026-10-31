package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/odin/gygo/internal/model"
)

// posterNameRe 只放行刮削缓存落盘的海报名（<media_type>-<tmdb_id>.jpg）。
// 白名单同时挡住路径穿越和对海报目录以外文件的探测。
var posterNameRe = regexp.MustCompile(`^(movie|tv)-\d{1,12}\.jpg$`)

// posterMaxAgeSeconds 是海报的浏览器缓存时长；同一 TMDB ID 的海报很少变，
// 真换了海报也会因为文件名不变而最多滞后一天。
const posterMaxAgeSeconds = 86400

// posterFileName 按记录拼出缓存文件名，与 scraper.SavePoster 的命名保持一致。
func posterFileName(mediaType model.MediaType, tmdbID int64) string {
	if tmdbID <= 0 || (mediaType != model.MediaMovie && mediaType != model.MediaTV) {
		return ""
	}
	return fmt.Sprintf("%s-%d.jpg", mediaType, tmdbID)
}

// posterDir 返回海报缓存目录，未配置时为空串。
func (s *Server) posterDir() string {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	if s.cfg == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg.Storage.PosterDir)
}

// servePosters 返回是否启用同源海报（storage.serve_posters，默认开启）。
func (s *Server) servePosters() bool {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.cfg != nil && s.cfg.Storage.ServePosters
}

// cachedPosterHref 返回缓存海报的同源地址；文件不存在时返回空串。
func (s *Server) cachedPosterHref(name string) string {
	if !s.servePosters() {
		return ""
	}
	dir := s.posterDir()
	if dir == "" || name == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		return ""
	}
	return "/posters/" + name
}

// publicPoster 给门户响应挑海报地址：本地有缓存就用同源地址，否则退回刮削时
// 记下的 TMDB 远端地址。访客网络打不开 image.tmdb.org 时，同源地址仍能显示。
func (s *Server) publicPoster(p *model.PublicLink) string {
	if p == nil {
		return ""
	}
	if href := s.cachedPosterHref(posterFileName(p.MediaType, p.TMDBID)); href != "" {
		return href
	}
	return strings.TrimSpace(p.PosterURL)
}

// absolutePoster 把同源路径补成绝对地址，供 OG 图片与 JSON-LD 的 image 字段使用。
func absolutePoster(baseURL, poster string) string {
	if strings.HasPrefix(poster, "/") {
		return strings.TrimRight(baseURL, "/") + poster
	}
	return poster
}

// posterFile 提供刮削缓存的海报原图。文件名先过白名单正则，
// 因此路径只会落在海报目录内的 <media>-<id>.jpg。
func (s *Server) posterFile(c *gin.Context) {
	name := c.Param("name")
	if !posterNameRe.MatchString(name) || !s.servePosters() {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}
	dir := s.posterDir()
	if dir == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
			return
		}
		serverError(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age="+strconv.Itoa(posterMaxAgeSeconds))
	c.Data(http.StatusOK, "image/jpeg", body)
}
