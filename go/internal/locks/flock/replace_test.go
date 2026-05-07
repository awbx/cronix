// Tests for the Replace concurrency policy (D-009 / D-010 / D-023).
//
// We can't actually fork a Go subprocess in unit tests cheaply, so we
// inject a fake locks.Killer that fires a callback (releasing the
// previous Backend's handle) instead of sending a real SIGTERM. That
// lets us exercise every branch — happy path, non-local holder,
// holder-refuses-to-exit — deterministically inside one process.
package flock

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/awbx/cronix/go/internal/locks"
)

// fakeKiller invokes onKill when Kill is called. The test orchestrates
// what onKill does (release the previous handle, no-op, etc.).
type fakeKiller struct {
	mu     sync.Mutex
	calls  []killCall
	onKill func(pid int, sig os.Signal) error
}

type killCall struct {
	pid int
	sig os.Signal
}

func (k *fakeKiller) Kill(pid int, sig os.Signal) error {
	k.mu.Lock()
	k.calls = append(k.calls, killCall{pid: pid, sig: sig})
	cb := k.onKill
	k.mu.Unlock()
	if cb != nil {
		return cb(pid, sig)
	}
	return nil
}

func (k *fakeKiller) records() []killCall {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]killCall(nil), k.calls...)
}

func TestReplace_LocalHolderTerminatedSuccessfully(t *testing.T) {
	dir := t.TempDir()
	const key = "billing.reconcile-payments"

	// Holder backend pretends to be PID 1001 on host "node-1".
	holder, err := New(dir, WithHost("node-1"), WithPID(1001))
	if err != nil {
		t.Fatalf("holder New: %v", err)
	}
	holderHandle, err := holder.Acquire(t.Context(), key, time.Minute)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}

	// Replacer backend is the same host but a different PID. Killer
	// callback simulates the holder honouring SIGTERM by releasing.
	released := make(chan struct{})
	killer := &fakeKiller{
		onKill: func(_ int, _ os.Signal) error {
			go func() {
				_ = holderHandle.Release()
				close(released)
			}()
			return nil
		},
	}
	replacer, err := New(dir, WithHost("node-1"), WithPID(2002), WithKiller(killer))
	if err != nil {
		t.Fatalf("replacer New: %v", err)
	}

	h, err := replacer.AcquireOrReplace(t.Context(), key, time.Minute, 2*time.Second)
	if err != nil {
		t.Fatalf("AcquireOrReplace: %v", err)
	}
	defer h.Release()

	// Killer was called exactly once with SIGTERM and the holder PID.
	calls := killer.records()
	if len(calls) != 1 {
		t.Fatalf("expected 1 kill call, got %d", len(calls))
	}
	if calls[0].pid != 1001 {
		t.Errorf("kill pid: got %d, want 1001", calls[0].pid)
	}
	if calls[0].sig != syscall.SIGTERM {
		t.Errorf("kill sig: got %v, want SIGTERM", calls[0].sig)
	}

	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("holder release goroutine never fired")
	}
}

func TestReplace_NonLocalHolder_ReturnsContended(t *testing.T) {
	dir := t.TempDir()
	const key = "billing.reconcile-payments"

	holder, err := New(dir, WithHost("node-1"), WithPID(1001))
	if err != nil {
		t.Fatalf("holder New: %v", err)
	}
	hh, err := holder.Acquire(t.Context(), key, time.Minute)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	defer hh.Release()

	// Replacer claims to be on a different host.
	killer := &fakeKiller{}
	replacer, err := New(dir, WithHost("node-2"), WithPID(2002), WithKiller(killer))
	if err != nil {
		t.Fatalf("replacer New: %v", err)
	}

	_, err = replacer.AcquireOrReplace(t.Context(), key, time.Minute, time.Second)
	if !errors.Is(err, locks.ErrContended) {
		t.Fatalf("expected ErrContended for non-local holder, got %v", err)
	}
	if got := killer.records(); len(got) != 0 {
		t.Errorf("expected zero kill calls for non-local holder, got %d", len(got))
	}
}

func TestReplace_HolderRefusesToExit_ReturnsContendedAfterWait(t *testing.T) {
	dir := t.TempDir()
	const key = "billing.reconcile-payments"

	holder, err := New(dir, WithHost("node-1"), WithPID(1001))
	if err != nil {
		t.Fatalf("holder New: %v", err)
	}
	hh, err := holder.Acquire(t.Context(), key, time.Minute)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	defer hh.Release()

	// Killer is called but the holder never releases.
	killer := &fakeKiller{onKill: func(_ int, _ os.Signal) error { return nil }}
	replacer, err := New(dir, WithHost("node-1"), WithPID(2002), WithKiller(killer))
	if err != nil {
		t.Fatalf("replacer New: %v", err)
	}

	start := time.Now()
	_, err = replacer.AcquireOrReplace(t.Context(), key, time.Minute, 200*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, locks.ErrContended) {
		t.Fatalf("expected ErrContended after wait, got %v", err)
	}
	// Should have waited roughly the configured wait window before giving up.
	if elapsed < 150*time.Millisecond {
		t.Errorf("returned too quickly (%v) — expected to wait at least 150ms", elapsed)
	}
}

func TestReplace_HolderAlreadyDead_ProceedsAfterESRCH(t *testing.T) {
	dir := t.TempDir()
	const key = "billing.reconcile-payments"

	// Holder backend takes the lock and pins itself as PID 9999.
	holder, err := New(dir, WithHost("node-1"), WithPID(9999))
	if err != nil {
		t.Fatalf("holder New: %v", err)
	}
	holderHandle, err := holder.Acquire(t.Context(), key, time.Minute)
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}

	// Replacer's killer reports ESRCH ("PID not found"); a real OS would
	// have already released the flock when the process died, so we
	// simulate that by releasing the holder's lock from a goroutine.
	released := make(chan struct{})
	killer := &fakeKiller{onKill: func(_ int, _ os.Signal) error {
		go func() {
			_ = holderHandle.Release()
			close(released)
		}()
		return locks.ErrProcessNotFound
	}}
	replacer, err := New(dir, WithHost("node-1"), WithPID(2002), WithKiller(killer))
	if err != nil {
		t.Fatalf("replacer New: %v", err)
	}

	h, err := replacer.AcquireOrReplace(t.Context(), key, time.Minute, time.Second)
	if err != nil {
		t.Fatalf("AcquireOrReplace: %v", err)
	}
	defer h.Release()
	if got := killer.records(); len(got) != 1 {
		t.Errorf("expected 1 kill call (ESRCH path), got %d", len(got))
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("holder release goroutine never fired")
	}
}

func TestReplace_FreeLock_NoKillNeeded(t *testing.T) {
	dir := t.TempDir()
	const key = "billing.reconcile-payments"

	killer := &fakeKiller{}
	replacer, err := New(dir, WithHost("node-1"), WithPID(2002), WithKiller(killer))
	if err != nil {
		t.Fatalf("replacer New: %v", err)
	}

	h, err := replacer.AcquireOrReplace(t.Context(), key, time.Minute, time.Second)
	if err != nil {
		t.Fatalf("AcquireOrReplace on free lock: %v", err)
	}
	defer h.Release()
	if got := killer.records(); len(got) != 0 {
		t.Errorf("expected zero kill calls on free lock, got %d", len(got))
	}
}

func TestReplace_HolderFileWrittenAndCleaned(t *testing.T) {
	dir := t.TempDir()
	const key = "billing.reconcile-payments"

	b, err := New(dir, WithHost("node-1"), WithPID(1001))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	holderPath := dir + "/" + key + ".holder"
	if _, err := os.Stat(holderPath); !os.IsNotExist(err) {
		t.Fatalf("holder file should not exist before acquire")
	}

	h, err := b.Acquire(t.Context(), key, time.Minute)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := os.Stat(holderPath); err != nil {
		t.Fatalf("holder file missing after acquire: %v", err)
	}
	if err := h.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(holderPath); !os.IsNotExist(err) {
		t.Errorf("holder file should be removed on release, stat err=%v", err)
	}
}

// Verify Replacer is a real interface implementation, not a duck-typed match.
var _ locks.Replacer = (*Backend)(nil)
