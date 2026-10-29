package api

import (
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
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

// sitemapXML 动态生成站点地图，包含门户首页和所有已收录资源
func (s *Server) sitemapXML(c *gin.Context) {
	baseURL := s.getBaseURL(c)

	// 获取所有已收录的资源
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
		urls = append(urls, URLItem{
			Loc:        fmt.Sprintf("%s/?id=%d", baseURL, item.ID),
			LastMod:    schemaDate(item.SharedAt),
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
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在"})
		return
	}
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
// ?id=<n> 时输出该资源自己的标题/描述/OG/JSON-LD，否则退回站点级。
func (s *Server) headTags(c *gin.Context) string {
	item := s.requestedPublicItem(c)
	tags := s.googleMetaTag()
	if canonical := s.canonicalTag(c); canonical != "" {
		tags += "\n    " + canonical
	}
	if item == nil {
		return tags
	}
	return tags + "\n    " + itemHeadTags(item, s.siteName(), s.getBaseURL(c))
}

// requestedPublicItem 取当前请求 ?id 对应的已推送资源；缺 id、id 非法或资源未收录时返回 nil。
func (s *Server) requestedPublicItem(c *gin.Context) *model.PublicLink {
	raw := strings.TrimSpace(c.Query("id"))
	if raw == "" {
		return nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil
	}
	item, err := s.store.GetPublic(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		slog.Warn("读取单条资源失败", "id", raw, "err", err)
		return nil
	}
	return item
}

// metaSummaryMaxRunes 是注入到描述类标签里的最大字符数，超出部分按省略号截断。
const metaSummaryMaxRunes = 200

// itemHeadTags 生成单条资源的原始 HTML 标签。
// 值全部经过 html 转义，JSON-LD 由 json.Marshal 转义尖括号与 &，因此可以安全拼进 head。
func itemHeadTags(item *model.PublicLink, siteName, baseURL string) string {
	label := "影视资源"
	if item.MediaType == model.MediaTV {
		label = "剧集"
	} else if item.MediaType == model.MediaMovie {
		label = "电影"
	}

	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "未命名资源"
	}
	if item.Year > 0 {
		title = fmt.Sprintf("%s（%d）", title, item.Year)
	}

	desc := truncateRunes(strings.TrimSpace(item.Overview), metaSummaryMaxRunes)
	if desc == "" {
		desc = fmt.Sprintf("%s《%s》的网盘分享链接", label, item.Title)
	}

	pageURL := fmt.Sprintf("%s/?id=%d", baseURL, item.ID)
	out := []string{
		"<title>" + htmlEscape(title+" · "+siteName) + "</title>",
		`<meta name="description" content="` + htmlEscape(desc) + `" />`,
		`<meta property="og:type" content="video.movie" />`,
		`<meta property="og:url" content="` + htmlEscape(pageURL) + `" />`,
		`<meta property="og:title" content="` + htmlEscape(title) + `" />`,
		`<meta property="og:description" content="` + htmlEscape(desc) + `" />`,
		`<meta property="og:site_name" content="` + htmlEscape(siteName) + `" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
		`<meta name="twitter:title" content="` + htmlEscape(title) + `" />`,
		`<meta name="twitter:description" content="` + htmlEscape(desc) + `" />`,
	}
	if item.PosterURL != "" {
		out = append(out,
			`<meta property="og:image" content="`+htmlEscape(item.PosterURL)+`" />`,
			`<meta name="twitter:image" content="`+htmlEscape(item.PosterURL)+`" />`,
		)
	}
	if jsonld, err := json.Marshal(buildMovieSchema(item, baseURL)); err == nil {
		// id 与前端注入的单条标记一致，data-item-id 让 SPA 认出「这条已经有了」，不必再取一次
		out = append(out, `<script id="structured-data" data-item-id="`+strconv.FormatInt(item.ID, 10)+`" type="application/ld+json">`+string(jsonld)+`</script>`)
	} else {
		slog.Warn("生成结构化数据失败", "id", item.ID, "err", err)
	}
	return strings.Join(out, "\n    ")
}

// schemaDate 把库里的 "2006-01-02 15:04:05" 归一成 ISO 8601 日期；
// 解析不了就退回一个不带时区的日期串，避免 Search Console 报无效日期。
func schemaDate(sharedAt string) string {
	sharedAt = strings.TrimSpace(sharedAt)
	if sharedAt == "" {
		return ""
	}
	if t, err := time.Parse("2006-01-02 15:04:05", sharedAt); err == nil {
		return t.Format("2006-01-02")
	}
	if len(sharedAt) >= 10 {
		return sharedAt[:10]
	}
	return sharedAt
}

// htmlEscape 转义属性值与标签正文里的引号、尖括号，防止闭合 content=" 或 </title> 注入标签。
func htmlEscape(v string) string { return html.EscapeString(v) }

// truncateRunes 按字符数截断，避免把多字节中文切成半个字节。
func truncateRunes(v string, n int) string {
	r := []rune(v)
	if len(r) <= n {
		return v
	}
	return string(r[:n]) + "…"
}

// injectHeadTags 把标签插进 index.html 的 </head> 之前，供 SEO 验证与站点级元信息使用。
// 标签里带 <title> 说明是单页：先删掉模板里的站点级标题/描述/社交标签，
// 否则同一页会出现两套 og:title、description，爬虫取哪一套不确定。
func injectHeadTags(indexHTML, tags string) string {
	if tags == "" {
		return indexHTML
	}
	idx := strings.Index(indexHTML, "</head>")
	if idx < 0 {
		return indexHTML
	}
	if strings.Contains(tags, "<title>") {
		if open, lo := strings.Index(indexHTML, "<title>"), strings.Index(indexHTML, "</title>"); open >= 0 && lo > open {
			indexHTML = indexHTML[:open] + indexHTML[lo+len("</title>"):]
		}
		indexHTML = siteMetaRe.ReplaceAllString(indexHTML, "")
		idx = strings.Index(indexHTML, "</head>")
	}
	return indexHTML[:idx] + "    " + tags + "\n  " + indexHTML[idx:]
}

// siteMetaRe 匹配模板里的站点级 description / og: / twitter: 标签。
var siteMetaRe = regexp.MustCompile(`[ \t]*<meta\s+(?:property="og:[a-zA-Z_:-]*"|name="twitter:[a-zA-Z_:-]*"|name="description")\s+content="[^"]*"\s*/>\n?`)

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
	// 带 id 的深链自成一篇，q/sort/page 这类列表参数不应把详情页规范到列表 URL 上
	if id := onlyCanonicalParam(c.Request.URL.Query(), "id"); id != "" {
		return `<link rel="canonical" href="` + base + p + "?id=" + id + `" />`
	}
	q := sanitizeCanonicalQuery(c.Request.URL.Query())
	return `<link rel="canonical" href="` + base + p + q + `" />`
}

// onlyCanonicalParam 取出指定参数的安全值；缺省或非法时返回空串。
func onlyCanonicalParam(v map[string][]string, key string) string {
	list := v[key]
	if len(list) == 0 {
		return ""
	}
	return sanitizeCanonicalValue(list[0])
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
	parts := make([]string, 0, 4)
	for _, key := range []string{"id", "q", "page", "sort"} {
		list := v[key]
		if len(list) == 0 {
			continue
		}
		if safe := sanitizeCanonicalValue(list[0]); safe != "" {
			parts = append(parts, key+"="+safe)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "?" + strings.Join(parts, "&")
}

// sanitizeCanonicalValue 只保留 URL 查询值里的安全字符，防止闭合引号或空格破坏 canonical 属性。
func sanitizeCanonicalValue(v string) string {
	safe := make([]rune, 0, len(v))
	for _, r := range v {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			safe = append(safe, r)
		case r == '-' || r == '_' || r == '.' || r == '~':
			safe = append(safe, r)
		}
	}
	return string(safe)
}

// buildMovieSchema 构建电影/剧集的 Schema.org 结构化数据
func buildMovieSchema(item *model.PublicLink, baseURL string) map[string]any {
	schemaType := "Movie"
	if item.MediaType == "tv" {
		schemaType = "TVSeries"
	}

	data := map[string]any{
		"@context": "https://schema.org",
		"@type":    schemaType,
		"name":     item.Title,
		"url":      fmt.Sprintf("%s/?id=%d", baseURL, item.ID),
	}

	if date := schemaDate(item.SharedAt); date != "" {
		data["datePublished"] = date
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
