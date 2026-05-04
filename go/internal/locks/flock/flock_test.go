package flock

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/awbx/cronix/go/internal/locks"
)

func TestAcquireRelease(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	h, err := b.Acquire(t.Context(), "job-a", time.Minute)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := h.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Re-acquire after release.
	h2, err := b.Acquire(t.Context(), "job-a", time.Minute)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if err := h2.Release(); err != nil {
		t.Fatalf("re-release: %v", err)
	}
}

func TestContentionFailFast(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	h, err := b.Acquire(t.Context(), "job-b", time.Minute)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer h.Release()

	if _, err := b.Acquire(t.Context(), "job-b", time.Minute); !errors.Is(err, locks.ErrContended) {
		t.Fatalf("expected ErrContended, got %v", err)
	}
}

func TestContendingGoroutinesExactlyOneWins(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

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
			h, err := b.Acquire(t.Context(), "job-c", time.Minute)
			if err != nil {
				if errors.Is(err, locks.ErrContended) {
					contended.Add(1)
					return
				}
				t.Errorf("acquire: %v", err)
				return
			}
			wins.Add(1)
			// Hold briefly to ensure others see contention.
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
		t.Fatalf("wins(%d)+contended(%d) != N(%d)", wins.Load(), contended.Load(), N)
	}
}

func TestWaitWithDeadline(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	h, err := b.Acquire(t.Context(), "job-d", time.Minute)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// Release the lock after 200ms so the contended caller can proceed.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = h.Release()
	}()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	h2, err := b.Acquire(ctx, "job-d", time.Minute)
	if err != nil {
		t.Fatalf("waited acquire: %v", err)
	}
	_ = h2.Release()
}

func TestWaitTimesOut(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	h, err := b.Acquire(t.Context(), "job-e", time.Minute)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer h.Release()

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := b.Acquire(ctx, "job-e", time.Minute); !errors.Is(err, locks.ErrContended) {
		t.Fatalf("expected ErrContended on timeout, got %v", err)
	}
}

func TestIllegalKey(t *testing.T) {
	b, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := b.Acquire(t.Context(), "", time.Minute); err == nil {
		t.Fatalf("expected error on empty key")
	}
	if _, err := b.Acquire(t.Context(), "../escape", time.Minute); err == nil {
		t.Fatalf("expected error on key with /")
	}
}
