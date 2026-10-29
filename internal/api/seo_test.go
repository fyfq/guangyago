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
		"<title>肖申克的救赎（1994） · 光鸭 GO</title>",
		`<meta name="description" content="两个囚徒的故事" />`,
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
	if !strings.Contains(got, `content="剧集《怪奇物语》的网盘分享链接"`) {
		t.Errorf("空简介应退回兜底描述: %s", got)
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
		"<title>教父（1972） · 光鸭 GO</title>",
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
