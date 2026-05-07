//go:build windows

package locks

import "os"

// killProcess on Windows is a no-op that returns ErrReplaceNotSupported.
// The Replace concurrency policy is documented as a Unix host-local
// feature (RFC §Trigger Shim Behavior); the AcquireOrReplace dance
// surfaces this as ErrContended, identical to how Forbid behaves.
//
// Genuine Windows process termination would use OpenProcess +
// TerminateProcess, but TerminateProcess is forceful (no graceful
// shutdown signal), which violates Replace's "send SIGTERM, wait for
// the holder to exit cleanly" contract.
func killProcess(_ int, _ os.Signal) error {
	return ErrReplaceNotSupported
}
