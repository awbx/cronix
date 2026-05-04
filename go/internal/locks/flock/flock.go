// Package flock implements the Lock interface using cross-platform OS file
// locks (`gofrs/flock`). This is the default lock backend for jobs whose
// concurrency_scope is "host" — locks live in /var/lock/cronix/ (or
// ~/.cronix/locks/ when not running as root) and are automatically released
// by the kernel on process exit, so a crashed shim does not leak the lock.
package flock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gofrsflock "github.com/gofrs/flock"

	"github.com/awbx/cronix/go/internal/locks"
)

// Backend is the flock-based Lock implementation.
type Backend struct {
	dir string
}

// New constructs a Backend rooted at `dir`. Defaults to /var/lock/cronix
// when dir is empty. Creates the directory if it does not exist.
func New(dir string) (*Backend, error) {
	if dir == "" {
		dir = "/var/lock/cronix"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("flock: mkdir %s: %w", dir, err)
	}
	return &Backend{dir: dir}, nil
}

// Name returns "flock".
func (*Backend) Name() string { return "flock" }

// Acquire returns a Handle on success, ErrContended on contention, or a
// wrapped error otherwise. When `ctx` is cancelable, Acquire polls every
// 100ms until acquired or the context is done.
func (b *Backend) Acquire(ctx context.Context, key string, _ time.Duration) (locks.Handle, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	path := filepath.Join(b.dir, key+".lock")
	fl := gofrsflock.New(path)

	if _, deadlineSet := ctx.Deadline(); !deadlineSet && ctx.Err() == nil {
		// No deadline → fail-fast on contention.
		got, err := fl.TryLock()
		if err != nil {
			return nil, fmt.Errorf("flock: try-lock %s: %w", path, err)
		}
		if !got {
			return nil, locks.ErrContended
		}
		return &handle{fl: fl}, nil
	}

	// With a deadline → poll until acquired or ctx done.
	for {
		got, err := fl.TryLock()
		if err != nil {
			return nil, fmt.Errorf("flock: try-lock %s: %w", path, err)
		}
		if got {
			return &handle{fl: fl}, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, locks.ErrContended
			}
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func validateKey(key string) error {
	if key == "" {
		return errors.New("flock: key must be non-empty")
	}
	if strings.ContainsAny(key, `/\` + "\x00") {
		return fmt.Errorf("flock: key contains illegal characters: %q", key)
	}
	return nil
}

type handle struct {
	mu       sync.Mutex
	fl       *gofrsflock.Flock
	released bool
}

func (h *handle) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return nil
	}
	h.released = true
	return h.fl.Unlock()
}

// Refresh is a no-op for flock — file locks do not expire.
func (h *handle) Refresh(_ context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return errors.New("flock: refresh after release")
	}
	return nil
}
