// Package crontab is the crontab backend adapter.
//
// Each cronix-managed entry is a 2-line block:
//
//	*/15 * * * * /usr/local/bin/cronix trigger billing.reconcile-payments
//	# cronix:owned app=billing job=reconcile-payments hash=abc123def idx=0
//
// The reconciler reads the crontab, finds owned blocks, computes a diff,
// and writes the file atomically. Lines without a cronix ownership marker
// are preserved untouched (D-026).
//
// File layout:
//
//	policy.go    backend-local conventions (DefaultPath, ownerMarker)
//	parse.go     ownership regex + line parser + stripOwnedFor
//	cron.go      schedule expression → 5-field cron translator
//	render.go    produces the 2-line blocks
//	client.go    JournalctlExecutor + file I/O (read/atomicWrite)
//	backend.go   Backend type + Options + Backend interface methods
package crontab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gofrs/flock"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/backends/historyutil"
	"github.com/awbx/cronix/go/internal/manifest"
)

// Backend reads and writes a single crontab file.
type Backend struct {
	path       string
	triggerBin string
	lockPath   string
	journalctl JournalctlExecutor
}

// Options for constructing a Backend.
type Options struct {
	// Path is the crontab file. Defaults to DefaultPath.
	Path string
	// TriggerBin is the absolute path to the cronix binary. Required.
	TriggerBin string
	// LockPath is the file used as the apply-time write mutex. Defaults
	// to <Path><lockSuffix>.
	LockPath string
	// Journalctl runs journalctl invocations for History. Defaults to
	// os/exec; History returns nil silently when journalctl is missing
	// (BSDs / macOS, classic syslog-only Linux).
	Journalctl JournalctlExecutor
}

// New constructs a Backend.
func New(opts Options) (*Backend, error) {
	if opts.Path == "" {
		opts.Path = DefaultPath
	}
	if opts.TriggerBin == "" {
		return nil, fmt.Errorf("crontab: TriggerBin is required")
	}
	if opts.LockPath == "" {
		opts.LockPath = opts.Path + lockSuffix
	}
	jx := opts.Journalctl
	if jx == nil {
		jx = defaultJournalctl{}
	}
	return &Backend{path: opts.Path, triggerBin: opts.TriggerBin, lockPath: opts.LockPath, journalctl: jx}, nil
}

// Name returns "crontab".
func (*Backend) Name() string { return "crontab" }

// List enumerates owned entries.
func (b *Backend) List(_ context.Context) ([]backends.ManagedEntry, error) {
	_, lines, err := b.readFile()
	if err != nil {
		return nil, err
	}
	out := make([]backends.ManagedEntry, 0)
	for i, line := range lines {
		m := ownerLineRe.FindStringSubmatch(line)
		if m == nil || i == 0 {
			continue
		}
		idx, _ := strconv.Atoi(m[ownerLineRe.SubexpIndex("idx")])
		out = append(out, backends.ManagedEntry{
			App:   m[ownerLineRe.SubexpIndex("app")],
			Job:   m[ownerLineRe.SubexpIndex("job")],
			Hash:  m[ownerLineRe.SubexpIndex("hash")],
			Index: idx,
			Raw:   lines[i-1],
		})
	}
	return out, nil
}

// Create installs every schedule for (app, job) as 2-line blocks.
func (b *Backend) Create(_ context.Context, app string, job manifest.NormalizedJob) error {
	if !appRe.MatchString(app) {
		return fmt.Errorf("crontab: invalid app id %q", app)
	}
	if !jobRe.MatchString(job.Name) {
		return fmt.Errorf("crontab: invalid job name %q", job.Name)
	}
	return b.rewrite(func(lines []string) []string {
		return append(lines, render(app, b.triggerBin, job)...)
	})
}

// Update replaces all owned blocks for (app, job.Name) with the freshly
// rendered form. Falls back to Create when no entries exist yet.
func (b *Backend) Update(_ context.Context, app string, job manifest.NormalizedJob) error {
	if !appRe.MatchString(app) {
		return fmt.Errorf("crontab: invalid app id %q", app)
	}
	return b.rewrite(func(lines []string) []string {
		filtered := stripOwnedFor(lines, app, job.Name)
		return append(filtered, render(app, b.triggerBin, job)...)
	})
}

// Delete removes all owned blocks for (app, jobName).
func (b *Backend) Delete(_ context.Context, app, jobName string) error {
	if !appRe.MatchString(app) {
		return fmt.Errorf("crontab: invalid app id %q", app)
	}
	return b.rewrite(func(lines []string) []string {
		return stripOwnedFor(lines, app, jobName)
	})
}

// Validate reports whether the job can be expressed in 5-field cron.
func (*Backend) Validate(job manifest.NormalizedJob) backends.ValidationResult {
	var issues []string
	for i, s := range job.Schedules {
		if _, ok := translate(s); !ok {
			issues = append(issues, fmt.Sprintf("schedules[%d]: cannot translate %q to 5-field crontab", i, s))
		}
	}
	if job.Timezone != "UTC" {
		issues = append(issues, fmt.Sprintf("crontab does not support per-job timezone (%q); fire times will be in the system timezone", job.Timezone))
	}
	return backends.ValidationResult{OK: len(issues) == 0, Issues: issues}
}

// History reads journald for `cronix trigger` invocations launched by
// crond. Modern Linux distros route cron stdout to journald via the
// SYSLOG_IDENTIFIER=cronix tag (since the trigger binary is named
// "cronix"). The shim emits one slog-JSON record per attempt; History
// folds them into one entry per terminal run.
//
// On systems without journalctl (BSDs / macOS / classic syslog-only
// Linux) this returns an empty list rather than erroring — `cronix
// list` still works, and operators can always tail their cron mail
// spool by hand.
func (b *Backend) History(ctx context.Context, opts backends.HistoryOpts) ([]backends.HistoryEntry, error) {
	if opts.App == "" || opts.Job == "" {
		return nil, fmt.Errorf("crontab: history requires App + Job")
	}
	args := []string{
		"--output=json",
		"--no-pager",
		historySyslogIdentifier,
	}
	if !opts.Since.IsZero() {
		args = append(args, "--since", opts.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	if !opts.Until.IsZero() {
		args = append(args, "--until", opts.Until.UTC().Format("2006-01-02 15:04:05"))
	}
	raw, err := b.journalctl.Run(ctx, args...)
	if err != nil {
		// journalctl missing is the common case on macOS / BSDs / older
		// Linux without journald; degrade gracefully rather than failing
		// `cronix history` on those hosts.
		return nil, nil //nolint:nilerr // intentional graceful degradation
	}
	entries := historyutil.FoldShimLogs(raw, opts.App, opts.Job, "journald", opts.Status)
	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[len(entries)-opts.Limit:]
	}
	return entries, nil
}

// Ensure verifies the crontab file exists and is writable.
func (b *Backend) Ensure(_ context.Context) error {
	dir := filepath.Dir(b.path)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("crontab: dir %s: %w", dir, err)
	}
	f, err := os.OpenFile(b.path, os.O_RDWR|os.O_CREATE, 0o644) //#nosec G304 — operator-managed
	if err != nil {
		return fmt.Errorf("crontab: open %s: %w", b.path, err)
	}
	return f.Close()
}

// rewrite acquires the apply-lock, applies fn, writes atomically.
func (b *Backend) rewrite(fn func([]string) []string) error {
	fl := flock.New(b.lockPath)
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("crontab: lock %s: %w", b.lockPath, err)
	}
	defer fl.Unlock()

	_, lines, err := b.readFile()
	if err != nil {
		return err
	}
	updated := fn(lines)
	return atomicWrite(b.path, updated)
}

var _ backends.Backend = (*Backend)(nil)
