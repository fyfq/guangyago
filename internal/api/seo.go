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

// maxSitemapURLs 是单个 sitemap 文件的条目上限。超出后 /sitemap.xml 改吐 sitemapindex，
// 按 ?part=<n> 分片，避免静默丢掉最老的记录。
const maxSitemapURLs = 5000

// sitemapNS 是 sitemaps.org 0.9 命名空间。
const sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

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
	// robots.txt 与 sitemap 都不常变，给一分钟到一小时的缓存，减少爬虫重复回源。
	c.Header("Cache-Control", "public, max-age=3600")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(content))
}

type sitemapURL struct {
	Loc        string  `xml:"loc"`
	LastMod    string  `xml:"lastmod,omitempty"`
	ChangeFreq string  `xml:"changefreq,omitempty"`
	Priority   float64 `xml:"priority,omitempty"`
}

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapIndexEntry struct {
	Loc string `xml:"loc"`
}

type sitemapIndexDoc struct {
	XMLName  xml.Name            `xml:"sitemapindex"`
	Xmlns    string              `xml:"xmlns,attr"`
	Sitemaps []sitemapIndexEntry `xml:"sitemap"`
}

// sitemapXML 动态生成站点地图：首页 + 已收录资源深链。
// 收录总量超过单文件上限时，首页请求返回 sitemapindex，各分片仍由本 handler 按 part 提供。
func (s *Server) sitemapXML(c *gin.Context) {
	baseURL := s.getBaseURL(c)
	part, _ := strconv.Atoi(c.Query("part"))
	if part < 0 {
		part = 0
	}

	data, err := s.store.Sitemap(part*maxSitemapURLs, maxSitemapURLs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成站点地图失败"})
		return
	}
	c.Header("Cache-Control", "public, max-age=3600")

	if part == 0 && data.Total > maxSitemapURLs {
		s.sitemapIndex(c, baseURL, data.Total)
		return
	}

	// 首页的 lastmod 取全站最近更新时间，爬虫据此判断列表页是否值得重新抓取；
	// 只在首片输出，否则分片索引会把同一条首页 URL 重复列几遍。
	urls := make([]sitemapURL, 0, len(data.Entries)+1)
	if part == 0 {
		urls = append(urls, sitemapURL{Loc: baseURL + "/", LastMod: schemaDate(data.Latest), ChangeFreq: "daily", Priority: 1.0})
	}
	for _, e := range data.Entries {
		urls = append(urls, sitemapURL{
			Loc:        fmt.Sprintf("%s/?id=%d", baseURL, e.ID),
			LastMod:    schemaDate(e.UpdatedAt),
			ChangeFreq: "monthly",
			Priority:   0.8,
		})
	}
	slog.Debug("生成站点地图", "part", part, "urls", len(urls), "total", data.Total)
	writeSitemapXML(c, sitemapURLSet{Xmlns: sitemapNS, URLs: urls})
}

// sitemapIndex 在条目超过单文件上限时输出分片索引，分片 URL 指回同一个 handler。
func (s *Server) sitemapIndex(c *gin.Context, baseURL string, total int) {
	entries := make([]sitemapIndexEntry, (total+maxSitemapURLs-1)/maxSitemapURLs)
	for i := range entries {
		entries[i].Loc = fmt.Sprintf("%s/sitemap.xml?part=%d", baseURL, i)
	}
	slog.Info("站点地图条目超过单文件上限，已分片", "total", total, "parts", len(entries))
	writeSitemapXML(c, sitemapIndexDoc{Xmlns: sitemapNS, Sitemaps: entries})
}

// writeSitemapXML 输出 sitemap 文档，urlset 与 sitemapindex 共用同一段响应头与编码逻辑。
func writeSitemapXML(c *gin.Context, v any) {
	c.Header("Content-Type", "application/xml; charset=utf-8")
	c.Writer.WriteString(xml.Header)
	if err := xml.NewEncoder(c.Writer).Encode(v); err != nil {
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

// getBaseURL 返回站点基础 URL：seo.base_url 优先，其次 server.public_url（对外访问地址），
// 两者都没配置时才从请求头推断——反代改写 Host 时推断结果会是内网地址。
func (s *Server) getBaseURL(c *gin.Context) string {
	s.configMu.RLock()
	fixed := ""
	public := ""
	if s.cfg != nil {
		fixed = strings.TrimSpace(s.cfg.SEO.BaseURL)
		public = strings.TrimSpace(s.cfg.Server.PublicURL)
	}
	s.configMu.RUnlock()
	if fixed == "" {
		fixed = public
	}
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
	parts := make([]string, 0, 4)
	if tag := s.googleMetaTag(); tag != "" {
		parts = append(parts, tag)
	}
	if tag := s.brandingHeadTags(); tag != "" {
		parts = append(parts, tag)
	}
	if canonical := s.canonicalTag(c); canonical != "" {
		parts = append(parts, canonical)
	}
	if item != nil {
		parts = append(parts, itemHeadTags(item, s.siteName(), s.getBaseURL(c)))
	}
	return strings.Join(parts, "\n    ")
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
	label := mediaLabel(item.MediaType)
	headTitle := strings.TrimSpace(item.Title)
	if headTitle == "" {
		headTitle = "未命名资源"
	}
	if item.Year > 0 {
		headTitle = fmt.Sprintf("%s（%d）", headTitle, item.Year)
	}
	// 品牌词"光鸭云盘"必须出现在 title 里，否则片名+光鸭云盘的长尾查询匹配不上
	seoTitle := headTitle + " - 光鸭云盘网盘分享 | " + siteName
	desc := itemDescription(item, label)

	pageURL := fmt.Sprintf("%s/?id=%d", baseURL, item.ID)
	out := []string{
		"<title>" + htmlEscape(seoTitle) + "</title>",
		`<meta name="description" content="` + htmlEscape(desc) + `" />`,
		`<meta property="og:type" content="video.movie" />`,
		`<meta property="og:url" content="` + htmlEscape(pageURL) + `" />`,
		`<meta property="og:title" content="` + htmlEscape(headTitle) + `" />`,
		`<meta property="og:description" content="` + htmlEscape(desc) + `" />`,
		`<meta property="og:site_name" content="` + htmlEscape(siteName) + `" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
		`<meta name="twitter:title" content="` + htmlEscape(headTitle) + `" />`,
		`<meta name="twitter:description" content="` + htmlEscape(desc) + `" />`,
	}
	if item.PosterURL != "" {
		out = append(out,
			`<meta property="og:image" content="`+htmlEscape(item.PosterURL)+`" />`,
			`<meta name="twitter:image" content="`+htmlEscape(item.PosterURL)+`" />`,
		)
	}
	if jsonld, err := json.Marshal(buildItemGraph(item, baseURL)); err == nil {
		// id 与前端注入的单条标记一致，data-item-id 让 SPA 认出「这条已经有了」，不必再取一次
		out = append(out, `<script id="structured-data" data-item-id="`+strconv.FormatInt(item.ID, 10)+`" type="application/ld+json">`+string(jsonld)+`</script>`)
	} else {
		slog.Warn("生成结构化数据失败", "id", item.ID, "err", err)
	}
	return strings.Join(out, "\n    ")
}

// mediaLabel 返回影视类型的中文名，空类型回退到通用文案。
func mediaLabel(mt model.MediaType) string {
	switch mt {
	case model.MediaTV:
		return "剧集"
	case model.MediaMovie:
		return "电影"
	}
	return "影视资源"
}

// itemDescription 组装描述文本：简介在前保证每页唯一（对抗 Google scraped-content 判定），
// 片名 + 类型 + 年份 + 时长 + 评分中段承载关键词信号，结尾固定"光鸭云盘网盘分享"打进目标查询空间。
// 不放提取码，避免 SERP 直接给出访问方式绕过点击进站。
func itemDescription(item *model.PublicLink, label string) string {
	var b strings.Builder
	if ov := strings.TrimSpace(item.Overview); ov != "" {
		b.WriteString(truncateRunes(ov, metaSummaryMaxRunes))
		if !strings.HasSuffix(ov, "。") {
			b.WriteString("。")
		}
	}
	b.WriteString("《")
	b.WriteString(strings.TrimSpace(item.Title))
	b.WriteString("》")
	b.WriteString(label)
	if item.Year > 0 {
		fmt.Fprintf(&b, "，%d 年", item.Year)
	}
	if item.Runtime > 0 {
		fmt.Fprintf(&b, "，时长 %d 分钟", item.Runtime)
	}
	if item.VoteAverage > 0 {
		fmt.Fprintf(&b, "，评分 %.1f/10", item.VoteAverage)
	}
	b.WriteString("。光鸭云盘网盘分享，点击获取提取码与在线观看地址。")
	return b.String()
}

// buildItemGraph 输出 @graph：资源主体 + 面包屑。
// Google 会依据 BreadcrumbList 在 SERP 显示面包屑富结果；
// 不做 VideoObject，因为其 contentUrl 要求可下载视频直链，网盘分享页会触发 rich result 违规。
func buildItemGraph(item *model.PublicLink, baseURL string) map[string]any {
	pageURL := fmt.Sprintf("%s/?id=%d", baseURL, item.ID)
	crumbs := []any{
		map[string]any{"@type": "ListItem", "position": 1, "name": "首页", "item": baseURL + "/"},
	}
	if item.MediaType != "" {
		crumbs = append(crumbs, map[string]any{
			"@type":    "ListItem",
			"position": len(crumbs) + 1,
			"name":     mediaLabel(item.MediaType),
			"item":     fmt.Sprintf("%s/?media_type=%s", baseURL, item.MediaType),
		})
	}
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "未命名资源"
	}
	crumbs = append(crumbs, map[string]any{
		"@type":    "ListItem",
		"position": len(crumbs) + 1,
		"name":     title,
		"item":     pageURL,
	})
	return map[string]any{
		"@context": "https://schema.org",
		"@graph": []any{
			buildMovieSchema(item, baseURL),
			map[string]any{"@type": "BreadcrumbList", "itemListElement": crumbs},
		},
	}
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
// 走这里而不是直接透传静态文件，是因为爬虫读不到前端 JS 补挂的 meta 标签，
// 站点名也要在服务端把模板占位符换成配置值。
func (s *Server) WriteIndex(c *gin.Context, fsys fs.FS) {
	body, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "首页不可用"})
		return
	}
	doc := injectHeadTags(string(body), s.headTags(c))
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.applySiteName(doc)))
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
