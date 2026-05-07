package locks

import (
	"context"
	"errors"
	"os"
	"time"
)

// Replacer is the optional capability a Lock implementation MAY ship to
// support the `Replace` concurrency policy (RFC §Trigger Shim Behavior;
// D-009 / D-010 / D-023). When the lock is contended, AcquireOrReplace
// identifies the current holder and SIGTERMs it (best-effort, host-local
// only), waits for the holder to exit, then retries acquire.
//
// Implementations MUST be safe to call concurrently with Acquire; the
// retry loop relies on Acquire returning ErrContended deterministically.
type Replacer interface {
	// AcquireOrReplace acquires the lock, replacing the previous holder if
	// the lock is contended and the holder lives on this host. Returns:
	//   - Handle (lock acquired) on success
	//   - ErrContended if the previous holder is non-local, refuses to
	//     exit within waitForExit, or the holder identity cannot be read.
	AcquireOrReplace(ctx context.Context, key string, ttl, waitForExit time.Duration) (Handle, error)
}

// AcquireOrReplace runs the Replace dance against `lock`. If the lock
// implements Replacer it delegates; otherwise it falls back to a plain
// Acquire whose contended outcome surfaces as ErrContended.
//
// Use this from the trigger shim instead of branching on the concrete
// lock type.
func AcquireOrReplace(ctx context.Context, lock Lock, key string, ttl, waitForExit time.Duration) (Handle, error) {
	if r, ok := lock.(Replacer); ok {
		return r.AcquireOrReplace(ctx, key, ttl, waitForExit)
	}
	return lock.Acquire(ctx, key, ttl)
}

// Holder describes a lock holder. Replacer implementations write one of
// these alongside their lock state and read it on contention.
type Holder struct {
	PID  int
	Host string
}

// Killer signals a process. Decoupled so tests can inject a no-op
// implementation. The default is SIGTERM via the OS.
type Killer interface {
	Kill(pid int, sig os.Signal) error
}

// DefaultKiller sends the signal via syscall.Kill on Unix. On Windows
// the Replace policy is documented as a Unix host-local feature (RFC
// §Trigger Shim Behavior), so the kill is a no-op that returns
// ErrReplaceNotSupported — AcquireOrReplace then surfaces ErrContended
// to the caller, identical to the Forbid policy. Trigger shim wires
// this to the real killer at boot.
type DefaultKiller struct{}

// Kill sends `sig` to `pid`. On Unix this calls syscall.Kill. On
// Windows it returns ErrReplaceNotSupported. A non-running PID surfaces
// as ErrProcessNotFound; the caller treats that as "holder already gone."
func (DefaultKiller) Kill(pid int, sig os.Signal) error {
	if pid <= 0 {
		return errors.New("locks: invalid PID")
	}
	return killProcess(pid, sig)
}

// ErrProcessNotFound is returned by a Killer when the target PID is no
// longer running (ESRCH). Replace treats this as "holder already gone,
// proceed to acquire."
var ErrProcessNotFound = errors.New("locks: process not found")

// ErrReplaceNotSupported is returned by DefaultKiller.Kill on Windows
// (and any other platform without a SIGTERM-style API). Surfaced through
// AcquireOrReplace as ErrContended.
var ErrReplaceNotSupported = errors.New("locks: Replace policy not supported on this platform")
