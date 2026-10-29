package api

import (
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/model"
)

// robotsTXT 返回搜索引擎爬虫指引
func (s *Server) robotsTXT(c *gin.Context) {
	baseURL := s.getBaseURL(c)
	content := fmt.Sprintf(`User-agent: *
Allow: /
Disallow: /admin
Disallow: /login
Disallow: /setup
Disallow: /api/

Sitemap: %s/sitemap.xml
`, baseURL)
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(content))
}

// sitemapXML 动态生成站点地图，包含门户首页和所有已推送资源
func (s *Server) sitemapXML(c *gin.Context) {
	baseURL := s.getBaseURL(c)

	// 获取所有已推送的资源
	items, _, err := s.store.ListPublic(model.PublicFilter{
		Sort:  "new",
		Limit: 5000, // sitemap 最多收录 5000 条
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成站点地图失败"})
		return
	}

	type URLItem struct {
		Loc        string  `xml:"loc"`
		LastMod    string  `xml:"lastmod,omitempty"`
		ChangeFreq string  `xml:"changefreq,omitempty"`
		Priority   float64 `xml:"priority,omitempty"`
	}

	urls := []URLItem{
		{
			Loc:        baseURL + "/",
			ChangeFreq: "daily",
			Priority:   1.0,
		},
	}

	for _, item := range items {
		loc := fmt.Sprintf("%s/?id=%d", baseURL, item.ID)
		lastMod := ""
		if item.SharedAt != "" {
			if t, err := time.Parse("2006-01-02 15:04:05", item.SharedAt); err == nil {
				lastMod = t.Format("2006-01-02")
			}
		}
		urls = append(urls, URLItem{
			Loc:        loc,
			LastMod:    lastMod,
			ChangeFreq: "monthly",
			Priority:   0.8,
		})
	}

	sitemap := struct {
		XMLName xml.Name `xml:"urlset"`
		Xmlns   string   `xml:"xmlns,attr"`
		URLs    []URLItem `xml:"url"`
	}{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  urls,
	}

	c.Header("Content-Type", "application/xml; charset=utf-8")
	c.Writer.WriteString(xml.Header)
	if err := xml.NewEncoder(c.Writer).Encode(sitemap); err != nil {
		slog.Error("编码 sitemap 失败", "err", err)
	}
}

// structuredData 返回站点的结构化数据，供前端嵌入 JSON-LD
func (s *Server) structuredData(c *gin.Context) {
	baseURL := s.getBaseURL(c)

	data := map[string]any{
		"@context": "https://schema.org",
		"@type":    "WebSite",
		"name":     "光鸭 GO",
		"url":      baseURL,
		"description": "光鸭 GO 资源库 - 网盘资源分享平台",
		"potentialAction": map[string]any{
			"@type":       "SearchAction",
			"target":      baseURL + "/?q={search_term_string}",
			"query-input": "required name=search_term_string",
		},
	}

	c.JSON(http.StatusOK, data)
}

// itemStructuredData 返回单个资源的结构化数据
func (s *Server) itemStructuredData(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	item, err := s.store.GetPublic(id)
	if err != nil {
		serverError(c, err)
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在"})
		return
	}

	baseURL := s.getBaseURL(c)
	data := buildMovieSchema(item, baseURL)
	c.JSON(http.StatusOK, data)
}

// getBaseURL 从请求中推断站点基础 URL
func (s *Server) getBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, c.Request.Host)
}

// buildMovieSchema 构建电影/剧集的 Schema.org 结构化数据
func buildMovieSchema(item *model.PublicLink, baseURL string) map[string]any {
	schemaType := "Movie"
	if item.MediaType == "tv" {
		schemaType = "TVSeries"
	}

	data := map[string]any{
		"@context":     "https://schema.org",
		"@type":        schemaType,
		"name":         item.Title,
		"url":          fmt.Sprintf("%s/?id=%d", baseURL, item.ID),
		"datePublished": item.SharedAt,
	}

	if item.OriginalTitle != "" && item.OriginalTitle != item.Title {
		data["alternateName"] = item.OriginalTitle
	}
	if item.Overview != "" {
		data["description"] = item.Overview
	}
	if item.PosterURL != "" {
		data["image"] = item.PosterURL
	}
	if item.Year > 0 {
		data["year"] = item.Year
	}
	if item.VoteAverage > 0 {
		data["aggregateRating"] = map[string]any{
			"@type":       "AggregateRating",
			"ratingValue": item.VoteAverage,
			"bestRating":  10,
			"worstRating": 0,
		}
	}
	if item.Runtime > 0 {
		data["duration"] = fmt.Sprintf("PT%dM", item.Runtime)
	}
	if item.Genres != "" {
		genres := strings.Split(item.Genres, ",")
		for i, g := range genres {
			genres[i] = strings.TrimSpace(g)
		}
		data["genre"] = genres
	}
	if item.TMDBID > 0 {
		data["sameAs"] = fmt.Sprintf("https://www.themoviedb.org/%s/%d", item.MediaType, item.TMDBID)
	}

	return data
}
