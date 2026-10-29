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
	// batch 是入队时的批次号。取消或重新入队后旧批次号失效，
	// 已排队和在途的任务据此丢弃，不会串到新批次的进度里。
	batch int64
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
	// updated 用于在多批次之间挑出最近活动的一个，不对外输出。
	updated time.Time
}

// Pipeline 以 goroutine 池串行处理刮削与推送任务。
type Pipeline struct {
	store    *store.Store
	scraper  *scraper.Client
	notifier *notifier.Notifier
	jobs     chan job
	// ctx 生命周期覆盖整个 pipeline，Stop 后所有 worker 与 feed goroutine 都会退出。
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	// progress 与 batch 按任务类型（刮削 / 推送）各自独立，
	// 两种任务可以同时进行，互不覆盖进度。
	progress map[jobKind]*Progress
	batch    map[jobKind]int64
	// guangya 是光鸭分享检测的并发上限。光鸭没有公布官方配额，
	// 默认只允许 2 个并发，接入分享检测后由 GuangyaGuard 统一把关。
	guangya *ratelimit.Semaphore
	// started 记录已经启动的 worker 数，避免热更新时重复起 goroutine。
	started bool
}

func New(st *store.Store, sc *scraper.Client, nt *notifier.Notifier) *Pipeline {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pipeline{
		store:    st,
		scraper:  sc,
		notifier: nt,
		jobs:     make(chan job, 1024),
		ctx:      ctx,
		cancel:   cancel,
		progress: map[jobKind]*Progress{},
		batch:    map[jobKind]int64{},
		guangya:  ratelimit.NewSemaphore(config.DefaultGuangyaConcurrency),
	}
}

// Stop 关停 pipeline：取消所有 feed goroutine、停止 worker 消费、清空队列。
// 幂等，重复调用安全。
func (p *Pipeline) Stop() {
	p.cancel()
}

// SetGuangyaLimit 更新光鸭分享检测的并发上限，界面改配置后即时生效。
func (p *Pipeline) SetGuangyaLimit(n int) {
	sem := ratelimit.NewSemaphore(n)
	p.mu.Lock()
	p.guangya = sem
	p.mu.Unlock()
}

// GuangyaGuard 占用一个光鸭分享检测名额，返回的函数用于释放。
// 光鸭官方尚未公布配额，这里用并发上限兜底（默认 2）。
func (p *Pipeline) GuangyaGuard(ctx context.Context) (func(), error) {
	p.mu.Lock()
	sem := p.guangya
	p.mu.Unlock()
	if sem == nil {
		return func() {}, nil
	}
	return sem.Acquire(ctx)
}

// components 返回当前刮削与推送组件，热更新时保证读到同一份快照。
func (p *Pipeline) components() (*scraper.Client, *notifier.Notifier) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scraper, p.notifier
}

// Scraper 返回当前刮削客户端，未配置 TMDB Key 时为 nil。
func (p *Pipeline) Scraper() *scraper.Client {
	sc, _ := p.components()
	return sc
}

// Start 启动 worker。同一实例重复调用只生效一次；Stop 后不可重启。
func (p *Pipeline) Start(workers int) {
	if workers <= 0 {
		workers = 1
	}
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.mu.Unlock()
	for i := 0; i < workers; i++ {
		go p.worker()
	}
}

// worker 消费任务，收到关停信号后退出。
func (p *Pipeline) worker() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case j, ok := <-p.jobs:
			if !ok {
				return
			}
			p.handle(j)
		}
	}
}

// Enqueue 提交一批任务，返回排队数量。
// 同类任务正在运行时追加到当前批次，不会重置进度。
// pipeline 已关停时返回 0，不再投递任务。
func (p *Pipeline) Enqueue(kind string, ids []int64) int {
	if len(ids) == 0 {
		return 0
	}
	select {
	case <-p.ctx.Done():
		return 0
	default:
	}
	k := jobKind(kind)
	p.mu.Lock()
	prog := p.progress[k]
	if prog == nil || !prog.Running {
		p.batch[k]++
		prog = &Progress{Running: true, Kind: kind, Total: len(ids), Current: "排队中"}
		p.progress[k] = prog
	} else {
		prog.Total += len(ids)
	}
	prog.updated = time.Now()
	batch := p.batch[k]
	p.mu.Unlock()

	go p.feed(k, batch, ids)
	return len(ids)
}

// feed 把本批次的 ID 依次写入队列，批次失效（取消或替换）时提前退出。
// 队列满或 pipeline 关停时也能中断，不会永久阻塞。
func (p *Pipeline) feed(kind jobKind, batch int64, ids []int64) {
	for _, id := range ids {
		p.mu.Lock()
		stale := p.batch[kind] != batch
		p.mu.Unlock()
		if stale {
			return
		}
		select {
		case <-p.ctx.Done():
			return
		case p.jobs <- job{kind: kind, linkID: id, batch: batch}:
		}
	}
}

// Cancel 取消指定类型的排队任务，kind 为空时取消全部。
// 已经在执行的那一条会跑完，但结果不再计入进度。
func (p *Pipeline) Cancel(kind string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for k, prog := range p.progress {
		if kind != "" && string(k) != kind {
			continue
		}
		if prog == nil || !prog.Running {
			continue
		}
		prog.Running = false
		prog.Current = "已取消"
		prog.updated = time.Now()
		p.batch[k]++
		n++
	}
	return n
}

// UpdateComponents 在界面修改密钥后替换刮削与推送组件。
func (p *Pipeline) UpdateComponents(sc *scraper.Client, nt *notifier.Notifier) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scraper = sc
	p.notifier = nt
}

// Status 返回最该展示的进度：优先正在运行且最近有动作的批次，
// 否则是最近结束的那个批次。
func (p *Pipeline) Status() Progress {
	p.mu.Lock()
	defer p.mu.Unlock()
	var best *Progress
	for _, prog := range p.progress {
		if prog == nil {
			continue
		}
		if best == nil || (prog.Running && !best.Running) ||
			(prog.Running == best.Running && prog.updated.After(best.updated)) {
			best = prog
		}
	}
	if best == nil {
		return Progress{}
	}
	return *best
}

// StatusOf 返回指定类型（scrape / push）的进度，没有跑过时返回零值。
func (p *Pipeline) StatusOf(kind string) Progress {
	p.mu.Lock()
	defer p.mu.Unlock()
	if prog := p.progress[jobKind(kind)]; prog != nil {
		return *prog
	}
	return Progress{}
}

func (p *Pipeline) tick(j job, ok bool, name string, err string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prog := p.progress[j.kind]
	if prog == nil || p.batch[j.kind] != j.batch {
		return
	}
	prog.Current = name
	prog.updated = time.Now()
	if ok {
		prog.Done++
	} else {
		prog.Failed++
		if err != "" {
			prog.Error = err
		}
	}
	if prog.Done+prog.Failed >= prog.Total {
		prog.Running = false
		prog.Current = "完成"
	}
}

func (p *Pipeline) stale(j job) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.batch[j.kind] != j.batch
}

func (p *Pipeline) handle(j job) {
	if p.stale(j) {
		return
	}
	ctx, cancel := context.WithTimeout(p.ctx, 60*time.Second)
	defer cancel()

	l, err := p.store.Get(j.linkID)
	if err != nil || l == nil {
		p.tick(j, false, "记录不存在", "记录不存在")
		return
	}
	sc, nt := p.components()
	switch j.kind {
	case jobScrape:
		p.scrape(ctx, j, l, sc)
	case jobPush:
		p.push(ctx, j, l, nt)
	}
}

// updateStatus 更新状态并在失败时写日志，避免调用点静默丢弃错误。
func (p *Pipeline) updateStatus(id int64, status model.Status, msg string) {
	if err := p.store.UpdateStatus(id, status, msg); err != nil {
		slog.Error("更新任务状态失败", "id", id, "status", string(status), "err", err)
	}
}

func (p *Pipeline) scrape(ctx context.Context, j job, l *model.Link, sc *scraper.Client) {
	if sc == nil {
		p.updateStatus(l.ID, model.StatusFailed, "未配置 TMDB API Key")
		p.tick(j, false, l.Title, "未配置 TMDB API Key")
		return
	}
	p.updateStatus(l.ID, model.StatusScraping, "")

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
		res, err = sc.Fetch(ctx, l.TMDBID, ref)
	}
	if err != nil || res == nil {
		res, err = sc.Search(ctx, ref.Title, ref.RawTitle, ref.Year)
	}
	if err != nil || res == nil {
		msg := "未匹配到 TMDB 条目"
		if err != nil {
			msg = err.Error()
		}
		p.updateStatus(l.ID, model.StatusUnmatched, msg)
		p.tick(j, false, l.Title, msg)
		return
	}

	poster := sc.SavePoster(ctx, res)
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
		slog.Error("写入刮削结果失败", "id", l.ID, "err", err)
		p.updateStatus(l.ID, model.StatusFailed, err.Error())
		p.tick(j, false, l.Title, err.Error())
		return
	}
	// 标题与年份都对不上，说明电影/剧集很可能判错了，转入待核查而非直接推送
	if res.LowConfidence {
		msg := fmt.Sprintf("TMDB %s/%d 命中「%s」(%d)，与原始标题「%s」差异较大，请确认类型",
			res.MediaType, res.TMDBID, res.Title, res.Year, ref.Title)
		p.updateStatus(l.ID, model.StatusReview, msg)
		p.tick(j, false, l.Title, msg)
		return
	}
	slog.Debug("刮削完成", "id", l.ID, "title", res.Title, "type", res.MediaType, "score", res.MatchScore)
	p.tick(j, true, res.Title, "")
}

// scrapedBefore 判断这条记录是否已经成功刮削过。
func scrapedBefore(s model.Status) bool {
	return s == model.StatusScraped || s == model.StatusPushed || s == model.StatusPushFailed
}

func (p *Pipeline) push(ctx context.Context, j job, l *model.Link, nt *notifier.Notifier) {
	if nt == nil {
		p.updateStatus(l.ID, model.StatusPushFailed, "未配置 Telegram Bot Token")
		p.tick(j, false, l.Title, "未配置 Telegram Bot Token")
		return
	}
	if l.Status != model.StatusScraped && l.TMDBID == 0 {
		p.updateStatus(l.ID, model.StatusPushFailed, "尚未完成刮削")
		p.tick(j, false, l.Title, "尚未完成刮削")
		return
	}
	msgID, err := nt.Send(ctx, l)
	if err != nil {
		p.updateStatus(l.ID, model.StatusPushFailed, err.Error())
		p.tick(j, false, l.Title, err.Error())
		return
	}
	// 推送已成功、消息 ID 写回失败时会导致后续无法撤回或跟踪该条消息，必须记录
	if err := p.store.UpdatePushed(l.ID, msgID); err != nil {
		slog.Error("回写推送消息 ID 失败", "id", l.ID, "message_id", msgID, "err", err)
	}
	p.tick(j, true, l.Title, "")
	// 发送节奏交给 Notifier 的限流器统一控制，这里不再额外 sleep：
	// 硬编码的等待会和限流叠加，也会绕过「海报失败回退」这类额外调用。
}
