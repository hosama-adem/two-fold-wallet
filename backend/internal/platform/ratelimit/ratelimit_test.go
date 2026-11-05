package ratelimit_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
)

func TestMemoryAllowsBurstThenDenies(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	lim := ratelimit.NewMemory(ratelimit.Config{
		Burst:  5,
		Window: time.Minute,
		Now:    func() time.Time { return now },
	})
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		res, err := lim.Allow(ctx, "ip:1")
		if err != nil || !res.Allowed {
			t.Fatalf("allow %d: %+v err=%v", i, res, err)
		}
	}
	res, err := lim.Allow(ctx, "ip:1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Allowed {
		t.Fatal("expected deny on 6th")
	}
	if res.RetryAfter < time.Second {
		t.Fatalf("RetryAfter=%v", res.RetryAfter)
	}
}

func TestMemoryRefills(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	lim := ratelimit.NewMemory(ratelimit.Config{
		Burst:  5,
		Window: time.Minute,
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
	})
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = lim.Allow(ctx, "phone:+2519")
	}
	mu.Lock()
	now = now.Add(time.Minute)
	mu.Unlock()
	res, err := lim.Allow(ctx, "phone:+2519")
	if err != nil || !res.Allowed {
		t.Fatalf("after refill: %+v err=%v", res, err)
	}
}

func TestMemoryConcurrent(t *testing.T) {
	t.Parallel()
	lim := ratelimit.NewMemory(ratelimit.Config{Burst: 50, Window: time.Minute})
	ctx := context.Background()
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := lim.Allow(ctx, "concurrent")
			if err != nil {
				t.Errorf("err: %v", err)
				return
			}
			if res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 50 {
		t.Fatalf("allowed=%d want 50", got)
	}
}

func TestAllowAllRequiresBoth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	lim := ratelimit.NewMemory(ratelimit.Config{Burst: 1, Window: time.Minute, Now: func() time.Time { return now }})
	ctx := context.Background()
	_, _ = lim.Allow(ctx, "ip:a")
	res, err := ratelimit.AllowAll(ctx, lim, "ip:a", "phone:b")
	if err != nil {
		t.Fatal(err)
	}
	if res.Allowed {
		t.Fatal("expected deny when IP exhausted")
	}
}
