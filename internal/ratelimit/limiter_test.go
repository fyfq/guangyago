package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestLimiter_Disabled(t *testing.T) {
	// enabled=false 与 perSecond<=0 都应退化为不限流
	for _, l := range []*Limiter{
		New(10, 5, false),
		New(0, 5, true),
		New(-1, 5, true),
		nil,
	} {
		ctx := context.Background()
		for i := 0; i < 100; i++ {
			if err := l.Wait(ctx); err != nil {
				t.Fatalf("未启用限流时 Wait 应始终返回 nil: %v", err)
			}
		}
	}
}

func TestLimiter_BurstThenThrottle(t *testing.T) {
	// 桶容量 2：前两次立即返回，第三次需要等待
	l := New(1000, 2, true)
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 2; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Errorf("突发额度内不应等待，实际耗时 %v", elapsed)
	}
	if err := l.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Errorf("超出突发额度后应等待，实际耗时 %v", elapsed)
	}
}

func TestLimiter_ContextCancel(t *testing.T) {
	// 极低速率：初始令牌耗尽后必须等待很久，用短超时验证 ctx 取消
	l := New(0.001, 1, true)
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Error("ctx 取消后 Wait 应返回错误")
	}
}

func TestSemaphore_NoLimit(t *testing.T) {
	for _, s := range []*Semaphore{nil, NewSemaphore(0), NewSemaphore(-1)} {
		release, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatalf("不限并发时 Acquire 应成功: %v", err)
		}
		release()
	}
}

func TestSemaphore_LimitAndRelease(t *testing.T) {
	s := NewSemaphore(1)
	release, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// 名额已占满，再次获取应在 ctx 取消后失败
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Acquire(ctx); err == nil {
		t.Error("名额耗尽时 Acquire 应阻塞直到 ctx 取消")
	}

	// 释放后可以再次获取
	release()
	release2, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatalf("释放后应能再次获取: %v", err)
	}
	release2()
}
