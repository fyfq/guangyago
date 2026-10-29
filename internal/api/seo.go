package api

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
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
		XMLName xml.Name  `xml:"urlset"`
		Xmlns   string    `xml:"xmlns,attr"`
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
		"@context":    "https://schema.org",
		"@type":       "WebSite",
		"name":        s.siteName(),
		"url":         baseURL,
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

// getBaseURL 从请求中推断站点基础 URL；配置里固定了 seo.base_url 时优先使用它。
func (s *Server) getBaseURL(c *gin.Context) string {
	s.configMu.RLock()
	fixed := ""
	if s.cfg != nil {
		fixed = strings.TrimSpace(s.cfg.SEO.BaseURL)
	}
	s.configMu.RUnlock()
	if fixed != "" {
		return strings.TrimRight(fixed, "/")
	}
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, c.Request.Host)
}

// siteName 返回配置里的站点名，未设置则用默认值。
func (s *Server) siteName() string {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	if s.cfg != nil && strings.TrimSpace(s.cfg.SEO.SiteName) != "" {
		return s.cfg.SEO.SiteName
	}
	return defaultSiteName
}

const defaultSiteName = "光鸭 GO"

// googleMetaTag 返回需要注入 index.html 原始 HTML 的验证标签；未配置 token 时返回空串。
// Google 的「HTML 标签」验证要求 meta 出现在首屏 HTML 源码里，前端用 JS 补挂是验不过的。
func (s *Server) googleMetaTag() string {
	s.configMu.RLock()
	token := ""
	if s.cfg != nil {
		token = strings.TrimSpace(s.cfg.SEO.GoogleVerification)
	}
	s.configMu.RUnlock()
	if !googleTokenRe.MatchString(token) {
		return ""
	}
	return `<meta name="google-site-verification" content="` + token + `" />`
}

// googleTokenRe 只允许 Search Console token 的合法字符，避免把任意 HTML 注入页面。
var googleTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{10,120}$`)

// headTags 汇总需要注入首页原始 HTML 的标签。
// 走原始 HTML 而不是前端脚本，是因为 CSP 禁内联脚本，爬虫也读不到 JS 补挂的标签。
func (s *Server) headTags(c *gin.Context) string {
	tags := s.googleMetaTag()
	if canonical := s.canonicalTag(c); canonical != "" {
		tags += "\n    " + canonical
	}
	return tags
}

// injectHeadTags 把标签插进 index.html 的 </head> 之前，供 SEO 验证与站点级元信息使用。
func injectHeadTags(indexHTML, tags string) string {
	if tags == "" {
		return indexHTML
	}
	idx := strings.Index(indexHTML, "</head>")
	if idx < 0 {
		return indexHTML
	}
	return indexHTML[:idx] + "    " + tags + "\n  " + indexHTML[idx:]
}

// WriteIndex 返回托管的前端首页，并把验证标签注入原始 HTML。
// 走这里而不是直接透传静态文件，是因为爬虫读不到前端 JS 补挂的 meta 标签。
func (s *Server) WriteIndex(c *gin.Context, fsys fs.FS) {
	body, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "首页不可用"})
		return
	}
	tags := s.headTags(c)
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(injectHeadTags(string(body), tags)))
}

// canonicalTag 按当前请求的完整 URL 生成 <link rel="canonical">，
// 对爬虫做同页归一，防止 ?page=2 之类的查询串被当成重复内容。
// URL 只从服务端已知的 baseURL + 请求路径拼出，且做了字符过滤，避免把任意内容注入 head。
func (s *Server) canonicalTag(c *gin.Context) string {
	// robots.txt 里已 Disallow 的后台/引导页不需要规范链接
	switch p := c.Request.URL.Path; {
	case p == "/admin" || strings.HasPrefix(p, "/admin/"), p == "/login", p == "/setup":
		return ""
	}
	base := s.getBaseURL(c)
	p := sanitizeCanonicalPath(c.Request.URL.Path)
	q := sanitizeCanonicalQuery(c.Request.URL.Query())
	return `<link rel="canonical" href="` + base + p + q + `" />`
}

// sanitizeCanonicalPath 只保留 / 与 URL 安全字符；其余替换掉，防止尖括号或空格破坏属性。
func sanitizeCanonicalPath(p string) string {
	if p == "" {
		return "/"
	}
	var b strings.Builder
	for _, r := range p {
		switch {
		case r == '/', r == '-':
			b.WriteRune(r)
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '_' || r == '.' || r == '~':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" || out[0] != '/' {
		out = "/" + out
	}
	return out
}

// sanitizeCanonicalQuery 保留常见 SEO 参数（id/q/page/sort），其余丢弃。
func sanitizeCanonicalQuery(v map[string][]string) string {
	if len(v) == 0 {
		return ""
	}
	allowed := map[string]bool{"id": true, "q": true, "page": true, "sort": true}
	parts := make([]string, 0, len(allowed))
	for _, key := range []string{"id", "q", "page", "sort"} {
		if !allowed[key] {
			continue
		}
		list := v[key]
		if len(list) == 0 || list[0] == "" {
			continue
		}
		safe := make([]rune, 0, len(list[0]))
		for _, r := range list[0] {
			switch {
			case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
				safe = append(safe, r)
			case r == '-' || r == '_' || r == '.' || r == '~':
				safe = append(safe, r)
			}
		}
		if len(safe) == 0 {
			continue
		}
		parts = append(parts, key+"="+string(safe))
	}
	if len(parts) == 0 {
		return ""
	}
	return "?" + strings.Join(parts, "&")
}

// buildMovieSchema 构建电影/剧集的 Schema.org 结构化数据
func buildMovieSchema(item *model.PublicLink, baseURL string) map[string]any {
	schemaType := "Movie"
	if item.MediaType == "tv" {
		schemaType = "TVSeries"
	}

	data := map[string]any{
		"@context":      "https://schema.org",
		"@type":         schemaType,
		"name":          item.Title,
		"url":           fmt.Sprintf("%s/?id=%d", baseURL, item.ID),
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
