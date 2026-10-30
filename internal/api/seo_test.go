package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/store"
)

func newSEOServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(st)
	s.cfg = &config.Config{}
	s.cfg.SEO.BaseURL = "https://gy.example.com"
	return s, st
}

// seedPushedLink 建一条走完刮削+推送流程的已收录资源。
func seedPushedLink(t *testing.T, st *store.Store, scraped *model.Link) int64 {
	t.Helper()
	l := &model.Link{ShareID: "share-" + scraped.Title, URL: "https://guangya.com/s/abc", Title: scraped.Title}
	ok, err := st.Create(l)
	if err != nil || !ok {
		t.Fatalf("Create: ok=%v err=%v", ok, err)
	}
	if err := st.UpdateScraped(l.ID, scraped); err != nil {
		t.Fatalf("UpdateScraped: %v", err)
	}
	if err := st.UpdatePushed(l.ID, 123); err != nil {
		t.Fatalf("UpdatePushed: %v", err)
	}
	return l.ID
}

// seedScrapedLink 建一条只完成刮削、还没推送到频道的资源；刮削成功即算收录。
func seedScrapedLink(t *testing.T, st *store.Store, scraped *model.Link) int64 {
	t.Helper()
	l := &model.Link{ShareID: "share-" + scraped.Title, URL: "https://guangya.com/s/abc", Title: scraped.Title}
	ok, err := st.Create(l)
	if err != nil || !ok {
		t.Fatalf("Create: ok=%v err=%v", ok, err)
	}
	if err := st.UpdateScraped(l.ID, scraped); err != nil {
		t.Fatalf("UpdateScraped: %v", err)
	}
	return l.ID
}

func seoCtx(target string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Request.Host = "gy.example.com"
	return c
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func testIndexFS(head string) fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html><head>" + head + "</head><body><div id=\"app\"></div></body></html>")},
	}
}

func writeIndex(s *Server, target, head string) string {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Request.Host = "gy.example.com"
	s.WriteIndex(c, testIndexFS(head))
	return rec.Body.String()
}

func TestItemHeadTags_Movie(t *testing.T) {
	item := &model.PublicLink{
		ID: 7, Title: "肖申克的救赎", Year: 1994, MediaType: model.MediaMovie,
		Overview: "两个囚徒的故事", PosterURL: "https://image.tmdb.org/t/p/w500/x.jpg",
		TMDBID: 278, Runtime: 142, VoteAverage: 8.7, Genres: "剧情,犯罪",
	}
	got := itemHeadTags(item, "光鸭 GO", "https://gy.example.com")

	for _, want := range []string{
		"<title>肖申克的救赎（1994） - 光鸭云盘网盘分享 | 光鸭 GO</title>",
		`content="两个囚徒的故事。《肖申克的救赎》电影，1994 年，时长 142 分钟，评分 8.7/10。光鸭云盘网盘分享，点击获取提取码与在线观看地址。"`,
		`<meta property="og:url" content="https://gy.example.com/?id=7" />`,
		`<meta property="og:image" content="https://image.tmdb.org/t/p/w500/x.jpg" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
		`"url":"https://gy.example.com/?id=7"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少标签 %q，实际:\n%s", want, got)
		}
	}
	if strings.Count(got, "<title>") != 1 {
		t.Errorf("应有且仅有一个 title: %q", got)
	}
	// og:title 保持不带品牌词，社交卡片展示更紧凑
	if !strings.Contains(got, `<meta property="og:title" content="肖申克的救赎（1994）" />`) {
		t.Errorf("og:title 应只带片名与年份: %s", got)
	}
}

func TestItemHeadTags_TVWithoutPoster(t *testing.T) {
	item := &model.PublicLink{ID: 3, Title: "怪奇物语", MediaType: model.MediaTV}
	got := itemHeadTags(item, "光鸭 GO", "https://gy.example.com")
	if !strings.Contains(got, `"@type":"TVSeries"`) {
		t.Errorf("剧集应输出 TVSeries: %s", got)
	}
	if strings.Contains(got, "og:image") {
		t.Error("无海报时不应输出 og:image")
	}
	// 空简介时仍要有片名+类型+品牌词，避免退化成通用文案
	if !strings.Contains(got, `content="《怪奇物语》剧集。光鸭云盘网盘分享，点击获取提取码与在线观看地址。"`) {
		t.Errorf("空简介应生成片名+类型描述: %s", got)
	}
}

func TestItemHeadTags_EscapesHostileTitle(t *testing.T) {
	item := &model.PublicLink{
		ID: 1, Title: `</title><script>alert(1)</script>`, MediaType: model.MediaMovie,
		Overview: `" onmouseover="alert(2)`, PosterURL: `https://x.example/"><svg onload=3>`,
	}
	got := itemHeadTags(item, "光鸭 GO", "https://gy.example.com")
	if strings.Contains(got, "<script>alert(1)</script>") {
		t.Errorf("标题未转义，可注入脚本: %s", got)
	}
	if strings.Contains(got, `content="" onmouseover`) {
		t.Errorf("属性未转义，可闭合 content: %s", got)
	}
	if !strings.Contains(got, "&lt;/title&gt;") || !strings.Contains(got, "&#34;") {
		t.Errorf("应看到转义后的实体: %s", got)
	}
}

func TestCanonicalTag_IDSelfCanonicalizes(t *testing.T) {
	s, _ := newSEOServer(t)
	cases := []struct {
		target string
		want   string
	}{
		{"/?id=9&q=abc&sort=rating", `<link rel="canonical" href="https://gy.example.com/?id=9" />`},
		{"/?q=abc&sort=rating", `<link rel="canonical" href="https://gy.example.com/?q=abc&sort=rating" />`},
		{"/", `<link rel="canonical" href="https://gy.example.com/" />`},
		{"/login", ""},
	}
	for _, c := range cases {
		if got := s.canonicalTag(seoCtx(c.target)); got != c.want {
			t.Errorf("%s: canonicalTag=%q 期望 %q", c.target, got, c.want)
		}
	}
}

func TestRequestedPublicItem(t *testing.T) {
	s, st := newSEOServer(t)
	id := seedPushedLink(t, st, &model.Link{Title: "霸王别姬", Year: 1993, MediaType: model.MediaMovie})

	if got := s.requestedPublicItem(seoCtx("/?id=" + itoa(id))); got == nil || got.Title != "霸王别姬" {
		t.Errorf("已收录 id 应返回该资源，实际 %+v", got)
	}
	if got := s.requestedPublicItem(seoCtx("/")); got != nil {
		t.Errorf("无 id 应返回 nil，实际 %+v", got)
	}
	if got := s.requestedPublicItem(seoCtx("/?id=abc")); got != nil {
		t.Errorf("非法 id 应返回 nil，实际 %+v", got)
	}
	// 刮削成功但还没推送的记录同样已收录
	scrapedID := seedScrapedLink(t, st, &model.Link{Title: "未推送的新片", Year: 2026, MediaType: model.MediaMovie})
	if got := s.requestedPublicItem(seoCtx("/?id=" + itoa(scrapedID))); got == nil || got.Title != "未推送的新片" {
		t.Errorf("待推送资源应已收录，实际 %+v", got)
	}
	// 还没刮削的记录不在门户可见范围内
	pending := &model.Link{ShareID: "share-pending", Title: "待刮削"}
	if ok, err := st.Create(pending); err != nil || !ok {
		t.Fatalf("Create pending: %v %v", ok, err)
	}
	if got := s.requestedPublicItem(seoCtx("/?id=" + itoa(pending.ID))); got != nil {
		t.Errorf("未刮削资源应返回 nil，实际 %+v", got)
	}
}

func TestWriteIndexInjectsItemMeta(t *testing.T) {
	s, st := newSEOServer(t)
	id := seedPushedLink(t, st, &model.Link{Title: "教父", Year: 1972, MediaType: model.MediaMovie, Overview: "科莱昂家族"})
	head := "<title>光鸭 GO - 网盘资源分享平台</title>"
	body := writeIndex(s, "/?id="+itoa(id), head)

	for _, want := range []string{
		"<title>教父（1972） - 光鸭云盘网盘分享 | 光鸭 GO</title>",
		`<link rel="canonical" href="https://gy.example.com/?id=`,
		`<script id="structured-data" data-item-id="` + itoa(id) + `" type="application/ld+json">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("未注入 %q，实际:\n%s", want, body)
		}
	}
	if strings.Count(body, "<title>") != 1 {
		t.Errorf("模板 title 未删除，出现重复标题: %s", body)
	}
	if strings.Contains(body, head) {
		t.Errorf("站点标题应被单页标题替换: %s", body)
	}
	if strings.Contains(body, `<script>alert`) {
		t.Error("不应出现可执行内联脚本")
	}
}

func TestWriteIndexListKeepsSiteTitle(t *testing.T) {
	s, _ := newSEOServer(t)
	head := "<title>光鸭 GO - 网盘资源分享平台</title>"
	body := writeIndex(s, "/", head)
	if !strings.Contains(body, head) {
		t.Errorf("列表页应保留站点标题: %s", body)
	}
	if strings.Contains(body, "og:type\" content=\"video.movie\"") {
		t.Errorf("列表页不应带单页标签: %s", body)
	}
}

func TestWriteIndexUnknownIDFallsBackToSite(t *testing.T) {
	s, _ := newSEOServer(t)
	head := "<title>光鸭 GO - 网盘资源分享平台</title>"
	body := writeIndex(s, "/?id=99999", head)
	if !strings.Contains(body, head) {
		t.Errorf("不存在的 id 应退回站点级标签: %s", body)
	}
}

func TestInjectHeadTags_ReplacesTemplateTitle(t *testing.T) {
	src := `<head><title>旧标题</title><meta name="x" /></head>`
	got := injectHeadTags(src, "<title>新标题</title>")
	if strings.Contains(got, "旧标题") {
		t.Errorf("旧标题未移除: %s", got)
	}
	if strings.Count(got, "<title>") != 1 {
		t.Errorf("应有且仅有一个标题: %s", got)
	}
	if !strings.Contains(got, `<meta name="x" />`) {
		t.Error("其余 head 内容应保留")
	}
	// 只有闭合标签没有开标签时不能越界删除
	broken := `<head></title></head>`
	out := injectHeadTags(broken, "<title>t</title>")
	if !strings.HasPrefix(out, "<head></title>") {
		t.Errorf("畸形模板的游离闭合标签应原样保留: %s", out)
	}
	if strings.Count(out, "<title>") != 1 {
		t.Errorf("畸形模板应只注入一个新标题: %s", out)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("光鸭网盘资源", 2); got != "光鸭…" {
		t.Errorf("按字符截断异常: %q", got)
	}
	if got := truncateRunes("短", 10); got != "短" {
		t.Errorf("未超长应原样返回: %q", got)
	}
}

func TestSchemaDate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"2026-10-27 16:37:34", "2026-10-27"},
		{"2026-10-27", "2026-10-27"},
		{"2026-10", "2026-10"},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := schemaDate(c.in); got != c.want {
			t.Errorf("schemaDate(%q)=%q 期望 %q", c.in, got, c.want)
		}
	}
}

func TestBuildMovieSchema_DatePublished(t *testing.T) {
	item := &model.PublicLink{ID: 5, Title: "猎户座", MediaType: model.MediaMovie, SharedAt: "2026-10-27 16:37:34", TMDBID: 42}
	got := buildMovieSchema(item, "https://gy.example.com")
	if got["datePublished"] != "2026-10-27" {
		t.Errorf("datePublished 应为 ISO 日期，实际 %v", got["datePublished"])
	}
	if got["sameAs"] != "https://www.themoviedb.org/movie/42" {
		t.Errorf("sameAs 异常: %v", got["sameAs"])
	}
	noDate := buildMovieSchema(&model.PublicLink{ID: 6, Title: "无日期"}, "https://gy.example.com")
	if _, ok := noDate["datePublished"]; ok {
		t.Error("空 shared_at 不应输出 datePublished")
	}
}

func TestInjectHeadTags_ItemTagsReplaceSiteSocialMeta(t *testing.T) {
	head := `<title>光鸭 GO - 网盘资源分享平台</title>` + "\n" +
		`    <meta name="description" content="站点简介" />` + "\n" +
		`    <meta property="og:type" content="website" />` + "\n" +
		`    <meta property="og:title" content="站点标题" />` + "\n" +
		`    <meta name="twitter:card" content="summary" />` + "\n" +
		`    <meta name="keywords" content="光鸭,网盘资源" />`
	item := "<title>教父（1972） · 光鸭 GO</title>\n" +
		`    <meta name="description" content="科莱昂家族" />` + "\n" +
		`    <meta property="og:type" content="video.movie" />` + "\n" +
		`    <meta property="og:title" content="教父（1972）" />` + "\n" +
		`    <meta name="twitter:card" content="summary_large_image" />`

	got := injectHeadTags("<head>\n"+head+"\n</head>", item)

	for _, want := range []string{`content="站点简介"`, `content="website"`, `content="站点标题"`, `content="summary"`} {
		if strings.Contains(got, want) {
			t.Errorf("站点级标签未移除，出现重复: %s 含 %s", got, want)
		}
	}
	for _, keep := range []string{`content="科莱昂家族"`, `content="video.movie"`, `content="教父（1972）"`, `content="summary_large_image"`, `content="光鸭,网盘资源"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("单页标签或无关标签被误删: %s", got)
		}
	}
	if strings.Count(got, "<title>") != 1 {
		t.Errorf("标题应唯一: %s", got)
	}
}

func TestWriteIndex_ItemPageHasSingleOGTitle(t *testing.T) {
	s, st := newSEOServer(t)
	id := seedPushedLink(t, st, &model.Link{Title: "阿凡达", Year: 2009, MediaType: model.MediaMovie, Overview: "潘多拉星球"})
	head := `<title>光鸭 GO - 网盘资源分享平台</title>` + "\n    " +
		`<meta property="og:type" content="website" />` + "\n    " +
		`<meta property="og:title" content="光鸭 GO - 网盘资源分享平台" />`
	body := writeIndex(s, "/?id="+itoa(id), head)
	if n := strings.Count(body, `property="og:title"`); n != 1 {
		t.Errorf("og:title 应唯一，实际 %d 个:\n%s", n, body)
	}
	if n := strings.Count(body, `<meta name="description"`); n != 1 {
		t.Errorf("description 应唯一，实际 %d 个", n)
	}
}

func TestGetBaseURL(t *testing.T) {
	s := &Server{}
	c := seoCtx("/")
	if got := s.getBaseURL(c); got != "http://gy.example.com" {
		t.Errorf("默认应回退请求 Host: %q", got)
	}

	c.Request.Header.Set("X-Forwarded-Proto", "HTTPS")
	if got := s.getBaseURL(c); got != "https://gy.example.com" {
		t.Errorf("反代声明 HTTPS 时应升级协议: %q", got)
	}

	s.cfg = &config.Config{SEO: config.SEOConfig{BaseURL: " https://fixed.example.com/ "}}
	if got := s.getBaseURL(c); got != "https://fixed.example.com" {
		t.Errorf("固定 base_url 应优先并去掉尾部斜杠: %q", got)
	}

	// server.public_url 是第二优先级：没填 base_url 时用它，填了就让位
	s.cfg = &config.Config{Server: config.ServerConfig{PublicURL: "https://gy.example.com"}}
	if got := s.getBaseURL(c); got != "https://gy.example.com" {
		t.Errorf("未固定 base_url 时应回落到对外地址: %q", got)
	}
	s.cfg.SEO.BaseURL = "https://seo.example.com"
	if got := s.getBaseURL(c); got != "https://seo.example.com" {
		t.Errorf("base_url 应优先于对外地址: %q", got)
	}
}

// TestBuildItemGraph_Breadcrumb 覆盖 Google 面包屑富结果需要的最小节点集合。
func TestBuildItemGraph_Breadcrumb(t *testing.T) {
	item := &model.PublicLink{ID: 9, Title: "流浪地球", MediaType: model.MediaMovie, Year: 2019}
	got := buildItemGraph(item, "https://gy.example.com")
	graph, ok := got["@graph"].([]any)
	if !ok || len(graph) != 2 {
		t.Fatalf("@graph 应包含 Movie + Breadcrumb 两个节点: %+v", got["@graph"])
	}
	crumb, ok := graph[1].(map[string]any)
	if !ok || crumb["@type"] != "BreadcrumbList" {
		t.Fatalf("第二节点应是 BreadcrumbList: %+v", graph[1])
	}
	elements := crumb["itemListElement"].([]any)
	if len(elements) != 3 {
		t.Fatalf("面包屑应有 首页/电影/片名 三层，实际 %d 层", len(elements))
	}
	if elements[1].(map[string]any)["item"] != "https://gy.example.com/?media_type=movie" {
		t.Errorf("中间层 URL 错误: %v", elements[1])
	}
	if elements[2].(map[string]any)["item"] != "https://gy.example.com/?id=9" {
		t.Errorf("末尾 URL 错误: %v", elements[2])
	}
}

// TestBuildItemGraph_NoMediaType 未刮削类型的资源只输出两层面包屑。
func TestBuildItemGraph_NoMediaType(t *testing.T) {
	got := buildItemGraph(&model.PublicLink{ID: 1, Title: "未知"}, "https://gy.example.com")
	graph := got["@graph"].([]any)
	crumbs := graph[1].(map[string]any)["itemListElement"].([]any)
	if len(crumbs) != 2 {
		t.Errorf("无 media_type 时应省略中间层，实际 %d 层", len(crumbs))
	}
}

// TestItemDescription_OverviewTruncated 简介超长时截断到 200 rune，防止描述膨胀。
func TestItemDescription_OverviewTruncated(t *testing.T) {
	long := strings.Repeat("鸭", metaSummaryMaxRunes+50)
	item := &model.PublicLink{Title: "测试", MediaType: model.MediaMovie, Overview: long}
	got := itemDescription(item, mediaLabel(item.MediaType))
	if !strings.HasPrefix(got, strings.Repeat("鸭", metaSummaryMaxRunes)+"…") {
		t.Errorf("简介应截断到 %d rune 并追加省略号: %q", metaSummaryMaxRunes, got[:min(30, len(got))])
	}
	if !strings.HasSuffix(got, "光鸭云盘网盘分享，点击获取提取码与在线观看地址。") {
		t.Errorf("描述结尾必须包含品牌词: %q", got)
	}
}

// callHandler 用测试上下文跑一个只读 handler，返回响应体与响应头。
func callHandler(t *testing.T, h func(*gin.Context), target string) (string, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Request.Host = "gy.example.com"
	h(c)
	return rec.Body.String(), rec.Header()
}

func TestSitemapXML_HomeAndItems(t *testing.T) {
	s, st := newSEOServer(t)
	seedPushedLink(t, st, &model.Link{Title: "教父", Year: 1972, MediaType: model.MediaMovie})
	seedScrapedLink(t, st, &model.Link{Title: "刚刮削的", Year: 2026, MediaType: model.MediaMovie})

	body, head := callHandler(t, s.sitemapXML, "/sitemap.xml")
	if ct := head.Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("Content-Type = %q，期望 application/xml", ct)
	}
	if cc := head.Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q，期望 public, max-age=3600", cc)
	}
	if got := strings.Count(body, "<loc>"); got != 3 {
		t.Errorf("应输出首页 + 2 条深链，实际 %d: %s", got, body)
	}
	// 首页与各条目的 lastmod 都取自 updated_at，不能留空
	if got := strings.Count(body, "<lastmod>"); got != 3 {
		t.Errorf("lastmod 应覆盖首页与每条深链，实际 %d: %s", got, body)
	}
	if !strings.Contains(body, "<loc>https://gy.example.com/?id=") {
		t.Errorf("缺少资源深链: %s", body)
	}

	// 首页地址只在 part=0 出现，否则 sitemapindex 会把同一个入口重复列几遍
	if !strings.Contains(body, "<loc>https://gy.example.com/</loc>") {
		t.Errorf("首片应包含首页地址: %s", body)
	}
	part1, _ := callHandler(t, s.sitemapXML, "/sitemap.xml?part=1")
	if strings.Contains(part1, "<loc>https://gy.example.com/</loc>") {
		t.Errorf("非首片不应重复首页地址: %s", part1)
	}
}

func TestSitemapStore_PagingAndTotal(t *testing.T) {
	_, st := newSEOServer(t)
	for _, title := range []string{"甲", "乙", "丙"} {
		seedPushedLink(t, st, &model.Link{Title: title, MediaType: model.MediaMovie})
	}
	// 未刮削的记录不进 sitemap
	pending := &model.Link{ShareID: "share-pending-sitemap", Title: "待刮削"}
	if ok, err := st.Create(pending); err != nil || !ok {
		t.Fatalf("Create pending: ok=%v err=%v", ok, err)
	}

	first, err := st.Sitemap(0, 2)
	if err != nil {
		t.Fatalf("Sitemap: %v", err)
	}
	if first.Total != 3 || len(first.Entries) != 2 {
		t.Errorf("首页应为 3 条取 2 条，实际 total=%d len=%d", first.Total, len(first.Entries))
	}
	if schemaDate(first.Latest) == "" {
		t.Error("全站最近更新时间不应为空")
	}
	second, err := st.Sitemap(2, 2)
	if err != nil {
		t.Fatalf("Sitemap(offset): %v", err)
	}
	if len(second.Entries) != 1 {
		t.Errorf("第二片应剩 1 条，实际 %d", len(second.Entries))
	}
	if second.Entries[0].UpdatedAt == "" {
		t.Error("条目缺少 updated_at")
	}
	if second.Total != 3 {
		t.Errorf("分片的 total 应始终是收录总数，实际 %d", second.Total)
	}
}

func TestSitemapIndex_ShardsByPart(t *testing.T) {
	s, _ := newSEOServer(t)
	target := maxSitemapURLs*2 + 1
	body, head := callHandler(t, func(c *gin.Context) {
		s.sitemapIndex(c, "https://gy.example.com", target)
	}, "/sitemap.xml")

	if !strings.Contains(body, "<sitemapindex") {
		t.Errorf("应输出 sitemapindex: %s", body)
	}
	if got := strings.Count(body, "<sitemap>"); got != 3 {
		t.Errorf("12501 条应切成 3 片，实际 %d: %s", got, body)
	}
	for _, want := range []string{"?part=0", "?part=1", "?part=2"} {
		if !strings.Contains(body, "/sitemap.xml"+want) {
			t.Errorf("缺少分片地址 %s: %s", want, body)
		}
	}
	if head.Get("Content-Type") == "" {
		t.Error("sitemapindex 缺少 Content-Type")
	}
}

func TestRobotsTXT_CacheControl(t *testing.T) {
	s, _ := newSEOServer(t)
	body, head := callHandler(t, s.robotsTXT, "/robots.txt")
	if cc := head.Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if !strings.Contains(body, "Sitemap: https://gy.example.com/sitemap.xml") {
		t.Errorf("robots.txt 未指向 sitemap: %s", body)
	}
}
