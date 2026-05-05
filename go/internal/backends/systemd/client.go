package systemd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// SystemctlExecutor runs `systemctl` invocations against the host.
// Production uses defaultSystemctl (shell-out); tests pass a recorder
// that captures argv for assertions without spawning processes.
type SystemctlExecutor interface {
	Run(ctx context.Context, args ...string) error
}

// JournalctlExecutor runs `journalctl` and returns its raw stdout.
// Used by Backend.History; injectable so tests can feed canned journal
// records without a real journald.
type JournalctlExecutor interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// defaultSystemctl shells out to the system `systemctl` binary on the
// host PATH. Errors include the failed argv and any stderr captured
// from the process so operator-facing messages identify the broken
// step (eg. `enable --now cronix-billing-reconcile-0.timer`).
type defaultSystemctl struct{}

func (defaultSystemctl) Run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...) //#nosec G204 — args are constructed internally
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// defaultJournalctl shells out to the system `journalctl` binary.
type defaultJournalctl struct{}

func (defaultJournalctl) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "journalctl", args...) //#nosec G204 — args are constructed internally
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("journalctl %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
