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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	gofrsflock "github.com/gofrs/flock"

	"github.com/awbx/cronix/go/internal/locks"
)

// Backend is the flock-based Lock implementation.
type Backend struct {
	dir    string
	host   string       // resolved at construction; static for process lifetime
	killer locks.Killer // injectable for tests
	pid    int          // injectable for tests; defaults to os.Getpid
}

// Option mutates a Backend at construction. Used to inject test doubles.
type Option func(*Backend)

// WithHost overrides the hostname stored in the holder file. Tests pin
// this so cross-host behaviour is deterministic.
func WithHost(host string) Option { return func(b *Backend) { b.host = host } }

// WithKiller swaps the killer used by AcquireOrReplace. Tests inject a
// stub that captures (pid, signal) calls without actually signaling.
func WithKiller(k locks.Killer) Option { return func(b *Backend) { b.killer = k } }

// WithPID overrides the PID stored in the holder file. Tests use this
// to simulate distinct holders inside a single OS process.
func WithPID(pid int) Option { return func(b *Backend) { b.pid = pid } }

// New constructs a Backend rooted at `dir`. Defaults to /var/lock/cronix
// when dir is empty. Creates the directory if it does not exist.
func New(dir string, opts ...Option) (*Backend, error) {
	if dir == "" {
		dir = "/var/lock/cronix"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("flock: mkdir %s: %w", dir, err)
	}
	host, _ := os.Hostname()
	b := &Backend{dir: dir, host: host, killer: locks.DefaultKiller{}, pid: os.Getpid()}
	for _, opt := range opts {
		opt(b)
	}
	return b, nil
}

// Name returns "flock".
func (*Backend) Name() string { return "flock" }

// Acquire returns a Handle on success, ErrContended on contention, or a
// wrapped error otherwise. When `ctx` is cancelable, Acquire polls every
// 100ms until acquired or the context is done. On success a holder
// sidecar file `<key>.holder` is written so Replace contenders can
// identify the current owner; the file is removed on Release.
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
		return b.makeHandle(fl, key)
	}

	// With a deadline → poll until acquired or ctx done.
	for {
		got, err := fl.TryLock()
		if err != nil {
			return nil, fmt.Errorf("flock: try-lock %s: %w", path, err)
		}
		if got {
			return b.makeHandle(fl, key)
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

// AcquireOrReplace implements locks.Replacer for the `Replace` concurrency
// policy. On contention it reads the holder sidecar; if the holder lives
// on this host it sends SIGTERM and polls for up to `waitForExit`. A
// non-local holder, an unreadable holder file, or a holder that refuses
// to exit within the wait window all surface as ErrContended.
func (b *Backend) AcquireOrReplace(ctx context.Context, key string, ttl, waitForExit time.Duration) (locks.Handle, error) {
	// Fast path: lock is free.
	h, err := b.Acquire(ctx, key, ttl)
	if !errors.Is(err, locks.ErrContended) {
		return h, err
	}

	// Contended → read the holder sidecar.
	holder, err := b.readHolder(key)
	if err != nil {
		// Can't identify the holder → can't safely replace.
		return nil, locks.ErrContended
	}
	if holder.Host != b.host {
		// Cross-host Replace is out of scope (host scope only). The
		// caller will surface ErrContended → ExitLockContended (4).
		return nil, locks.ErrContended
	}
	if holder.PID == b.pid {
		// We're the holder of record but failed to acquire — likely a
		// stale sidecar. Treat as contended; the caller can retry next
		// fire when the OS has released our previous flock.
		return nil, locks.ErrContended
	}

	// SIGTERM the holder. ESRCH means the holder is already gone.
	killErr := b.killer.Kill(holder.PID, syscall.SIGTERM)
	if killErr != nil && !errors.Is(killErr, locks.ErrProcessNotFound) {
		return nil, fmt.Errorf("flock: SIGTERM holder pid=%d: %w", holder.PID, killErr)
	}

	// Poll for the holder to release. The kernel drops the flock when
	// the previous process exits, so a successful TryLock confirms the
	// holder has fully released.
	deadline := time.Now().Add(waitForExit)
	for {
		retry, err := b.Acquire(ctx, key, ttl)
		if err == nil {
			return retry, nil
		}
		if !errors.Is(err, locks.ErrContended) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, locks.ErrContended
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// makeHandle records the holder file and constructs a Handle. The
// holder file is best-effort — failure to write it does not fail the
// acquire (the lock is the source of truth).
func (b *Backend) makeHandle(fl *gofrsflock.Flock, key string) (locks.Handle, error) {
	holderPath := b.holderPath(key)
	contents := fmt.Sprintf("pid=%d\nhost=%s\n", b.pid, b.host)
	_ = os.WriteFile(holderPath, []byte(contents), 0o644)
	return &handle{fl: fl, holderPath: holderPath}, nil
}

func (b *Backend) holderPath(key string) string {
	return filepath.Join(b.dir, key+".holder")
}

// readHolder parses the sidecar `<key>.holder` file written at Acquire
// time. Returns ErrContended if the file is missing or malformed; the
// caller treats either as "can't safely Replace."
func (b *Backend) readHolder(key string) (locks.Holder, error) {
	raw, err := os.ReadFile(b.holderPath(key))
	if err != nil {
		return locks.Holder{}, err
	}
	var h locks.Holder
	for line := range strings.SplitSeq(string(raw), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "pid":
			pid, perr := strconv.Atoi(strings.TrimSpace(v))
			if perr != nil {
				return locks.Holder{}, fmt.Errorf("flock: holder pid: %w", perr)
			}
			h.PID = pid
		case "host":
			h.Host = strings.TrimSpace(v)
		}
	}
	if h.PID == 0 {
		return locks.Holder{}, errors.New("flock: holder file missing pid")
	}
	return h, nil
}

func validateKey(key string) error {
	if key == "" {
		return errors.New("flock: key must be non-empty")
	}
	if strings.ContainsAny(key, `/\`+"\x00") {
		return fmt.Errorf("flock: key contains illegal characters: %q", key)
	}
	return nil
}

type handle struct {
	mu         sync.Mutex
	fl         *gofrsflock.Flock
	holderPath string
	released   bool
}

func (h *handle) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return nil
	}
	h.released = true
	// Best-effort holder-file cleanup; the lock release is the source of truth.
	if h.holderPath != "" {
		_ = os.Remove(h.holderPath)
	}
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
