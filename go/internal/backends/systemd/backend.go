// Package systemd is the systemd-timer backend adapter.
//
// Per-job artifacts:
//
//	<unitDir>/cronix-<app>-<job>-<idx>.timer
//	<unitDir>/cronix-<app>-<job>-<idx>.service
//
// Ownership lives in `X-Cronix-*` annotations inside the unit files —
// systemd ignores `X-` prefixed fields, so we use them as our marker.
// See policy.go for the annotation set; render.go for the unit shape;
// parse.go for ownership detection.
//
// The reconciler invokes `systemctl daemon-reload`, `enable --now`, and
// `disable --now` via SystemctlExecutor (client.go) so tests can drive
// the backend without an actual init system.
package systemd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/backends/historyutil"
	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/policy"
)

// Backend writes systemd timer + service unit pairs and drives systemctl.
type Backend struct {
	unitDir    string
	triggerBin string
	systemctl  SystemctlExecutor
	journalctl JournalctlExecutor
}

// Options for constructing a Backend.
type Options struct {
	// UnitDir defaults to DefaultUnitDir.
	UnitDir string
	// TriggerBin is the absolute path to the cronix binary on the host. Required.
	TriggerBin string
	// Systemctl runs `systemctl ...` invocations. Defaults to os/exec
	// against the system PATH.
	Systemctl SystemctlExecutor
	// Journalctl runs `journalctl ...` invocations. Defaults to os/exec.
	Journalctl JournalctlExecutor
}

// New constructs a Backend.
func New(opts Options) (*Backend, error) {
	if opts.UnitDir == "" {
		opts.UnitDir = DefaultUnitDir
	}
	if opts.TriggerBin == "" {
		return nil, fmt.Errorf("systemd: TriggerBin is required")
	}
	sx := opts.Systemctl
	if sx == nil {
		sx = defaultSystemctl{}
	}
	jx := opts.Journalctl
	if jx == nil {
		jx = defaultJournalctl{}
	}
	return &Backend{unitDir: opts.UnitDir, triggerBin: opts.TriggerBin, systemctl: sx, journalctl: jx}, nil
}

// Name returns "systemd-timer".
func (*Backend) Name() string { return "systemd-timer" }

// List enumerates owned timer units. The reported Hash is the
// X-Cronix-Hash annotation, but if the actual `OnCalendar=` line in
// the [Timer] section no longer matches the X-Cronix-OnCalendar
// annotation, policy.DriftSpecEdited is returned instead — so a manual
// edit to OnCalendar surfaces in `cronix drift` even when the operator
// left the hash annotation alone.
func (b *Backend) List(_ context.Context) ([]backends.ManagedEntry, error) {
	matches, err := filepath.Glob(filepath.Join(b.unitDir, policy.SchedulePrefix+"*.timer"))
	if err != nil {
		return nil, fmt.Errorf("systemd: glob %s: %w", b.unitDir, err)
	}
	out := make([]backends.ManagedEntry, 0, len(matches))
	for _, p := range matches {
		raw, err := os.ReadFile(p) //#nosec G304 — operator-managed dir
		if err != nil {
			return nil, fmt.Errorf("systemd: read %s: %w", p, err)
		}
		body := string(raw)
		entry, ok := parseUnit(body)
		if !ok {
			continue
		}
		if want := firstMatch(canonicalCalendarRe, body); want != "" {
			actual := firstMatch(actualCalendarRe, body)
			if actual != "" && actual != want {
				entry.Hash = policy.DriftSpecEdited
			}
		}
		entry.Raw = p
		out = append(out, entry)
	}
	return out, nil
}

// Create installs (timer, service) pairs for every schedule of `job`.
func (b *Backend) Create(ctx context.Context, app string, job manifest.NormalizedJob) error {
	if err := validateName(app); err != nil {
		return fmt.Errorf("systemd: app id: %w", err)
	}
	if err := validateName(job.Name); err != nil {
		return fmt.Errorf("systemd: job name: %w", err)
	}
	for i := range job.Schedules {
		hash := policy.Hash(job, i)
		timerFile, serviceFile, err := renderUnitsWithHash(b.triggerBin, app, job, i, hash)
		if err != nil {
			return err
		}
		unit := policy.ScheduleName(app, job.Name, i)
		if err := b.writeFile(unit+".timer", timerFile); err != nil {
			return err
		}
		if err := b.writeFile(unit+".service", serviceFile); err != nil {
			return err
		}
	}
	if err := b.systemctl.Run(ctx, "daemon-reload"); err != nil {
		return fmt.Errorf("systemd: daemon-reload: %w", err)
	}
	for i := range job.Schedules {
		unit := policy.ScheduleName(app, job.Name, i) + ".timer"
		if err := b.systemctl.Run(ctx, "enable", "--now", unit); err != nil {
			return fmt.Errorf("systemd: enable %s: %w", unit, err)
		}
	}
	return nil
}

// Update replaces all owned units for (app, job.Name). Implemented as
// Delete + Create so the reload-and-restart cycle is identical to a
// fresh install.
func (b *Backend) Update(ctx context.Context, app string, job manifest.NormalizedJob) error {
	if err := b.Delete(ctx, app, job.Name); err != nil {
		return err
	}
	return b.Create(ctx, app, job)
}

// Delete disables and removes every owned (timer, service) pair for (app, jobName).
func (b *Backend) Delete(ctx context.Context, app, jobName string) error {
	if err := validateName(app); err != nil {
		return fmt.Errorf("systemd: app id: %w", err)
	}
	if err := validateName(jobName); err != nil {
		return fmt.Errorf("systemd: job name: %w", err)
	}
	prefix := policy.SchedulePrefix + app + "-" + jobName + "-"
	matches, err := filepath.Glob(filepath.Join(b.unitDir, prefix+"*.timer"))
	if err != nil {
		return fmt.Errorf("systemd: glob: %w", err)
	}
	for _, timerPath := range matches {
		timerName := filepath.Base(timerPath)
		if err := b.systemctl.Run(ctx, "disable", "--now", timerName); err != nil {
			return fmt.Errorf("systemd: disable %s: %w", timerName, err)
		}
		servicePath := strings.TrimSuffix(timerPath, ".timer") + ".service"
		if err := os.Remove(timerPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("systemd: remove %s: %w", timerPath, err)
		}
		if err := os.Remove(servicePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("systemd: remove %s: %w", servicePath, err)
		}
	}
	if len(matches) > 0 {
		if err := b.systemctl.Run(ctx, "daemon-reload"); err != nil {
			return fmt.Errorf("systemd: daemon-reload: %w", err)
		}
	}
	return nil
}

// Validate maps the schedule to OnCalendar= and reports any unsupported
// patterns.
func (*Backend) Validate(job manifest.NormalizedJob) backends.ValidationResult {
	var issues []string
	for i, s := range job.Schedules {
		if _, err := translateOnCalendar(s); err != nil {
			issues = append(issues, fmt.Sprintf("schedules[%d]: %v", i, err))
		}
	}
	return backends.ValidationResult{OK: len(issues) == 0, Issues: issues}
}

// History reads journalctl for the units owned by (App, Job) and folds
// per-attempt log lines into one HistoryEntry per terminal run. Records
// older than HistoryOpts.Since are skipped; Limit truncates from the
// most recent.
//
// The trigger shim emits structured slog JSON; History parses each
// journald record's MESSAGE field as JSON and reassembles status from
// the message tag (`trigger: success`, `trigger: app rejected`,
// `trigger: retries exhausted`, `trigger: lock contended`).
func (b *Backend) History(ctx context.Context, opts backends.HistoryOpts) ([]backends.HistoryEntry, error) {
	if opts.App == "" || opts.Job == "" {
		return nil, fmt.Errorf("systemd: history requires App + Job")
	}
	args := []string{
		"--output=json",
		"--no-pager",
		"--unit", fmt.Sprintf("%s%s-%s-*.service", policy.SchedulePrefix, opts.App, opts.Job),
	}
	if !opts.Since.IsZero() {
		args = append(args, "--since", opts.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	if !opts.Until.IsZero() {
		args = append(args, "--until", opts.Until.UTC().Format("2006-01-02 15:04:05"))
	}
	raw, err := b.journalctl.Run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("systemd: journalctl: %w", err)
	}
	entries := historyutil.FoldShimLogs(raw, opts.App, opts.Job, "journald", opts.Status)
	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[len(entries)-opts.Limit:]
	}
	return entries, nil
}

// Ensure verifies systemd is the init system and the unit dir exists.
func (b *Backend) Ensure(_ context.Context) error {
	if _, err := os.Stat(systemdRunDir); err != nil {
		return fmt.Errorf("systemd: not running under systemd (%s: %w)", systemdRunDir, err)
	}
	if _, err := os.Stat(b.unitDir); err != nil {
		return fmt.Errorf("systemd: unit dir %s: %w", b.unitDir, err)
	}
	return nil
}

func (b *Backend) writeFile(name, contents string) error {
	if err := os.MkdirAll(b.unitDir, 0o755); err != nil {
		return fmt.Errorf("systemd: mkdir %s: %w", b.unitDir, err)
	}
	path := filepath.Join(b.unitDir, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil { //#nosec G306 — systemd reads world-readable units
		return fmt.Errorf("systemd: write %s: %w", path, err)
	}
	return nil
}

func validateName(s string) error {
	if s == "" {
		return fmt.Errorf("name is empty")
	}
	if len(s) > 63 {
		return fmt.Errorf("name %q too long (max 63)", s)
	}
	for i, r := range s {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		isHyphen := r == '-'
		if !isLower && !isDigit && !isHyphen {
			return fmt.Errorf("name %q has invalid char at %d", s, i)
		}
	}
	return nil
}

var _ backends.Backend = (*Backend)(nil)
