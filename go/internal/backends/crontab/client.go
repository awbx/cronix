package crontab

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// JournalctlExecutor runs `journalctl` and returns its raw stdout. Used
// by Backend.History; injectable so tests can feed canned journal
// records without a real journald.
type JournalctlExecutor interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// defaultJournalctl shells out to the system `journalctl` binary on the
// host PATH. Errors are returned verbatim — the caller (History)
// degrades gracefully when journalctl is missing.
type defaultJournalctl struct{}

func (defaultJournalctl) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "journalctl", args...) //#nosec G204 — args are constructed internally
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("journalctl %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// readFile reads the crontab. Missing file is not an error — we report
// an empty crontab so a fresh host's first apply just appends.
func (b *Backend) readFile() ([]rawEntry, []string, error) {
	f, err := os.Open(b.path) //#nosec G304 — operator-managed
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("crontab: open %s: %w", b.path, err)
	}
	defer f.Close()
	return parseLines(f)
}

// atomicWrite writes lines to path via a temp-file-and-rename so a
// crash mid-write cannot leave a half-written crontab. The temp file
// is created in the same directory as the target so the rename stays
// on the same filesystem.
func atomicWrite(path string, lines []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cronix-crontab-*")
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}
	w := bufio.NewWriter(tmp)
	for _, line := range lines {
		if _, err := w.WriteString(line); err != nil {
			cleanup()
			return err
		}
		if _, err := w.WriteString("\n"); err != nil {
			cleanup()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
