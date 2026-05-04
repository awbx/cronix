// Package locks defines the Lock interface used by the trigger shim to
// enforce per-job concurrency policies (D-009 / D-010), plus its built-in
// implementations: flock-based for `concurrency_scope: host` and Redis-based
// for `concurrency_scope: global`.
package locks

import (
	"context"
	"errors"
	"time"
)

// ErrContended is returned by Acquire when the lock is held by another
// caller and the configured wait policy gives up. The trigger shim
// translates this into exit code 4 (transient — scheduler should not alarm).
var ErrContended = errors.New("locks: contended")

// Lock is a single-key distributed (or local) mutex factory.
type Lock interface {
	// Acquire takes the lock for `key`. Implementations MAY block until
	// `ctx` is cancelled or return ErrContended immediately on contention,
	// depending on their policy. `ttl` is the maximum lifetime of the
	// lock; the trigger shim sets it to `policy.timeout_seconds + headroom`.
	//
	// Returns a Handle that the caller MUST release in a defer. Implementations
	// MUST NOT leak the lock if the holder crashes — flock is held on the OS
	// process and released by the kernel on exit; Redis impls use TTLs.
	Acquire(ctx context.Context, key string, ttl time.Duration) (Handle, error)

	// Name returns the implementation identifier ("flock", "redis", ...).
	Name() string
}

// Handle is an acquired lock. Release MUST be idempotent.
type Handle interface {
	// Release returns the lock. After Release, Refresh is a no-op.
	Release() error

	// Refresh extends the lock's TTL. For implementations whose locks do
	// not expire (flock), Refresh is a no-op. For Redis-style impls,
	// long-running handlers should call Refresh periodically or pass a
	// generous TTL up front.
	Refresh(ctx context.Context) error
}
