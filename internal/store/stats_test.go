package store

import (
	"testing"
	"time"

	"github.com/odin/guangya-go/internal/model"
)

func statsDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.Local)
}

func TestPortalTraffic_VisitUpsertAndUV(t *testing.T) {
	s := newTestStore(t)
	today := statsDay(2026, 10, 31)
	day := DayKey(today)

	for _, visitor := range []string{"a", "a", "b"} {
		if err := s.RecordVisit(day, visitor); err != nil {
			t.Fatalf("RecordVisit: %v", err)
		}
	}

	got, err := s.PortalTraffic(7, today)
	if err != nil {
		t.Fatalf("PortalTraffic: %v", err)
	}
	if got.TodayPV != 3 {
		t.Errorf("今日 PV = %d, 期望 3", got.TodayPV)
	}
	if got.TodayUV != 2 {
		t.Errorf("今日 UV = %d, 期望 2", got.TodayUV)
	}
	if got.RangePV != 3 || got.RangeUV != 2 {
		t.Errorf("区间 PV/UV = %d/%d, 期望 3/2", got.RangePV, got.RangeUV)
	}
	if len(got.Trend) != 7 {
		t.Fatalf("趋势长度 = %d, 期望 7", len(got.Trend))
	}
	last := got.Trend[len(got.Trend)-1]
	if last.Date != day || last.PV != 3 || last.UV != 2 {
		t.Errorf("趋势最后一天 = %+v, 期望 %s PV3 UV2", last, day)
	}
}

func TestPortalTraffic_RangeUVIsDistinct(t *testing.T) {
	s := newTestStore(t)
	day1 := statsDay(2026, 10, 29)
	day2 := statsDay(2026, 10, 30)
	if err := s.RecordVisit(DayKey(day1), "same"); err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}
	if err := s.RecordVisit(DayKey(day2), "same"); err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}
	got, err := s.PortalTraffic(7, day2)
	if err != nil {
		t.Fatalf("PortalTraffic: %v", err)
	}
	if got.RangePV != 2 {
		t.Errorf("区间 PV = %d, 期望 2", got.RangePV)
	}
	if got.RangeUV != 1 {
		t.Errorf("区间 UV = %d, 期望 1（同一访客跨天去重）", got.RangeUV)
	}
}

func TestPortalTraffic_TopAndDeletedLink(t *testing.T) {
	s := newTestStore(t)
	a := &model.Link{ShareID: "a", URL: "https://x/a", Title: "甲", Year: 2024, Status: model.StatusPending}
	b := &model.Link{ShareID: "b", URL: "https://x/b", Title: "乙", Year: 2025, Status: model.StatusPending}
	mustCreateLink(t, s, a)
	mustCreateLink(t, s, b)

	day := DayKey(statsDay(2026, 10, 31))
	for i := 0; i < 2; i++ {
		if err := s.RecordAction(a.ID, day, model.ActionOpen); err != nil {
			t.Fatalf("RecordAction: %v", err)
		}
	}
	for i := 0; i < 5; i++ {
		if err := s.RecordAction(b.ID, day, model.ActionOpen); err != nil {
			t.Fatalf("RecordAction: %v", err)
		}
	}

	got, err := s.PortalTraffic(7, statsDay(2026, 10, 31))
	if err != nil {
		t.Fatalf("PortalTraffic: %v", err)
	}
	if got.TotalOpens != 7 || got.RangeOpens != 7 {
		t.Errorf("累计/区间打开 = %d/%d, 期望 7/7", got.TotalOpens, got.RangeOpens)
	}
	if len(got.Top) != 2 || got.Top[0].ID != b.ID || got.Top[0].Opens != 5 {
		t.Fatalf("排行不符合预期: %+v", got.Top)
	}

	// 资源删除后：排行里消失，但累计打开数保留
	if err := s.Delete(b.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err = s.PortalTraffic(7, statsDay(2026, 10, 31))
	if err != nil {
		t.Fatalf("PortalTraffic: %v", err)
	}
	if len(got.Top) != 1 || got.Top[0].ID != a.ID {
		t.Errorf("删除后排行 = %+v, 期望只剩甲", got.Top)
	}
	if got.TotalOpens != 7 {
		t.Errorf("删除后累计打开 = %d, 期望保留 7", got.TotalOpens)
	}
}

// TestPortalTraffic_TopGroupsSharesByTMDB 覆盖同一条资源的多条分享：
// 三类互动分开统计，排行按 TMDB 分组求和，代表行取最近分享的一条。
func TestPortalTraffic_TopGroupsSharesByTMDB(t *testing.T) {
	s := newTestStore(t)
	old := &model.Link{ShareID: "g-old", URL: "https://x/o", Title: "同一部", SharedAt: "2026-10-01", Status: model.StatusPending}
	latest := &model.Link{ShareID: "g-new", URL: "https://x/n", Title: "同一部", SharedAt: "2026-10-20", Status: model.StatusPending}
	mustCreateLink(t, s, old)
	mustCreateLink(t, s, latest)
	scraped := &model.Link{Title: "同一部", Year: 2020, MediaType: model.MediaMovie, TMDBID: 500}
	for _, id := range []int64{old.ID, latest.ID} {
		if err := s.UpdateScraped(id, scraped); err != nil {
			t.Fatalf("UpdateScraped(%d): %v", id, err)
		}
	}

	day := DayKey(statsDay(2026, 10, 31))
	for _, id := range []int64{old.ID, latest.ID, latest.ID} {
		if err := s.RecordAction(id, day, model.ActionOpen); err != nil {
			t.Fatalf("RecordAction(%d): %v", id, err)
		}
	}
	for i := 0; i < 4; i++ {
		if err := s.RecordAction(latest.ID, day, model.ActionBrowse); err != nil {
			t.Fatalf("RecordAction browse: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := s.RecordAction(old.ID, day, model.ActionCopy); err != nil {
			t.Fatalf("RecordAction copy: %v", err)
		}
	}

	got, err := s.PortalTraffic(7, statsDay(2026, 10, 31))
	if err != nil {
		t.Fatalf("PortalTraffic: %v", err)
	}
	if got.TotalBrowses != 4 || got.TotalCopies != 2 || got.TotalOpens != 3 {
		t.Errorf("累计浏览/复制/打开 = %d/%d/%d, 期望 4/2/3",
			got.TotalBrowses, got.TotalCopies, got.TotalOpens)
	}
	if got.RangeBrowses != 4 || got.RangeCopies != 2 || got.RangeOpens != 3 {
		t.Errorf("区间浏览/复制/打开 = %d/%d/%d, 期望 4/2/3",
			got.RangeBrowses, got.RangeCopies, got.RangeOpens)
	}
	if len(got.Top) != 1 || got.Top[0].ID != latest.ID ||
		got.Top[0].Browses != 4 || got.Top[0].Copies != 2 || got.Top[0].Opens != 3 {
		t.Errorf("排行 = %+v, 期望按 TMDB 汇总到条目 %d", got.Top, latest.ID)
	}

	stats, err := s.ShareActions([]int64{old.ID, latest.ID, 999999})
	if err != nil {
		t.Fatalf("ShareActions: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("分享数 = %d, 期望 2", len(stats))
	}
	if got := stats[old.ID]; got.Browses != 0 || got.Copies != 2 || got.Opens != 1 {
		t.Errorf("旧分享统计 = %+v, 期望 浏览0 复制2 打开1", got)
	}
	if got := stats[latest.ID]; got.Browses != 4 || got.Copies != 0 || got.Opens != 2 {
		t.Errorf("新分享统计 = %+v, 期望 浏览4 复制0 打开2", got)
	}
}

func TestPortalTraffic_EmptyReturnsFullShape(t *testing.T) {
	s := newTestStore(t)
	got, err := s.PortalTraffic(30, statsDay(2026, 10, 31))
	if err != nil {
		t.Fatalf("PortalTraffic: %v", err)
	}
	if got.Trend == nil || len(got.Trend) != 30 {
		t.Errorf("空数据趋势长度 = %d, 期望 30", len(got.Trend))
	}
	if got.Top == nil {
		t.Error("空数据 Top 应为空数组而不是 nil")
	}
	if got.Trend[0].Date != "2026-10-02" || got.Trend[29].Date != "2026-10-31" {
		t.Errorf("趋势日期范围 = %s..%s", got.Trend[0].Date, got.Trend[29].Date)
	}
}

func TestPurgeTraffic(t *testing.T) {
	s := newTestStore(t)
	old := DayKey(statsDay(2026, 1, 1))
	recent := DayKey(statsDay(2026, 10, 30))
	for _, day := range []string{old, recent} {
		if err := s.RecordVisit(day, "v"); err != nil {
			t.Fatalf("RecordVisit: %v", err)
		}
		if err := s.RecordAction(1, day, model.ActionOpen); err != nil {
			t.Fatalf("RecordAction: %v", err)
		}
	}

	n, err := s.PurgeTraffic(recent)
	if err != nil {
		t.Fatalf("PurgeTraffic: %v", err)
	}
	if n != 2 {
		t.Errorf("删除行数 = %d, 期望 2", n)
	}
	got, err := s.PortalTraffic(7, statsDay(2026, 10, 30))
	if err != nil {
		t.Fatalf("PortalTraffic: %v", err)
	}
	if got.RangePV != 1 || got.RangeOpens != 1 {
		t.Errorf("清理后区间 = PV%d 打开%d, 期望各 1", got.RangePV, got.RangeOpens)
	}
}
