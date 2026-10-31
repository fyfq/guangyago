package pipeline

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/odin/gygo/internal/config"
	"github.com/odin/gygo/internal/model"
	"github.com/odin/gygo/internal/notifier"
	"github.com/odin/gygo/internal/scraper"
	"github.com/odin/gygo/internal/store"
)

// newSeamPipeline 用真实的 scraper/notifier 客户端装配 pipeline，两者的根地址都指向
// 包内假服务，因此成功路径可以离线跑通，不走外网。
func newSeamPipeline(t *testing.T, sc *scraper.Client, nt *notifier.Notifier) (*Pipeline, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	p := New(st, sc, nt)
	t.Cleanup(p.Stop)
	p.Start(1)
	return p, st
}

func seamSeed(t *testing.T, st *store.Store, shareID, rawTitle string, status model.Status) *model.Link {
	t.Helper()
	l := &model.Link{
		ShareID: shareID, RawTitle: rawTitle, Title: rawTitle,
		URL: "https://guangyapan.com/s/" + shareID, Status: status,
	}
	ok, err := st.Create(l)
	if err != nil || !ok {
		t.Fatalf("Create(%q): ok=%v err=%v", shareID, ok, err)
	}
	return l
}

func seamWait(t *testing.T, p *Pipeline, kind string) Progress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
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

const seamMovieDetail = `{"id":278,"title":"肖申克的救赎","original_title":"The Shawshank Redemption",
"release_date":"1994-09-10","overview":"希望是好事，也许是人间至善。","vote_average":8.7,"runtime":142,
"poster_path":"/kXq.jpg","genres":[{"name":"剧情"},{"name":"犯罪"}]}`

// seamTMDB 起一个假 TMDB：命中 routes 的路径返回对应 JSON，其余返回 404，
// 于是 Fetch 只会试探到电影侧，正好走「按 ID 直接命中」的成功路径。
func seamTMDB(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func seamScraper(t *testing.T, apiBase string) *scraper.Client {
	t.Helper()
	c, err := scraper.NewAtAPIBase(config.TMDBConfig{APIKey: "seam-key"}, t.TempDir(), nil, nil, apiBase)
	if err != nil || c == nil {
		t.Fatalf("NewAtAPIBase: %+v %v", c, err)
	}
	return c
}

// seamTelegram 起一个最小可用的假 Bot API：getMe 让构造通过，
// sendPhoto/sendMessage 固定返回消息 ID，并把收到的方法名记下来。
func seamTelegram(t *testing.T, messageID int) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		mu.Lock()
		calls = append(calls, method)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":77,"is_bot":true,"first_name":"光鸭","username":"guangya_bot"}}`))
		case "sendPhoto", "sendMessage":
			msg, err := json.Marshal(messageID)
			if err != nil {
				t.Fatalf("编码消息 ID: %v", err)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":` + string(msg) +
				`,"date":0,"chat":{"id":-1001234567890,"type":"channel"}}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

func seamNotifier(t *testing.T, serverURL string) *notifier.Notifier {
	t.Helper()
	nt, err := notifier.NewAtServerURL(
		config.TelegramConfig{Token: "77:seam-token", ChatID: config.ChatID("-1001234567890")},
		nil, nil, serverURL)
	if err != nil || nt == nil {
		t.Fatalf("NewAtServerURL: %+v %v", nt, err)
	}
	return nt
}

func TestScrape_SuccessMarksScraped(t *testing.T) {
	tmdb := seamTMDB(t, map[string]string{"/movie/278": seamMovieDetail})
	p, st := newSeamPipeline(t, seamScraper(t, tmdb.URL), nil)
	l := seamSeed(t, st, "scrape-ok", "肖申克的救赎 (1994)", model.StatusPending)
	if err := st.SetTMDBID(l.ID, 278, model.MediaMovie); err != nil {
		t.Fatalf("SetTMDBID: %v", err)
	}

	if n := p.Enqueue("scrape", []int64{l.ID}); n != 1 {
		t.Fatalf("排队数量 = %d，期望 1", n)
	}
	pr := seamWait(t, p, "scrape")
	if pr.Done != 1 || pr.Failed != 0 {
		t.Fatalf("刮削应计为成功: %+v", pr)
	}

	got, err := st.Get(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusScraped || got.Error != "" {
		t.Errorf("状态应为 scraped，实际 %q %q", got.Status, got.Error)
	}
	if got.Title != "肖申克的救赎" || got.OriginalTitle != "The Shawshank Redemption" ||
		got.Year != 1994 || got.Runtime != 142 || got.TMDBID != 278 {
		t.Errorf("刮削结果未落库: %+v", got)
	}
	if got.MediaType != model.MediaMovie || got.Overview == "" || got.Genres == "" {
		t.Errorf("类型/简介/分类未落库: %+v", got)
	}
	// 未开启海报缓存时不应下载图片，但远程地址要保留
	if got.PosterPath != "" || !strings.Contains(got.PosterURL, "/t/p/") {
		t.Errorf("海报处理不符合预期: %+v", got)
	}
}

func TestPush_SuccessRecordsMessageID(t *testing.T) {
	srv, calls := seamTelegram(t, 4242)
	p, st := newSeamPipeline(t, nil, seamNotifier(t, srv.URL))
	l := seamSeed(t, st, "push-ok", "肖申克的救赎 (1994)", model.StatusScraped)
	l.TMDBID = 278
	l.MediaType = model.MediaMovie
	l.Title = "肖申克的救赎"
	if err := st.UpdateScraped(l.ID, l); err != nil {
		t.Fatalf("UpdateScraped: %v", err)
	}

	if n := p.Enqueue("push", []int64{l.ID}); n != 1 {
		t.Fatalf("排队数量 = %d，期望 1", n)
	}
	pr := seamWait(t, p, "push")
	if pr.Done != 1 || pr.Failed != 0 {
		t.Fatalf("推送应计为成功: %+v", pr)
	}

	got, err := st.Get(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusPushed || got.TGMessageID != 4242 {
		t.Errorf("状态/消息 ID 未回写: %q %d", got.Status, got.TGMessageID)
	}
	// 无海报时直接发纯文本，不应尝试 sendPhoto（空图片会被 Telegram 当 file_id 拒掉）
	for _, m := range calls() {
		if m == "sendPhoto" {
			t.Error("无海报时不应调用 sendPhoto")
		}
	}
}

// 推送失败必须留下可检索的日志：记录 id 与 Telegram 原始报错，便于事后定位。
func TestPush_FailureLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		w.Header().Set("Content-Type", "application/json")
		if method == "getMe" {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":77,"is_bot":true,"first_name":"光鸭","username":"guangya_bot"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: wrong remote file identifier specified"}`))
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	p, st := newSeamPipeline(t, nil, seamNotifier(t, srv.URL))
	l := seamSeed(t, st, "push-log", "肖申克的救赎 (1994)", model.StatusScraped)
	l.TMDBID = 278
	if err := st.UpdateScraped(l.ID, l); err != nil {
		t.Fatalf("UpdateScraped: %v", err)
	}

	p.Enqueue("push", []int64{l.ID})
	if pr := seamWait(t, p, "push"); pr.Failed != 1 {
		t.Fatalf("推送应失败: %+v", pr)
	}
	out := buf.String()
	for _, want := range []string{"推送失败", "wrong remote file identifier"} {
		if !strings.Contains(out, want) {
			t.Errorf("失败日志缺少 %q: %s", want, out)
		}
	}
}

// 刮削成功后紧接着推送：一条记录走完两个阶段，状态从 scraped 推进到 pushed。
func TestScrapeThenPush_Chain(t *testing.T) {
	tmdb := seamTMDB(t, map[string]string{"/movie/278": seamMovieDetail})
	srv, _ := seamTelegram(t, 555)
	p, st := newSeamPipeline(t, seamScraper(t, tmdb.URL), seamNotifier(t, srv.URL))
	l := seamSeed(t, st, "chain", "肖申克的救赎 (1994)", model.StatusPending)
	if err := st.SetTMDBID(l.ID, 278, model.MediaMovie); err != nil {
		t.Fatalf("SetTMDBID: %v", err)
	}

	p.Enqueue("scrape", []int64{l.ID})
	if pr := seamWait(t, p, "scrape"); pr.Done != 1 {
		t.Fatalf("刮削阶段失败: %+v", pr)
	}
	p.Enqueue("push", []int64{l.ID})
	if pr := seamWait(t, p, "push"); pr.Done != 1 {
		t.Fatalf("推送阶段失败: %+v", pr)
	}

	got, err := st.Get(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusPushed || got.TGMessageID != 555 || got.Title == "" {
		t.Errorf("链路结束后记录状态异常: %+v", got)
	}
	// 刮削与推送各自成批，互不覆盖进度
	if sc, pu := p.StatusOf("scrape"), p.StatusOf("push"); sc.Done != 1 || pu.Done != 1 {
		t.Errorf("两类进度应各自独立: scrape %+v push %+v", sc, pu)
	}
}

// 未匹配到条目时转入 unmatched，并保留失败原因，供后台重试。
func TestScrape_Unmatched(t *testing.T) {
	tmdb := seamTMDB(t, nil)
	p, st := newSeamPipeline(t, seamScraper(t, tmdb.URL), nil)
	l := seamSeed(t, st, "miss", "不存在的片子 (2020)", model.StatusPending)

	p.Enqueue("scrape", []int64{l.ID})
	pr := seamWait(t, p, "scrape")
	if pr.Done != 0 || pr.Failed != 1 || pr.Error == "" {
		t.Errorf("未匹配应计为失败并带原因: %+v", pr)
	}

	got, err := st.Get(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusUnmatched {
		t.Errorf("状态应为 unmatched，实际 %q", got.Status)
	}
}
