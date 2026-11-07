package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Result is the outcome of an Allow check.
type Result struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Limiter is a concurrency-safe rate limiter. Implementations may be local or remote (e.g. Redis).
type Limiter interface {
	Allow(ctx context.Context, key string) (Result, error)
}

// Memory is an in-process token-bucket limiter.
type Memory struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	capacity float64
	refill   float64 // tokens per second
	now      func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// Config tunes a Memory limiter.
type Config struct {
	Burst  int           // max tokens (default 5)
	Window time.Duration // refill window to refill Burst tokens (default 60s)
	Now    func() time.Time
}

// NewMemory builds a process-local token bucket.
func NewMemory(cfg Config) *Memory {
	burst := cfg.Burst
	if burst <= 0 {
		burst = 5
	}
	window := cfg.Window
	if window <= 0 {
		window = time.Minute
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Memory{
		buckets:  make(map[string]*bucket),
		capacity: float64(burst),
		refill:   float64(burst) / window.Seconds(),
		now:      now,
	}
}

// Allow consumes one token for key if available.
func (m *Memory) Allow(_ context.Context, key string) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	b := m.buckets[key]
	if b == nil {
		b = &bucket{tokens: m.capacity, last: now}
		m.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * m.refill
		if b.tokens > m.capacity {
			b.tokens = m.capacity
		}
		b.last = now
	}

	if b.tokens < 1 {
		need := 1 - b.tokens
		retry := time.Duration(need/m.refill*float64(time.Second)) + time.Second/10
		if retry < time.Second {
			retry = time.Second
		}
		return Result{Allowed: false, RetryAfter: retry.Round(time.Second)}, nil
	}

	b.tokens--
	return Result{Allowed: true}, nil
}

// AllowAll requires every key to pass; returns the first denial (largest RetryAfter among denials).
func AllowAll(ctx context.Context, lim Limiter, keys ...string) (Result, error) {
	var denied Result
	denied.Allowed = true
	for _, key := range keys {
		if key == "" {
			continue
		}
		res, err := lim.Allow(ctx, key)
		if err != nil {
			return Result{}, err
		}
		if !res.Allowed {
			if denied.Allowed || res.RetryAfter > denied.RetryAfter {
				denied = res
			}
		}
	}
	if !denied.Allowed {
		return denied, nil
	}
	return Result{Allowed: true}, nil
}
