package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/model"
)

// visitIndex 走一次门户首页渲染，返回下发的访客 Cookie（如有）。
func visitIndex(t *testing.T, env *apiEnv, target string, cookie string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	if cookie != "" {
		c.Request.AddCookie(&http.Cookie{Name: visitorCookieName, Value: cookie})
	}
	env.srv.WriteIndex(c, testIndexFS(""))
	requireStatus(t, rec, http.StatusOK)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == visitorCookieName {
			return ck
		}
	}
	return nil
}

func TestPortalTraffic_RecordsVisitsAndOpens(t *testing.T) {
	env := newAPIEnv(t)

	first := visitIndex(t, env, "/", "")
	if first == nil || first.Value == "" {
		t.Fatal("首次访问应下发访客 Cookie")
	}
	second := visitIndex(t, env, "/", first.Value)
	if second != nil {
		t.Error("带已有 Cookie 的访问不应重复下发")
	}
	// 后台入口不计入门户 PV
	visitIndex(t, env, "/admin", first.Value)

	link := env.seedPublishedLink("stats-1", &model.Link{
		Title: "千与千寻", Year: 2001, MediaType: model.MediaMovie, TMDBID: 129,
	})
	rec := env.callAnon(http.MethodPost, "/api/portal/open/"+idPath(link.ID), nil)
	requireStatus(t, rec, http.StatusNoContent)
	// 详情 GET 只读，不应再增加打开次数
	rec = env.callAnon(http.MethodGet, "/api/portal/link/"+idPath(link.ID), nil)
	requireStatus(t, rec, http.StatusOK)

	rec = env.call(http.MethodGet, "/api/stats/portal?days=7", nil)
	requireStatus(t, rec, http.StatusOK)
	var got model.PortalTraffic
	decodeInto(t, rec, &got)
	if got.Days != 7 || len(got.Trend) != 7 {
		t.Errorf("days/趋势长度 = %d/%d, 期望 7/7", got.Days, len(got.Trend))
	}
	if got.TodayPV != 2 || got.TodayUV != 1 {
		t.Errorf("今日 PV/UV = %d/%d, 期望 2/1", got.TodayPV, got.TodayUV)
	}
	if got.TotalOpens != 1 || got.RangeOpens != 1 {
		t.Errorf("打开次数 = 总 %d / 区间 %d, 期望 1/1", got.TotalOpens, got.RangeOpens)
	}
	if len(got.Top) != 1 || got.Top[0].ID != link.ID || got.Top[0].Opens != 1 {
		t.Errorf("热门资源 = %+v, 期望命中本次打开的资源", got.Top)
	}
}

func TestPortalTraffic_DaysFallback(t *testing.T) {
	env := newAPIEnv(t)
	for _, q := range []string{"", "?days=5", "?days=abc", "?days=0"} {
		rec := env.call(http.MethodGet, "/api/stats/portal"+q, nil)
		requireStatus(t, rec, http.StatusOK)
		var got model.PortalTraffic
		decodeInto(t, rec, &got)
		if got.Days != 30 {
			t.Errorf("days=%q 时窗口 = %d, 期望 30", q, got.Days)
		}
	}
}

func TestPortalTraffic_RequiresAdmin(t *testing.T) {
	env := newAPIEnv(t)
	rec := env.callAnon(http.MethodGet, "/api/stats/portal", nil)
	requireStatus(t, rec, http.StatusUnauthorized)

	op := env.createUser("op", "Abcdef12!", model.RoleOperator)
	rec = env.callAs(http.MethodGet, "/api/stats/portal", nil, env.newSession(op.ID))
	requireStatus(t, rec, http.StatusForbidden)
}

// TestPortalOpen_ReportsClick 覆盖按分享计数：同一条资源的多条分享各记各的，
// 热门榜单按 TMDB 分组汇总。
func TestPortalOpen_ReportsClick(t *testing.T) {
	env := newAPIEnv(t)
	old := env.seedPublishedLink("open-report-old", &model.Link{
		Title: "点击上报", MediaType: model.MediaMovie, TMDBID: 4242, SharedAt: "2026-10-01",
	})
	latest := env.seedPublishedLink("open-report-new", &model.Link{
		Title: "点击上报", MediaType: model.MediaMovie, TMDBID: 4242, SharedAt: "2026-10-20",
	})

	post := func(action string, target int64) *httptest.ResponseRecorder {
		rec := env.callAnon(http.MethodPost, "/api/portal/"+action+"/"+idPath(target), nil)
		requireStatus(t, rec, http.StatusNoContent)
		return rec
	}
	for _, target := range []int64{old.ID, latest.ID, latest.ID} {
		post("open", target)
	}
	// 浏览海报与复制分享各记各的口径，不能混进打开次数
	post("browse", old.ID)
	post("browse", latest.ID)
	post("copy", latest.ID)

	rec := env.call(http.MethodGet, "/api/stats/portal?days=7", nil)
	requireStatus(t, rec, http.StatusOK)
	var got model.PortalTraffic
	decodeInto(t, rec, &got)
	if got.TotalOpens != 3 || got.RangeOpens != 3 {
		t.Errorf("打开次数 = 总 %d / 区间 %d, 期望 3/3", got.TotalOpens, got.RangeOpens)
	}
	if got.TotalBrowses != 2 || got.RangeBrowses != 2 {
		t.Errorf("浏览次数 = 总 %d / 区间 %d, 期望 2/2", got.TotalBrowses, got.RangeBrowses)
	}
	if got.TotalCopies != 1 || got.RangeCopies != 1 {
		t.Errorf("复制次数 = 总 %d / 区间 %d, 期望 1/1", got.TotalCopies, got.RangeCopies)
	}
	// 代表行取最近分享的一条，三类互动都按分组汇总
	if len(got.Top) != 1 || got.Top[0].ID != latest.ID ||
		got.Top[0].Browses != 2 || got.Top[0].Copies != 1 || got.Top[0].Opens != 3 {
		t.Errorf("热门资源 = %+v, 期望按 TMDB 汇总到 %d: 浏 2 复 1 开 3", got.Top, latest.ID)
	}

	// 后台详情按分享返回各自的累计次数
	rec = env.call(http.MethodGet, "/api/stats/portal/link/"+idPath(old.ID), nil)
	requireStatus(t, rec, http.StatusOK)
	var detail model.PublicLink
	decodeInto(t, rec, &detail)
	if len(detail.Links) != 2 {
		t.Fatalf("分享条数 = %d, 期望 2", len(detail.Links))
	}
	stats := map[int64]model.ShareStats{}
	for _, s := range detail.Links {
		stats[s.ID] = model.ShareStats{Browses: s.Browses, Copies: s.Copies, Opens: s.Opens}
	}
	if stats[old.ID] != (model.ShareStats{Browses: 1, Opens: 1}) {
		t.Errorf("旧分享计数 = %+v, 期望 浏 1 开 1", stats[old.ID])
	}
	if stats[latest.ID] != (model.ShareStats{Browses: 1, Copies: 1, Opens: 2}) {
		t.Errorf("新分享计数 = %+v, 期望 浏 1 复 1 开 2", stats[latest.ID])
	}

	rec = env.call(http.MethodGet, "/api/stats/portal/link/999999", nil)
	requireStatus(t, rec, http.StatusNotFound)
	for _, action := range []string{"browse", "copy", "open"} {
		rec = env.callAnon(http.MethodPost, "/api/portal/"+action+"/999999", nil)
		requireStatus(t, rec, http.StatusNotFound)
		rec = env.callAnon(http.MethodPost, "/api/portal/"+action+"/abc", nil)
		requireStatus(t, rec, http.StatusBadRequest)
	}
}

func TestPortalTrafficLink_RequiresAdmin(t *testing.T) {
	env := newAPIEnv(t)
	link := env.seedPublishedLink("detail-admin", &model.Link{
		Title: "详情鉴权", MediaType: model.MediaMovie, TMDBID: 77,
	})
	rec := env.callAnon(http.MethodGet, "/api/stats/portal/link/"+idPath(link.ID), nil)
	requireStatus(t, rec, http.StatusUnauthorized)

	op := env.createUser("op", "Abcdef12!", model.RoleOperator)
	rec = env.callAs(http.MethodGet, "/api/stats/portal/link/"+idPath(link.ID), nil, env.newSession(op.ID))
	requireStatus(t, rec, http.StatusForbidden)
}

func TestPortalTraffic_DisabledStopsRecording(t *testing.T) {
	env := newAPIEnv(t)
	env.srv.cfg.Stats.Enabled = false

	visitIndex(t, env, "/", "")
	link := env.seedPublishedLink("stats-off", &model.Link{Title: "关闭统计", MediaType: model.MediaMovie})
	for _, action := range []string{"browse", "copy", "open"} {
		rec := env.callAnon(http.MethodPost, "/api/portal/"+action+"/"+idPath(link.ID), nil)
		requireStatus(t, rec, http.StatusNoContent)
	}
	rec := env.callAnon(http.MethodGet, "/api/portal/link/"+idPath(link.ID), nil)
	requireStatus(t, rec, http.StatusOK)

	rec = env.call(http.MethodGet, "/api/stats/portal", nil)
	requireStatus(t, rec, http.StatusOK)
	var got model.PortalTraffic
	decodeInto(t, rec, &got)
	if got.TodayPV != 0 || got.TotalBrowses != 0 || got.TotalCopies != 0 || got.TotalOpens != 0 {
		t.Errorf("关闭统计后仍记录: PV=%d 浏=%d 复=%d 开=%d", got.TodayPV, got.TotalBrowses, got.TotalCopies, got.TotalOpens)
	}
}
