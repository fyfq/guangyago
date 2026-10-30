package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/notifier"
	"github.com/odin/guangya-go/internal/scraper"
	"github.com/odin/guangya-go/internal/store"
)

func newTestPipeline(t *testing.T) (*Pipeline, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p := New(st, nil, nil)
	t.Cleanup(p.Stop)
	return p, st
}

func seedLink(t *testing.T, st *store.Store, shareID string, status model.Status) *model.Link {
	t.Helper()
	l := &model.Link{ShareID: shareID, URL: "https://guangyapan.com/s/" + shareID + "_x", Status: status}
	ok, err := st.Create(l)
	if err != nil || !ok {
		t.Fatalf("Create(%q): ok=%v err=%v", shareID, ok, err)
	}
	return l
}

func waitDone(t *testing.T, p *Pipeline, kind string) Progress {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pr := p.StatusOf(kind)
		if !pr.Running {
			return pr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s 任务未在超时内结束: %+v", kind, p.StatusOf(kind))
	return Progress{}
}

func TestEnqueue_EmptyAndStopped(t *testing.T) {
	p, _ := newTestPipeline(t)
	if n := p.Enqueue("scrape", nil); n != 0 {
		t.Errorf("空 ID 列表应返回 0，实际 %d", n)
	}
	p.Stop()
	if n := p.Enqueue("scrape", []int64{1}); n != 0 {
		t.Errorf("关停后 Enqueue 应返回 0，实际 %d", n)
	}
}

func TestScrape_NoScraperMarksFailed(t *testing.T) {
	p, st := newTestPipeline(t)
	l := seedLink(t, st, "s1", model.StatusPending)
	p.Start(2)

	if n := p.Enqueue("scrape", []int64{l.ID}); n != 1 {
		t.Fatalf("排队数量 = %d，期望 1", n)
	}
	pr := waitDone(t, p, "scrape")
	if pr.Done != 0 || pr.Failed != 1 {
		t.Errorf("未配置刮削器应计为失败: %+v", pr)
	}
	if pr.Error == "" {
		t.Error("失败进度应带错误信息")
	}

	got, err := st.Get(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusFailed || got.Error == "" {
		t.Errorf("记录状态应更新为 failed: %+v", got)
	}
}

func TestPush_NoNotifierMarksFailed(t *testing.T) {
	p, st := newTestPipeline(t)
	l := seedLink(t, st, "p1", model.StatusScraped)
	p.Start(1)

	if n := p.Enqueue("push", []int64{l.ID}); n != 1 {
		t.Fatalf("排队数量 = %d", n)
	}
	pr := waitDone(t, p, "push")
	if pr.Failed != 1 {
		t.Errorf("未配置推送器应计为失败: %+v", pr)
	}

	got, _ := st.Get(l.ID)
	if got.Status != model.StatusPushFailed {
		t.Errorf("状态应为 push_failed，实际 %q", got.Status)
	}
}

func TestHandle_MissingLink(t *testing.T) {
	p, _ := newTestPipeline(t)
	p.Start(1)

	if n := p.Enqueue("scrape", []int64{999}); n != 1 {
		t.Fatalf("排队数量 = %d", n)
	}
	pr := waitDone(t, p, "scrape")
	if pr.Failed != 1 || pr.Error != "记录不存在" {
		t.Errorf("缺失记录应计为失败: %+v", pr)
	}
}

func TestCancelAndReenqueue(t *testing.T) {
	p, st := newTestPipeline(t)
	// 不启动 worker，任务会一直排队，便于观察批次状态
	l := seedLink(t, st, "c1", model.StatusPending)
	if n := p.Enqueue("scrape", []int64{l.ID}); n != 1 {
		t.Fatalf("排队数量 = %d", n)
	}
	if pr := p.StatusOf("scrape"); !pr.Running {
		t.Errorf("刚入队应为运行中: %+v", pr)
	}
	if n := p.Cancel("scrape"); n != 1 {
		t.Errorf("Cancel 应取消 1 个批次，实际 %d", n)
	}
	pr := p.StatusOf("scrape")
	if pr.Running || pr.Current != "已取消" {
		t.Errorf("取消后状态异常: %+v", pr)
	}
	// 已取消的批次重复取消不应计数
	if n := p.Cancel("scrape"); n != 0 {
		t.Errorf("重复取消应返回 0，实际 %d", n)
	}

	// 重新入队会开新批次
	if n := p.Enqueue("scrape", []int64{l.ID}); n != 1 {
		t.Fatal("重新入队失败")
	}
	if pr := p.StatusOf("scrape"); !pr.Running {
		t.Errorf("新批次应为运行中: %+v", pr)
	}

	// 按类型取消只影响指定类型
	p.Cancel("")
	if pr := p.StatusOf("scrape"); pr.Running {
		t.Errorf("Cancel(\"\") 应取消全部: %+v", pr)
	}
}

func TestStatus_PrefersRunningAndNewest(t *testing.T) {
	p, st := newTestPipeline(t)
	if pr := p.Status(); pr.Running || pr.Total != 0 {
		t.Errorf("没有任务时应返回零值: %+v", pr)
	}
	if pr := p.StatusOf("unknown"); pr.Total != 0 {
		t.Errorf("未知类型应返回零值: %+v", pr)
	}

	l := seedLink(t, st, "st1", model.StatusPending)
	p.Enqueue("scrape", []int64{l.ID})
	// 再入队另一类任务，正在运行的那个应被优先展示
	p.Cancel("scrape")
	p.Enqueue("push", []int64{l.ID})

	pr := p.Status()
	if !pr.Running || pr.Kind == "" {
		t.Errorf("Status 应返回运行中的批次: %+v", pr)
	}
}

func TestStart_Idempotent(t *testing.T) {
	p, _ := newTestPipeline(t)
	p.Start(0) // workers<=0 应回落为 1
	p.Start(3) // 重复调用不应重复起 goroutine
	p.Stop()
	p.Stop() // 幂等
}

func TestGuangyaLimit(t *testing.T) {
	p, _ := newTestPipeline(t)

	// 默认 2 并发：占满两个名额后，第三次获取应阻塞
	release1, err := p.GuangyaGuard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release2, err := p.GuangyaGuard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.GuangyaGuard(ctx); err == nil {
		t.Error("名额占满时应阻塞到 ctx 取消")
	}
	release1()
	release2()

	// 调整为 1 并发后仍可正常获取
	p.SetGuangyaLimit(1)
	release, err := p.GuangyaGuard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()

	// 0 表示不限并发
	p.SetGuangyaLimit(0)
	r, err := p.GuangyaGuard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r()
}

func TestScraperAccessorAndComponents(t *testing.T) {
	p, _ := newTestPipeline(t)
	if p.Scraper() != nil {
		t.Error("未配置时应返回 nil")
	}
	p.UpdateComponents(nil, nil)
	if sc, nt := p.components(); sc != nil || nt != nil {
		t.Error("热更新后组件应为 nil")
	}
}

func TestScrapedBefore(t *testing.T) {
	for _, s := range []model.Status{model.StatusScraped, model.StatusPushed, model.StatusPushFailed} {
		if !scrapedBefore(s) {
			t.Errorf("%q 应视为已刮削", s)
		}
	}
	for _, s := range []model.Status{model.StatusPending, model.StatusFailed, model.StatusReview} {
		if scrapedBefore(s) {
			t.Errorf("%q 不应视为已刮削", s)
		}
	}
}

// fakeTMDBServer 提供最小的 TMDB：按 ID 取详情 + multi 搜索，供刮削链路离线测试。
func fakeTMDBServer(t *testing.T) *httptest.Server {
	t.Helper()
	detail := func(id int) string {
		return fmt.Sprintf(`{"id":%d,"title":"Fight Club","original_title":"Fight Club",`+
			`"release_date":"1999-10-15","overview":"x","vote_average":8.4,"runtime":139,`+
			`"genres":[{"name":"Drama"}]}`, id)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/3/movie/550":
			_, _ = w.Write([]byte(detail(550)))
		case "/3/movie/900":
			_, _ = w.Write([]byte(detail(900)))
		case "/3/search/multi":
			_, _ = w.Write([]byte(`{"results":[{"id":900,"media_type":"movie","title":"Fight Club",` +
				`"original_title":"Fight Club","release_date":"1999-10-15"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newScrapePipeline(t *testing.T, srv *httptest.Server) (*Pipeline, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sc, err := scraper.NewAtAPIBase(
		config.TMDBConfig{APIKey: "test-key", Language: "zh-CN"},
		t.TempDir(), nil, &config.TMDBRate{Enable: false}, srv.URL+"/3")
	if err != nil {
		t.Fatalf("构造刮削器: %v", err)
	}
	p := New(st, sc, nil)
	t.Cleanup(p.Stop)
	return p, st
}

// 手动指定 / 已落库的 TMDB ID 视为已确认：标题对不上也应直接刮削成功。
func TestScrape_PinnedTMDBIDNotReviewed(t *testing.T) {
	srv := fakeTMDBServer(t)
	p, st := newScrapePipeline(t, srv)
	l := &model.Link{
		ShareID: "pinned", URL: "https://guangyapan.com/s/pinned_x",
		RawTitle: "流浪地球", Status: model.StatusPending,
	}
	if ok, err := st.Create(l); err != nil || !ok {
		t.Fatalf("Create: ok=%v err=%v", ok, err)
	}
	if err := st.SetTMDBID(l.ID, 550, model.MediaMovie); err != nil {
		t.Fatalf("指定 TMDB ID: %v", err)
	}

	p.Start(1)
	if n := p.Enqueue("scrape", []int64{l.ID}); n != 1 {
		t.Fatalf("排队数量 = %d", n)
	}
	if pr := waitDone(t, p, "scrape"); pr.Failed != 0 || pr.Done != 1 {
		t.Errorf("显式 ID 应一次成功: %+v", pr)
	}

	got, err := st.Get(l.ID)
	if err != nil || got == nil {
		t.Fatalf("读取记录: %v", err)
	}
	if got.Status != model.StatusScraped {
		t.Errorf("状态 = %s，期望 scraped（不应再次待核实）", got.Status)
	}
	if got.TMDBID != 550 || got.Title != "Fight Club" {
		t.Errorf("刮削结果未写入: %+v", got)
	}
}

// 搜索结果与原始标题严重不符时仍应转待核实，避免自动刮错。
func TestScrape_SearchMismatchStillReviews(t *testing.T) {
	srv := fakeTMDBServer(t)
	p, st := newScrapePipeline(t, srv)
	l := &model.Link{
		ShareID: "search", URL: "https://guangyapan.com/s/search_x",
		RawTitle: "完全无关的标题", Status: model.StatusPending,
	}
	if ok, err := st.Create(l); err != nil || !ok {
		t.Fatalf("Create: ok=%v err=%v", ok, err)
	}

	p.Start(1)
	if n := p.Enqueue("scrape", []int64{l.ID}); n != 1 {
		t.Fatalf("排队数量 = %d", n)
	}
	waitDone(t, p, "scrape")

	got, err := st.Get(l.ID)
	if err != nil || got == nil {
		t.Fatalf("读取记录: %v", err)
	}
	if got.Status != model.StatusReview {
		t.Errorf("低置信搜索应转待核实，实际 %s", got.Status)
	}
}

// 待核实记录里的 TMDB ID 来自自动搜索，尚未人工确认，重刮仍需复核。
func TestScrape_ReviewWithTMDBIDStaysReviewed(t *testing.T) {
	srv := fakeTMDBServer(t)
	p, st := newScrapePipeline(t, srv)
	l := &model.Link{
		ShareID: "review", URL: "https://guangyapan.com/s/review_x",
		RawTitle: "完全无关的标题", TMDBID: 550, Status: model.StatusReview,
	}
	if ok, err := st.Create(l); err != nil || !ok {
		t.Fatalf("Create: ok=%v err=%v", ok, err)
	}

	p.Start(1)
	if n := p.Enqueue("scrape", []int64{l.ID}); n != 1 {
		t.Fatalf("排队数量 = %d", n)
	}
	waitDone(t, p, "scrape")

	got, err := st.Get(l.ID)
	if err != nil || got == nil {
		t.Fatalf("读取记录: %v", err)
	}
	if got.Status != model.StatusReview {
		t.Errorf("未确认的 ID 重刮后仍应待核实，实际 %s", got.Status)
	}
}

// fakeTelegramServer 是离线 Bot API：构造时的 getMe 与推送的 sendMessage/sendPhoto 都返回成功。
func fakeTelegramServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"bot","username":"bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"), strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77,"date":1,"chat":{"id":-100,"type":"channel"}}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newAutoPushPipeline 在刮削流水线上挂一个假 Telegram，并打开自动推送。
func newAutoPushPipeline(t *testing.T, srv *httptest.Server) (*Pipeline, *store.Store) {
	t.Helper()
	p, st := newScrapePipeline(t, srv)
	tg := fakeTelegramServer(t)
	nt, err := notifier.NewAtServerURL(
		config.TelegramConfig{Token: "123:test", ChatID: "-1001"},
		nil, &config.TelegramRate{Enable: false}, tg.URL)
	if err != nil || nt == nil {
		t.Fatalf("构造推送器: %+v %v", nt, err)
	}
	p.UpdateComponents(p.Scraper(), nt)
	p.SetAutoPush(true)
	return p, st
}

func TestScrape_AutoPushAfterSuccess(t *testing.T) {
	p, st := newAutoPushPipeline(t, fakeTMDBServer(t))
	l := &model.Link{
		ShareID: "auto", URL: "https://guangyapan.com/s/auto_x",
		Title: "Fight Club", TMDBID: 550, Status: model.StatusPending,
	}
	if ok, err := st.Create(l); err != nil || !ok {
		t.Fatalf("Create: ok=%v err=%v", ok, err)
	}

	p.Start(1)
	if n := p.Enqueue("scrape", []int64{l.ID}); n != 1 {
		t.Fatalf("排队数量 = %d", n)
	}
	waitDone(t, p, "scrape")

	deadline := time.Now().Add(3 * time.Second)
	var got *model.Link
	for time.Now().Before(deadline) {
		got, _ = st.Get(l.ID)
		if got != nil && got.Status == model.StatusPushed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got == nil || got.Status != model.StatusPushed {
		t.Fatalf("刮削成功后应自动推送，实际 %+v", got)
	}
	if got.TGMessageID != 77 {
		t.Errorf("推送消息 ID 未回写: %+v", got)
	}
	if pr := p.StatusOf("push"); pr.Total != 1 || pr.Done != 1 {
		t.Errorf("推送进度异常: %+v", pr)
	}
}

func TestScrape_NoAutoPushWhenDisabled(t *testing.T) {
	p, st := newScrapePipeline(t, fakeTMDBServer(t))
	l := &model.Link{
		ShareID: "manual", URL: "https://guangyapan.com/s/manual_x",
		Title: "Fight Club", TMDBID: 550, Status: model.StatusPending,
	}
	if ok, err := st.Create(l); err != nil || !ok {
		t.Fatalf("Create: ok=%v err=%v", ok, err)
	}

	p.Start(1)
	p.Enqueue("scrape", []int64{l.ID})
	waitDone(t, p, "scrape")

	got, err := st.Get(l.ID)
	if err != nil || got == nil {
		t.Fatalf("读取记录: %v", err)
	}
	if got.Status != model.StatusScraped {
		t.Errorf("关闭自动推送后应停在已刮削，实际 %s", got.Status)
	}
	if pr := p.StatusOf("push"); pr.Total != 0 {
		t.Errorf("关闭自动推送不应产生推送任务: %+v", pr)
	}
}

func TestScrape_SkipsAutoPushWhenAlreadyPushed(t *testing.T) {
	p, st := newAutoPushPipeline(t, fakeTMDBServer(t))
	l := &model.Link{
		ShareID: "re-scrape", URL: "https://guangyapan.com/s/re-scrape_x",
		Title: "Fight Club", TMDBID: 550, Status: model.StatusPushed,
	}
	if ok, err := st.Create(l); err != nil || !ok {
		t.Fatalf("Create: ok=%v err=%v", ok, err)
	}

	p.Start(1)
	p.Enqueue("scrape", []int64{l.ID})
	waitDone(t, p, "scrape")

	got, err := st.Get(l.ID)
	if err != nil || got == nil {
		t.Fatalf("读取记录: %v", err)
	}
	if got.Status != model.StatusScraped {
		t.Errorf("重刮后应回到已刮削，实际 %s", got.Status)
	}
	if pr := p.StatusOf("push"); pr.Total != 0 {
		t.Errorf("已推送记录重刮不应重复推送: %+v", pr)
	}
}

// slowTMDBServer 让指定条目的详情请求变慢，用来模拟长时间批量刮削。
func slowTMDBServer(t *testing.T, delay time.Duration, paths ...string) *httptest.Server {
	t.Helper()
	base := fakeTMDBServer(t)
	slow := map[string]bool{}
	for _, p := range paths {
		slow[p] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slow[r.URL.Path] {
			time.Sleep(delay)
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitStatus(t *testing.T, st *store.Store, id int64, want model.Status) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		l, err := st.Get(id)
		if err == nil && l != nil && l.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("记录 %d 未在超时内进入 %s 状态", id, want)
}

// 刮削与推送各用一条队列：长批量刮削进行中，推送也要能立刻跑完。
func TestPushRunsWhileScrapeBatchRunning(t *testing.T) {
	srv := slowTMDBServer(t, 1200*time.Millisecond, "/3/movie/550")
	p, st := newScrapePipeline(t, srv)
	tg := fakeTelegramServer(t)
	nt, err := notifier.NewAtServerURL(
		config.TelegramConfig{Token: "123:test", ChatID: "-1001"},
		nil, &config.TelegramRate{Enable: false}, tg.URL)
	if err != nil || nt == nil {
		t.Fatalf("构造推送器: %+v %v", nt, err)
	}
	p.UpdateComponents(p.Scraper(), nt)

	scraping := &model.Link{
		ShareID: "slow-scrape", URL: "https://guangyapan.com/s/slow_x",
		Title: "Fight Club", TMDBID: 550, Status: model.StatusPending,
	}
	ready := &model.Link{
		ShareID: "ready-push", URL: "https://guangyapan.com/s/ready_x",
		Title: "Fight Club", TMDBID: 900, Status: model.StatusScraped,
	}
	for _, l := range []*model.Link{scraping, ready} {
		if ok, err := st.Create(l); err != nil || !ok {
			t.Fatalf("Create(%s): ok=%v err=%v", l.ShareID, ok, err)
		}
	}

	p.Start(1)
	p.Enqueue("scrape", []int64{scraping.ID})
	waitStatus(t, st, scraping.ID, model.StatusScraping)

	p.Enqueue("push", []int64{ready.ID})
	if pr := waitDone(t, p, "push"); pr.Done != 1 {
		t.Fatalf("推送进度异常: %+v", pr)
	}
	if !p.StatusOf("scrape").Running {
		t.Errorf("推送跑完时刮削应仍在进行，实际: %+v", p.StatusOf("scrape"))
	}
	waitDone(t, p, "scrape")

	got, err := st.Get(ready.ID)
	if err != nil || got == nil || got.Status != model.StatusPushed {
		t.Fatalf("待推送记录应变为已推送: %+v %v", got, err)
	}
}
