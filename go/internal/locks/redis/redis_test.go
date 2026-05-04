package redis

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/awbx/cronix/go/internal/locks"
)

func newBackend(t *testing.T) (*Backend, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return New(client, "test:lock:"), mr
}

func TestAcquireRelease(t *testing.T) {
	b, _ := newBackend(t)
	h, err := b.Acquire(t.Context(), "job-a", 30*time.Second)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := h.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Idempotent release.
	if err := h.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
	// Re-acquire works after release.
	h2, err := b.Acquire(t.Context(), "job-a", 30*time.Second)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	_ = h2.Release()
}

func TestContentionFailFast(t *testing.T) {
	b, _ := newBackend(t)
	h, err := b.Acquire(t.Context(), "job-b", 30*time.Second)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer h.Release()

	if _, err := b.Acquire(t.Context(), "job-b", 30*time.Second); !errors.Is(err, locks.ErrContended) {
		t.Fatalf("expected ErrContended, got %v", err)
	}
}

func TestExclusiveAcrossGoroutines(t *testing.T) {
	b, _ := newBackend(t)
	const N = 10
	var wins atomic.Int32
	var contended atomic.Int32
	var wg sync.WaitGroup
	wg.Add(N)
	start := make(chan struct{})

	for range N {
		go func() {
			defer wg.Done()
			<-start
			h, err := b.Acquire(t.Context(), "job-c", 30*time.Second)
			if err != nil {
				if errors.Is(err, locks.ErrContended) {
					contended.Add(1)
					return
				}
				t.Errorf("acquire: %v", err)
				return
			}
			wins.Add(1)
			time.Sleep(20 * time.Millisecond)
			_ = h.Release()
		}()
	}
	close(start)
	wg.Wait()

	if wins.Load() < 1 {
		t.Fatalf("expected at least one win, got %d", wins.Load())
	}
	if wins.Load()+contended.Load() != N {
		t.Fatalf("wins(%d)+contended(%d) != %d", wins.Load(), contended.Load(), N)
	}
}

func TestRefreshExtendsTTL(t *testing.T) {
	b, mr := newBackend(t)
	h, err := b.Acquire(t.Context(), "job-d", 1*time.Second)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer h.Release()

	mr.FastForward(700 * time.Millisecond)
	if err := h.Refresh(t.Context()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	mr.FastForward(700 * time.Millisecond)
	// After refresh + 700ms forward, lock should still be held (refresh
	// reset TTL to 1s; 700ms is < 1s).
	if _, err := b.Acquire(t.Context(), "job-d", 5*time.Second); !errors.Is(err, locks.ErrContended) {
		t.Fatalf("expected ErrContended after refresh, got %v", err)
	}
}

func TestReleaseDoesNotKillOtherHolder(t *testing.T) {
	b, mr := newBackend(t)
	// Acquire and let the lock expire.
	h1, err := b.Acquire(t.Context(), "job-e", 1*time.Second)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	mr.FastForward(2 * time.Second)
	// Now a second holder takes the lock.
	h2, err := b.Acquire(t.Context(), "job-e", 30*time.Second)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	defer h2.Release()
	// First holder's late Release MUST NOT delete the second holder's key.
	if err := h1.Release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	// Confirm the lock is still held by h2 — a third Acquire should fail.
	if _, err := b.Acquire(t.Context(), "job-e", 5*time.Second); !errors.Is(err, locks.ErrContended) {
		t.Fatalf("expected ErrContended after stale release, got %v", err)
	}
}

func TestWaitTimesOut(t *testing.T) {
	b, _ := newBackend(t)
	h, err := b.Acquire(t.Context(), "job-f", 30*time.Second)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer h.Release()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := b.Acquire(ctx, "job-f", 30*time.Second); !errors.Is(err, locks.ErrContended) {
		t.Fatalf("expected ErrContended on deadline, got %v", err)
	}
}

func TestIllegalArgs(t *testing.T) {
	b, _ := newBackend(t)
	if _, err := b.Acquire(t.Context(), "", 30*time.Second); err == nil {
		t.Fatalf("expected error on empty key")
	}
	if _, err := b.Acquire(t.Context(), "ok", 0); err == nil {
		t.Fatalf("expected error on zero ttl")
	}
}
