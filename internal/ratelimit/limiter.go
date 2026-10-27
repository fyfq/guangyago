// Package ratelimit 提供令牌桶限流与并发上限控制，
// 用来把 TMDB / Telegram / 光鸭的请求频率压在各自官方阈值之下。
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter 是一个简单的令牌桶。零值可用，但零值等于不限流，
// 请统一用 New 构造，避免误用。
type Limiter struct {
	mu      sync.Mutex
	rate    float64 // 每秒补充的令牌数
	burst   float64 // 桶容量，允许的瞬时突发
	tokens  float64
	last    time.Time
	enabled bool
}

// New 构造限流器。perSecond <= 0 或 enabled=false 时退化为不限流。
// burst 是桶容量：设为 1 即等价于「两次调用至少间隔 1/perSecond」。
func New(perSecond float64, burst int, enabled bool) *Limiter {
	if !enabled || perSecond <= 0 {
		return &Limiter{enabled: false}
	}
	if burst < 1 {
		burst = 1
	}
	return &Limiter{
		rate:    perSecond,
		burst:   float64(burst),
		tokens:  float64(burst), // 桶初始是满的，首次调用不会等待
		last:    time.Now(),
		enabled: true,
	}
}

// Wait 阻塞到拿到一个令牌为止；ctx 取消时返回 ctx.Err()。
// 未启用限流时立即返回。
func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil || !l.enabled {
		return nil
	}
	for {
		d, ok := l.reserve()
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
}

// reserve 尝试取一个令牌，取不到时返回需要等待的时长。
func (l *Limiter) reserve() (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	// 按经过的时间补充令牌
	if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
		l.tokens += elapsed * l.rate
		l.last = now
	}
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	if l.tokens >= 1 {
		l.tokens--
		return 0, true
	}
	// 还差 (1 - tokens) 个令牌
	wait := (1 - l.tokens) / l.rate
	d := time.Duration(wait * float64(time.Second))
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d, false
}

// Rate 返回当前生效的每秒速率，用于界面回显。
func (l *Limiter) Rate() float64 {
	if l == nil || !l.enabled {
		return 0
	}
	return l.rate
}

// Enabled 报告该限流器是否生效。
func (l *Limiter) Enabled() bool { return l != nil && l.enabled }

// Semaphore 限制同时进行的任务数，用于没有明确 QPS 配额、
// 但需要控制并发的第三方服务（例如光鸭分享检测）。
type Semaphore struct {
	ch   chan struct{}
	size int
}

// NewSemaphore 构造并发上限为 n 的信号量；n <= 0 时不做限制（Acquire 立即返回）。
func NewSemaphore(n int) *Semaphore {
	if n <= 0 {
		return &Semaphore{size: 0}
	}
	return &Semaphore{ch: make(chan struct{}, n), size: n}
}

// Acquire 占用一个名额，返回释放函数。未启用时返回空函数。
func (s *Semaphore) Acquire(ctx context.Context) (func(), error) {
	if s == nil || s.size <= 0 {
		return func() {}, nil
	}
	select {
	case s.ch <- struct{}{}:
		return func() { <-s.ch }, nil
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
}

// Size 返回并发上限，0 表示不限。
func (s *Semaphore) Size() int {
	if s == nil {
		return 0
	}
	return s.size
}
