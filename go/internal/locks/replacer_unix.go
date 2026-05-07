//go:build !windows

package locks

import (
	"errors"
	"os"
	"syscall"
)

// killProcess delivers `sig` to `pid` via syscall.Kill on Unix.
// Translates ESRCH (no such process) into ErrProcessNotFound so the
// AcquireOrReplace dance treats it as "holder already gone — proceed
// to acquire" rather than a hard failure.
func killProcess(pid int, sig os.Signal) error {
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
