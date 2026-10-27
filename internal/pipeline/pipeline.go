package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/notifier"
	"github.com/odin/guangya-go/internal/parser"
	"github.com/odin/guangya-go/internal/ratelimit"
	"github.com/odin/guangya-go/internal/scraper"
	"github.com/odin/guangya-go/internal/store"
)

type jobKind string

const (
	jobScrape jobKind = "scrape"
	jobPush   jobKind = "push"
)

type job struct {
	kind   jobKind
	linkID int64
}

// Progress 描述当前批次任务的执行情况。
type Progress struct {
	Running bool   `json:"running"`
	Kind    string `json:"kind"`
	Total   int    `json:"total"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
	Current string `json:"current"`
	Error   string `json:"error"`
}

// Pipeline 以 goroutine 池串行处理刮削与推送任务。
type Pipeline struct {
	store    *store.Store
	scraper  *scraper.Client
	notifier *notifier.Notifier
	jobs     chan job
	mu       sync.Mutex
	progress Progress
	wg       sync.WaitGroup
	// guangya 是光鸭分享检测的并发上限。光鸭没有公布官方配额，
	// 默认只允许 2 个并发，接入分享检测后由 GuangyaGuard 统一把关。
	guangya *ratelimit.Semaphore
}

func New(st *store.Store, sc *scraper.Client, nt *notifier.Notifier) *Pipeline {
	return &Pipeline{
		store:    st,
		scraper:  sc,
		notifier: nt,
		jobs:     make(chan job, 1024),
		guangya:  ratelimit.NewSemaphore(config.DefaultGuangyaConcurrency),
	}
}

// SetGuangyaLimit 更新光鸭分享检测的并发上限，界面改配置后即时生效。
func (p *Pipeline) SetGuangyaLimit(n int) { p.guangya = ratelimit.NewSemaphore(n) }

// GuangyaGuard 占用一个光鸭分享检测名额，返回的函数用于释放。
// 光鸭官方尚未公布配额，这里用并发上限兜底（默认 2）。
func (p *Pipeline) GuangyaGuard(ctx context.Context) (func(), error) {
	if p == nil || p.guangya == nil {
		return func() {}, nil
	}
	return p.guangya.Acquire(ctx)
}

// Start 启动 worker。
func (p *Pipeline) Start(workers int) {
	if workers <= 0 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		go func() {
			for j := range p.jobs {
				p.handle(j)
			}
		}()
	}
}

// Enqueue 提交一批任务，返回排队数量。
func (p *Pipeline) Enqueue(kind jobKind, ids []int64) int {
	if len(ids) == 0 {
		return 0
	}
	p.mu.Lock()
	p.progress = Progress{Running: true, Kind: string(kind), Total: len(ids)}
	p.mu.Unlock()
	go func() {
		for _, id := range ids {
			p.jobs <- job{kind: kind, linkID: id}
		}
	}()
	return len(ids)
}

// UpdateComponents 在界面修改密钥后替换刮削与推送组件。
func (p *Pipeline) UpdateComponents(sc *scraper.Client, nt *notifier.Notifier) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scraper = sc
	p.notifier = nt
}

func (p *Pipeline) Status() Progress {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.progress
}

func (p *Pipeline) tick(ok bool, name string, err string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.progress.Current = name
	if ok {
		p.progress.Done++
	} else {
		p.progress.Failed++
		if err != "" {
			p.progress.Error = err
		}
	}
	if p.progress.Done+p.progress.Failed >= p.progress.Total {
		p.progress.Running = false
		p.progress.Current = "完成"
	}
}

func (p *Pipeline) handle(j job) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	l, err := p.store.Get(j.linkID)
	if err != nil || l == nil {
		p.tick(false, "记录不存在", "记录不存在")
		return
	}
	switch j.kind {
	case jobScrape:
		p.scrape(ctx, l)
	case jobPush:
		p.push(ctx, l)
	}
}

func (p *Pipeline) scrape(ctx context.Context, l *model.Link) {
	if p.scraper == nil {
		p.store.UpdateStatus(l.ID, model.StatusFailed, "未配置 TMDB API Key")
		p.tick(false, l.Title, "未配置 TMDB API Key")
		return
	}
	_ = p.store.UpdateStatus(l.ID, model.StatusScraping, "")

	var res *scraper.Result
	var err error
	// 刮削会覆写 title/year，必须从 raw_title 回溯原始片名与年份，
	// 否则重刮时拿上一次的结果自我印证，永远纠不回正确的类型。
	ref := scraper.MatchRef{Title: l.Title, RawTitle: l.RawTitle, Year: l.Year}
	if origTitle, origYear, _ := parser.SplitTitle(l.RawTitle); origTitle != "" {
		ref.Title = origTitle
		ref.Year = origYear
	}
	if ref.Year == 0 {
		ref.Year = l.Year
	}
	// 已刮削过的记录重刮时不带 hint，避免沿用上一次（可能错误的）类型
	if l.MediaType != "" && !scrapedBefore(l.Status) {
		ref.Hint = string(l.MediaType)
	}
	if l.TMDBID > 0 {
		res, err = p.scraper.Fetch(ctx, l.TMDBID, ref)
	}
	if err != nil || res == nil {
		res, err = p.scraper.Search(ctx, ref.Title, ref.RawTitle, ref.Year)
	}
	if err != nil || res == nil {
		msg := "未匹配到 TMDB 条目"
		if err != nil {
			msg = err.Error()
		}
		_ = p.store.UpdateStatus(l.ID, model.StatusUnmatched, msg)
		p.tick(false, l.Title, msg)
		return
	}

	poster := p.scraper.SavePoster(ctx, res)
	l.Title = res.Title
	l.OriginalTitle = res.OriginalTitle
	l.Year = res.Year
	l.MediaType = model.MediaType(res.MediaType)
	l.TMDBID = res.TMDBID
	l.Overview = res.Overview
	l.Genres = res.Genres
	l.VoteAverage = res.VoteAverage
	l.Runtime = res.Runtime
	l.PosterURL = res.PosterURL
	l.PosterPath = poster
	if err := p.store.UpdateScraped(l.ID, l); err != nil {
		_ = p.store.UpdateStatus(l.ID, model.StatusFailed, err.Error())
		p.tick(false, l.Title, err.Error())
		return
	}
	// 标题与年份都对不上，说明电影/剧集很可能判错了，转入待核查而非直接推送
	if res.LowConfidence {
		msg := fmt.Sprintf("TMDB %s/%d 命中「%s」(%d)，与原始标题「%s」差异较大，请确认类型",
			res.MediaType, res.TMDBID, res.Title, res.Year, ref.Title)
		_ = p.store.UpdateStatus(l.ID, model.StatusReview, msg)
		p.tick(false, l.Title, msg)
		return
	}
	slog.Debug("刮削完成", "id", l.ID, "title", res.Title, "type", res.MediaType, "score", res.MatchScore)
	p.tick(true, res.Title, "")
}

// scrapedBefore 判断这条记录是否已经成功刮削过。
func scrapedBefore(s model.Status) bool {
	return s == model.StatusScraped || s == model.StatusPushed || s == model.StatusPushFailed
}

func (p *Pipeline) push(ctx context.Context, l *model.Link) {
	if p.notifier == nil {
		_ = p.store.UpdateStatus(l.ID, model.StatusPushFailed, "未配置 Telegram Bot Token")
		p.tick(false, l.Title, "未配置 Telegram Bot Token")
		return
	}
	if l.Status != model.StatusScraped && l.TMDBID == 0 {
		_ = p.store.UpdateStatus(l.ID, model.StatusPushFailed, "尚未完成刮削")
		p.tick(false, l.Title, "尚未完成刮削")
		return
	}
	msgID, err := p.notifier.Send(ctx, l)
	if err != nil {
		_ = p.store.UpdateStatus(l.ID, model.StatusPushFailed, err.Error())
		p.tick(false, l.Title, err.Error())
		return
	}
	_ = p.store.UpdatePushed(l.ID, msgID)
	p.tick(true, l.Title, "")
	// 发送节奏交给 Notifier 的限流器统一控制，这里不再额外 sleep：
	// 硬编码的等待会和限流叠加，也会绕过「海报失败回退」这类额外调用。
}
