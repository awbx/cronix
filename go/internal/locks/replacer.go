package locks

import (
	"context"
	"errors"
	"os"
	"syscall"
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

// DefaultKiller sends the signal via syscall.Kill on Unix; the no-op
// fallback on platforms without it. Trigger shim wires this to the real
// killer at boot.
type DefaultKiller struct{}

// Kill sends the signal using `os/syscall`. A non-running PID surfaces as
// ErrProcessNotFound; the caller treats that as "holder already gone."
func (DefaultKiller) Kill(pid int, sig os.Signal) error {
	if pid <= 0 {
		return errors.New("locks: invalid PID")
	}
	syscallSig, ok := sig.(syscall.Signal)
	if !ok {
		return errors.New("locks: signal is not a syscall.Signal")
	}
	if err := syscall.Kill(pid, syscallSig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return ErrProcessNotFound
		}
		return err
	}
	return nil
}

// ErrProcessNotFound is returned by a Killer when the target PID is no
// longer running (ESRCH). Replace treats this as "holder already gone,
// proceed to acquire."
var ErrProcessNotFound = errors.New("locks: process not found")
