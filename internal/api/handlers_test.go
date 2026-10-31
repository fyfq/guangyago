package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/odin/gygo/internal/auth"
	"github.com/odin/gygo/internal/model"
	"github.com/odin/gygo/internal/pipeline"
)

func TestHealthAndSEORoutes(t *testing.T) {
	e := newAPIEnv(t)

	rec := e.callAnon(http.MethodGet, "/api/health", nil)
	requireStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("健康检查响应异常: %s", rec.Body.String())
	}

	rec = e.callAnon(http.MethodGet, "/robots.txt", nil)
	requireStatus(t, rec, http.StatusOK)
	robots := rec.Body.String()
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("robots.txt Content-Type 异常: %q", rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{"Disallow: /admin", "Disallow: /login", "Disallow: /setup", "Disallow: /api/", "Sitemap: http://example.com/sitemap.xml"} {
		if !strings.Contains(robots, want) {
			t.Errorf("robots.txt 缺少 %q: %s", want, robots)
		}
	}

	rec = e.callAnon(http.MethodGet, "/api/structured-data", nil)
	requireStatus(t, rec, http.StatusOK)
	site := rec.Body.String()
	for _, want := range []string{`"@type":"WebSite"`, `"name":"gygo"`, `"url":"http://example.com"`} {
		if !strings.Contains(site, want) {
			t.Errorf("站点结构化数据缺少 %q: %s", want, site)
		}
	}

	rec = e.callAnon(http.MethodGet, "/sitemap.xml", nil)
	requireStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/xml") {
		t.Errorf("sitemap Content-Type 异常: %q", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "<loc>http://example.com/</loc>") {
		t.Errorf("站点地图缺少首页: %s", rec.Body.String())
	}
}

func TestWriteIndexMissingFile(t *testing.T) {
	e := newAPIEnv(t)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	e.srv.WriteIndex(c, fstest.MapFS{})
	if rec.Code != http.StatusNotFound {
		t.Errorf("缺少 index.html 应回 404，实际 %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLinksAPI(t *testing.T) {
	e := newAPIEnv(t)
	pending := e.seedLink("links-pending", "待刮削")
	scraped := e.seedLink("links-scraped", "已刮削")
	if err := e.st.UpdateScraped(scraped.ID, &model.Link{
		Title: "教父", Year: 1972, MediaType: model.MediaMovie, VoteAverage: 9.2, TMDBID: 238,
	}); err != nil {
		t.Fatalf("写入刮削结果失败: %v", err)
	}

	rec := e.call(http.MethodGet, "/api/links", nil)
	requireStatus(t, rec, http.StatusOK)
	var list struct {
		Total int          `json:"total"`
		Items []model.Link `json:"items"`
	}
	decodeInto(t, rec, &list)
	if list.Total != 2 || len(list.Items) != 2 {
		t.Fatalf("列表数量异常: %+v", list)
	}
	if list.Items[0].ID != scraped.ID {
		t.Errorf("列表应按 id 倒序，首条 = %d 期望 %d", list.Items[0].ID, scraped.ID)
	}

	rec = e.call(http.MethodGet, "/api/links?status=pending", nil)
	decodeInto(t, rec, &list)
	if list.Total != 1 || list.Items[0].ID != pending.ID {
		t.Errorf("单状态筛选异常: %+v", list)
	}
	rec = e.call(http.MethodGet, "/api/links?status=scraped_set", nil)
	decodeInto(t, rec, &list)
	if list.Total != 1 || list.Items[0].ID != scraped.ID {
		t.Errorf("状态集合筛选异常: %+v", list)
	}
	rec = e.call(http.MethodGet, "/api/links?q=教父", nil)
	decodeInto(t, rec, &list)
	if list.Total != 1 {
		t.Errorf("关键词筛选异常: %+v", list)
	}
	requireStatus(t, e.call(http.MethodGet, "/api/links?limit=999&offset=-3", nil), http.StatusOK)

	rec = e.call(http.MethodGet, "/api/links/"+idPath(pending.ID), nil)
	requireStatus(t, rec, http.StatusOK)
	var one model.Link
	decodeInto(t, rec, &one)
	if one.ShareID != "links-pending" {
		t.Errorf("单条详情异常: %+v", one)
	}
	requireStatus(t, e.call(http.MethodGet, "/api/links/99999", nil), http.StatusNotFound)
	requireStatus(t, e.call(http.MethodGet, "/api/links/abc", nil), http.StatusBadRequest)

	rec = e.call(http.MethodGet, "/api/links/"+idPath(scraped.ID)+"/caption", nil)
	requireStatus(t, rec, http.StatusOK)
	var caption struct {
		HTML string `json:"html"`
		Text string `json:"text"`
	}
	decodeInto(t, rec, &caption)
	if !strings.Contains(caption.HTML, "教父") || caption.Text == "" {
		t.Errorf("推送文案预览异常: %+v", caption)
	}
	requireStatus(t, e.call(http.MethodGet, "/api/links/99999/caption", nil), http.StatusNotFound)
	requireStatus(t, e.call(http.MethodGet, "/api/links/abc/caption", nil), http.StatusBadRequest)

	requireStatus(t, e.call(http.MethodDelete, "/api/links/abc", nil), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodDelete, "/api/links/"+idPath(pending.ID), nil), http.StatusOK)
	gone, err := e.st.Get(pending.ID)
	if err != nil || gone != nil {
		t.Errorf("删除后不应再读到记录: %v %+v", err, gone)
	}
}

func TestImportAPI(t *testing.T) {
	e := newAPIEnv(t)

	requireStatus(t, e.call(http.MethodPost, "/api/links/text", map[string]string{"text": "  "}), http.StatusBadRequest)

	rec := e.call(http.MethodPost, "/api/links/text", map[string]string{
		"text": "分享「流浪地球」 https://guangyapan.com/s/wandering_abc?code=8899",
	})
	requireStatus(t, rec, http.StatusOK)
	var result ImportResult
	decodeInto(t, rec, &result)
	if result.Inserted != 1 || result.Total != 1 || len(result.IDs) != 1 {
		t.Fatalf("文本导入异常: %+v", result)
	}

	csv := "文件名,链接,提取码,分享时间,分享状态\n" +
		"老友记 (1994) {tmdb-1668},https://guangyapan.com/s/friends_abc,1234,2026-01-02 03:04:05,成功\n"

	rec = e.call(http.MethodPost, "/api/links/import", "https://guangyapan.com/s/rawtext_abc")
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &result)
	if result.Inserted != 1 {
		t.Fatalf("原始文本导入异常: %+v", result)
	}

	// 首行含「链接」时应识别为 CSV
	rec = e.call(http.MethodPost, "/api/links/import", csv)
	requireStatus(t, rec, http.StatusOK)
	result = ImportResult{}
	decodeInto(t, rec, &result)
	if result.Inserted != 1 {
		t.Fatalf("CSV 导入异常: %+v", result)
	}

	requireStatus(t, e.call(http.MethodPost, "/api/links/import", ""), http.StatusBadRequest)

	// multipart 上传：第一行与上面重复应跳过，第二行新增
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	file, err := mw.CreateFormFile("file", "links.csv")
	if err != nil {
		t.Fatalf("构造上传表单失败: %v", err)
	}
	if _, err := file.Write([]byte(csv + "怪奇物语 (2016),https://guangyapan.com/s/stranger_abc,5566,2026-02-03 04:05:06,成功\n")); err != nil {
		t.Fatalf("写入上传内容失败: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭上传表单失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/links/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: e.token})
	rec = e.serve(req)
	requireStatus(t, rec, http.StatusOK)
	result = ImportResult{}
	decodeInto(t, rec, &result)
	if result.Inserted != 1 || result.Skipped != 1 {
		t.Fatalf("多段上传导入异常: %+v", result)
	}
}

func TestExportAndStatsAPI(t *testing.T) {
	e := newAPIEnv(t)
	l := e.seedLink("export-one", "=1+1")
	if err := e.st.UpdateScraped(l.ID, &model.Link{
		Title: "教父, 第一部", Year: 1972, MediaType: model.MediaMovie,
	}); err != nil {
		t.Fatalf("写入刮削结果失败: %v", err)
	}

	rec := e.call(http.MethodGet, "/api/links/export", nil)
	requireStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.HasPrefix(body, "\xef\xbb\xbf") {
		t.Error("导出 CSV 缺少 BOM")
	}
	if !strings.Contains(body, "文件名") || !strings.Contains(body, "推送消息ID") {
		t.Errorf("导出表头异常: %s", body)
	}
	if !strings.Contains(body, "'=1+1") {
		t.Errorf("公式注入字段应加前导单引号: %s", body)
	}
	if !strings.Contains(body, `"教父, 第一部"`) {
		t.Errorf("含逗号的字段应加引号: %s", body)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "guangya-links.csv") {
		t.Errorf("下载文件名异常: %q", cd)
	}

	rec = e.call(http.MethodGet, "/api/stats", nil)
	requireStatus(t, rec, http.StatusOK)
	var stats map[string]int
	decodeInto(t, rec, &stats)
	if stats[string(model.StatusScraped)] != 1 {
		t.Errorf("scraped 计数异常: %v", stats)
	}
	if len(stats) != 9 {
		t.Errorf("应固定输出 8 个状态并带 total，实际 %d: %v", len(stats), stats)
	}
	if stats["total"] != 1 {
		t.Errorf("total 应等于库内记录总数，实际 %v: %v", stats["total"], stats)
	}
}

func TestBatchDeleteAPI(t *testing.T) {
	e := newAPIEnv(t)
	a := e.seedLink("batch-a", "a")
	b := e.seedLink("batch-b", "b")

	requireStatus(t, e.call(http.MethodPost, "/api/links/batch-delete", map[string]any{}), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPost, "/api/links/batch-delete", map[string]any{"ids": []int64{}}), http.StatusBadRequest)

	rec := e.call(http.MethodPost, "/api/links/batch-delete", map[string]any{"ids": []int64{a.ID, b.ID}})
	requireStatus(t, rec, http.StatusOK)
	var out struct {
		Deleted int64 `json:"deleted"`
	}
	decodeInto(t, rec, &out)
	if out.Deleted != 2 {
		t.Errorf("批量删除数量异常: %+v", out)
	}
}

func TestPortalAPI(t *testing.T) {
	e := newAPIEnv(t)
	movie := e.seedPublishedLink("portal-movie", &model.Link{
		Title: "教父", Year: 1972, MediaType: model.MediaMovie, VoteAverage: 9.2,
		SharedAt: "2026-10-01 10:00:00", Overview: "科莱昂家族",
		PosterURL: "https://image.tmdb.org/t/p/w500/a.jpg", TMDBID: 238, Runtime: 175,
		Genres: "剧情,犯罪",
	})
	tv := e.seedPublishedLink("portal-tv", &model.Link{
		Title: "怪奇物语", Year: 2016, MediaType: model.MediaTV, VoteAverage: 8.7,
		SharedAt: "2026-10-02 10:00:00",
	})
	e.seedLink("portal-pending", "待推送")

	rec := e.callAnon(http.MethodGet, "/api/portal/links", nil)
	requireStatus(t, rec, http.StatusOK)
	var list struct {
		Total int                `json:"total"`
		Items []model.PublicLink `json:"items"`
	}
	decodeInto(t, rec, &list)
	if list.Total != 2 || len(list.Items) != 2 {
		t.Fatalf("门户只应展示已推送记录: %+v", list)
	}
	if list.Items[0].ID != tv.ID {
		t.Errorf("应按分享时间倒序，首条 = %d 期望 %d", list.Items[0].ID, tv.ID)
	}
	if list.Items[0].LinkCount != 1 || len(list.Items[0].Links) != 1 {
		t.Errorf("单条分享的链接数应为 1: %+v", list.Items[0])
	}

	rec = e.callAnon(http.MethodGet, "/api/portal/links?media_type=movie", nil)
	decodeInto(t, rec, &list)
	if list.Total != 1 || list.Items[0].Title != "教父" {
		t.Errorf("电影筛选异常: %+v", list)
	}
	rec = e.callAnon(http.MethodGet, "/api/portal/links?media_type=tv", nil)
	decodeInto(t, rec, &list)
	if list.Total != 1 || list.Items[0].Title != "怪奇物语" {
		t.Errorf("剧集筛选异常: %+v", list)
	}
	requireStatus(t, e.callAnon(http.MethodGet, "/api/portal/links?media_type=bogus", nil), http.StatusBadRequest)

	rec = e.callAnon(http.MethodGet, "/api/portal/links?year=abc", nil)
	decodeInto(t, rec, &list)
	if list.Total != 2 {
		t.Errorf("非法年份应忽略，实际 %+v", list)
	}
	rec = e.callAnon(http.MethodGet, "/api/portal/links?year=1972", nil)
	decodeInto(t, rec, &list)
	if list.Total != 1 {
		t.Errorf("年份筛选异常: %+v", list)
	}
	rec = e.callAnon(http.MethodGet, "/api/portal/links?sort=rating", nil)
	decodeInto(t, rec, &list)
	if list.Items[0].ID != movie.ID {
		t.Errorf("评分排序异常: %+v", list)
	}
	requireStatus(t, e.callAnon(http.MethodGet, "/api/portal/links?limit=999&offset=99999", nil), http.StatusOK)

	rec = e.callAnon(http.MethodGet, "/api/portal/stats", nil)
	requireStatus(t, rec, http.StatusOK)
	var stats model.PublicStats
	decodeInto(t, rec, &stats)
	if stats.Total != 2 || stats.Movies != 1 || stats.TV != 1 || stats.LibraryTotal != 2 {
		t.Errorf("门户统计异常: %+v", stats)
	}
	if len(stats.Years) != 2 {
		t.Errorf("年份选项数量异常: %+v", stats.Years)
	}
	requireStatus(t, e.callAnon(http.MethodGet, "/api/portal/stats?media_type=bogus", nil), http.StatusBadRequest)

	// 分类 / 年份 / 关键词筛选下的统计：这里曾因 CTE 缺列返回 500，前端把分类计数回落成 0
	rec = e.callAnon(http.MethodGet, "/api/portal/stats?media_type=movie", nil)
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &stats)
	if stats.Total != 2 || stats.Movies != 1 || stats.TV != 1 ||
		len(stats.Years) != 1 || stats.Years[0].Year != 1972 || stats.Years[0].Count != 1 {
		t.Errorf("按分类统计异常: %+v", stats)
	}
	rec = e.callAnon(http.MethodGet, "/api/portal/stats?year=1972", nil)
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &stats)
	if stats.Total != 1 || stats.Movies != 1 || stats.TV != 0 || len(stats.Years) != 2 {
		t.Errorf("按年份统计异常: %+v", stats)
	}
	rec = e.callAnon(http.MethodGet, "/api/portal/stats?q=%E6%95%99%E7%88%B6", nil)
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &stats)
	if stats.Total != 1 {
		t.Errorf("按关键词统计异常: %+v", stats)
	}

	rec = e.callAnon(http.MethodGet, "/api/portal/link/"+idPath(movie.ID), nil)
	requireStatus(t, rec, http.StatusOK)
	var item model.PublicLink
	decodeInto(t, rec, &item)
	if item.Title != "教父" {
		t.Errorf("门户详情异常: %+v", item)
	}
	if item.LinkCount != 1 || len(item.Links) != 1 {
		t.Errorf("详情应带出链接列表: %+v", item)
	}
	requireStatus(t, e.callAnon(http.MethodGet, "/api/portal/link/99999", nil), http.StatusNotFound)
	requireStatus(t, e.callAnon(http.MethodGet, "/api/portal/link/abc", nil), http.StatusBadRequest)

	rec = e.callAnon(http.MethodGet, "/api/structured-data/"+idPath(movie.ID), nil)
	requireStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"@type":"Movie"`) {
		t.Errorf("电影结构化数据异常: %s", rec.Body.String())
	}
	rec = e.callAnon(http.MethodGet, "/api/structured-data/"+idPath(tv.ID), nil)
	requireStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"@type":"TVSeries"`) {
		t.Errorf("剧集结构化数据异常: %s", rec.Body.String())
	}
	requireStatus(t, e.callAnon(http.MethodGet, "/api/structured-data/99999", nil), http.StatusNotFound)
	requireStatus(t, e.callAnon(http.MethodGet, "/api/structured-data/abc", nil), http.StatusBadRequest)
	// 未推送的记录对外不可见
	requireStatus(t, e.callAnon(http.MethodGet, "/api/structured-data/"+idPath(e.seedLink("structured-draft", "草稿").ID), nil), http.StatusNotFound)

	rec = e.callAnon(http.MethodGet, "/sitemap.xml", nil)
	requireStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "/?id="+idPath(movie.ID)) {
		t.Errorf("站点地图未收录详情页: %s", rec.Body.String())
	}
}

func TestPipelineRoutes(t *testing.T) {
	e := newAPIEnv(t)

	rec := e.call(http.MethodPost, "/api/scrape", map[string]any{})
	requireStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "没有符合状态的记录") {
		t.Errorf("空队列提示异常: %s", rec.Body.String())
	}
	requireStatus(t, e.call(http.MethodPost, "/api/push", map[string]any{}), http.StatusBadRequest)

	pending := e.seedLink("pipeline-pending", "待刮削")
	var queued struct {
		Queued int `json:"queued"`
	}
	rec = e.call(http.MethodPost, "/api/scrape", map[string]any{"all": true})
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &queued)
	if queued.Queued != 1 {
		t.Errorf("刮削入队数量异常: %+v", queued)
	}

	rec = e.call(http.MethodGet, "/api/tasks?kind=scrape", nil)
	requireStatus(t, rec, http.StatusOK)
	var progress pipeline.Progress
	decodeInto(t, rec, &progress)
	if !progress.Running || progress.Total != 1 {
		t.Errorf("刮削进度异常: %+v", progress)
	}
	requireStatus(t, e.call(http.MethodGet, "/api/tasks", nil), http.StatusOK)

	rec = e.call(http.MethodPost, "/api/tasks/cancel", map[string]string{"kind": "scrape"})
	requireStatus(t, rec, http.StatusOK)
	var canceled struct {
		Canceled int `json:"canceled"`
	}
	decodeInto(t, rec, &canceled)
	if canceled.Canceled != 1 {
		t.Errorf("取消数量异常: %+v", canceled)
	}

	requireStatus(t, e.call(http.MethodPost, "/api/links/"+idPath(pending.ID)+"/scrape", nil), http.StatusOK)
	requireStatus(t, e.call(http.MethodPost, "/api/links/abc/scrape", nil), http.StatusBadRequest)

	scraped := e.seedLink("pipeline-scraped", "已刮削")
	if err := e.st.UpdateScraped(scraped.ID, &model.Link{
		Title: "已刮削", TMDBID: 1, MediaType: model.MediaMovie,
	}); err != nil {
		t.Fatalf("写入刮削结果失败: %v", err)
	}
	rec = e.call(http.MethodPost, "/api/push", map[string]any{"scraped": true})
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &queued)
	if queued.Queued != 1 {
		t.Errorf("推送入队数量异常: %+v", queued)
	}
	requireStatus(t, e.call(http.MethodPost, "/api/links/"+idPath(scraped.ID)+"/push", nil), http.StatusOK)

	// 未配置 TMDB Key 时预览接口直接回 400
	requireStatus(t, e.call(http.MethodGet, "/api/tmdb/lookup?query=x", nil), http.StatusBadRequest)
	requireStatus(t, e.call(http.MethodPost, "/api/tmdb/lookup", map[string]any{}), http.StatusBadRequest)

	requireStatus(t, e.call(http.MethodPost, "/api/links/"+idPath(pending.ID)+"/tmdb", map[string]any{}), http.StatusBadRequest)
	rec = e.call(http.MethodPost, "/api/links/"+idPath(pending.ID)+"/tmdb", map[string]any{
		"tmdb_id": 550, "media_type": "tv", "scrape": true,
	})
	requireStatus(t, rec, http.StatusOK)
	got, err := e.st.Get(pending.ID)
	if err != nil || got == nil {
		t.Fatalf("读取记录失败: %v", err)
	}
	if got.TMDBID != 550 || got.MediaType != model.MediaTV || got.Status != model.StatusPending {
		t.Errorf("手动指定 TMDB 结果异常: %+v", got)
	}

	// 非法类型按电影落库
	rec = e.call(http.MethodPost, "/api/links/"+idPath(pending.ID)+"/tmdb", map[string]any{
		"tmdb_id": 551, "media_type": "bogus",
	})
	requireStatus(t, rec, http.StatusOK)
	got, err = e.st.Get(pending.ID)
	if err != nil || got == nil {
		t.Fatalf("读取记录失败: %v", err)
	}
	if got.MediaType != model.MediaMovie {
		t.Errorf("未知类型应回退 movie: %+v", got)
	}
}

// pickIDs 的三种取数方式：显式 ids、显式 statuses、all + 空白 statuses 回落 pending。
func TestScrapePickBranches(t *testing.T) {
	e := newAPIEnv(t)
	pending := e.seedLink("pick-pending", "待刮削")
	failed := e.seedLink("pick-failed", "失败")
	if err := e.st.UpdateStatus(failed.ID, model.StatusFailed, "boom"); err != nil {
		t.Fatalf("写入失败状态: %v", err)
	}
	var queued struct {
		Queued int `json:"queued"`
	}

	rec := e.call(http.MethodPost, "/api/scrape", map[string]any{"statuses": []string{" failed "}})
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &queued)
	if queued.Queued != 1 {
		t.Errorf("按状态入队数量异常: %+v", queued)
	}

	rec = e.call(http.MethodPost, "/api/scrape", map[string]any{"ids": []int64{pending.ID, failed.ID}})
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &queued)
	if queued.Queued != 2 {
		t.Errorf("按 ids 入队数量异常: %+v", queued)
	}

	// all=true 且 statuses 全是空白 → 回落 pending
	rec = e.call(http.MethodPost, "/api/scrape", map[string]any{"all": true, "statuses": []string{"  "}})
	requireStatus(t, rec, http.StatusOK)
	decodeInto(t, rec, &queued)
	if queued.Queued != 1 {
		t.Errorf("空白状态应回落 pending: %+v", queued)
	}
}

// 数据库故障时接口统一回 500 且不泄露内部细节。
func TestStoreFailureRoutes(t *testing.T) {
	e := newAPIEnv(t)
	if err := e.st.Close(); err != nil {
		t.Fatalf("关闭数据库: %v", err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		token  string
	}{
		{"安装状态", http.MethodGet, "/api/setup/status", nil, ""},
		{"登录", http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": "Abcdef12!"}, ""},
		{"当前账号", http.MethodGet, "/api/auth/me", nil, e.token},
		{"账号列表", http.MethodGet, "/api/users", nil, e.token},
		{"链接列表", http.MethodGet, "/api/links", nil, e.token},
		{"链接详情", http.MethodGet, "/api/links/1", nil, e.token},
		{"文案预览", http.MethodGet, "/api/links/1/caption", nil, e.token},
		{"删除链接", http.MethodDelete, "/api/links/1", nil, e.token},
		{"导出", http.MethodGet, "/api/links/export", nil, e.token},
		{"统计", http.MethodGet, "/api/stats", nil, e.token},
		{"门户列表", http.MethodGet, "/api/portal/links", nil, ""},
		{"门户统计", http.MethodGet, "/api/portal/stats", nil, ""},
		{"门户详情", http.MethodGet, "/api/portal/link/1", nil, ""},
		{"条目结构化数据", http.MethodGet, "/api/structured-data/1", nil, ""},
		{"站点地图", http.MethodGet, "/sitemap.xml", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.callAs(c.method, c.path, c.body, c.token)
			requireStatus(t, rec, http.StatusInternalServerError)
			if !strings.Contains(rec.Body.String(), "error") {
				t.Errorf("应返回错误提示: %s", rec.Body.String())
			}
		})
	}
}
