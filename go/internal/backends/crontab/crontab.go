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
package crontab

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofrs/flock"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/backends/historyutil"
	"github.com/awbx/cronix/go/internal/manifest"
)

// JournalctlExecutor runs `journalctl` and returns its raw output. Used
// by History; injectable so tests can feed canned journal records.
type JournalctlExecutor interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// Backend reads and writes a single crontab file.
type Backend struct {
	path       string
	triggerBin string
	lockPath   string
	journalctl JournalctlExecutor
}

// Options for constructing a Backend.
type Options struct {
	// Path is the crontab file. Defaults to /etc/crontab.
	Path string
	// TriggerBin is the absolute path to the cronix binary. Required.
	TriggerBin string
	// LockPath is the file used as the apply-time write mutex. Defaults
	// to <Path>.cronix.lock.
	LockPath string
	// Journalctl runs journalctl invocations for History. Defaults to
	// os/exec; History returns nil silently when journalctl is missing
	// (BSDs / macOS, classic syslog-only Linux).
	Journalctl JournalctlExecutor
}

// New constructs a Backend.
func New(opts Options) (*Backend, error) {
	if opts.Path == "" {
		opts.Path = "/etc/crontab"
	}
	if opts.TriggerBin == "" {
		return nil, fmt.Errorf("crontab: TriggerBin is required")
	}
	if opts.LockPath == "" {
		opts.LockPath = opts.Path + ".cronix.lock"
	}
	jx := opts.Journalctl
	if jx == nil {
		jx = defaultJournalctl{}
	}
	return &Backend{path: opts.Path, triggerBin: opts.TriggerBin, lockPath: opts.LockPath, journalctl: jx}, nil
}

// Name returns "crontab".
func (*Backend) Name() string { return "crontab" }

var (
	appRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	jobRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	// ownerLineRe matches a cronix ownership comment line.
	ownerLineRe = regexp.MustCompile(
		`^# cronix:owned app=(?P<app>[a-z][a-z0-9-]{0,62}) job=(?P<job>[a-z][a-z0-9-]{0,62}) hash=(?P<hash>[0-9a-f]{1,64}) idx=(?P<idx>\d+)$`,
	)
)

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
		"_COMM=cronix",
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

// readFile reads the crontab. Missing file is not an error — we report
// an empty crontab.
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

type rawEntry struct {
	scheduleLine string
	ownerLine    string
}

func parseLines(r io.Reader) ([]rawEntry, []string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		all     []string
		entries []rawEntry
		prev    string
	)
	for scanner.Scan() {
		line := scanner.Text()
		all = append(all, line)
		if ownerLineRe.MatchString(line) && prev != "" && !strings.HasPrefix(prev, "#") {
			entries = append(entries, rawEntry{scheduleLine: prev, ownerLine: line})
		}
		prev = line
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return entries, all, nil
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

// stripOwnedFor returns lines minus any 2-line owned block for (app, job).
func stripOwnedFor(lines []string, app, job string) []string {
	out := make([]string, 0, len(lines))
	for i := range lines {
		m := ownerLineRe.FindStringSubmatch(lines[i])
		if m != nil && m[1] == app && m[2] == job {
			// Drop the schedule line that immediately precedes this marker.
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, lines[i])
	}
	return out
}

// render produces the 2-line blocks for every schedule of `job`.
func render(app, triggerBin string, job manifest.NormalizedJob) []string {
	out := make([]string, 0, 2*len(job.Schedules))
	for i, sched := range job.Schedules {
		cron, ok := translate(sched)
		if !ok {
			// Validate() would have caught this; defensive skip to avoid
			// emitting a malformed crontab line.
			continue
		}
		hash := hashJobSchedule(job, i)
		out = append(out,
			fmt.Sprintf("%s %s trigger %s.%s", cron, triggerBin, app, job.Name),
			fmt.Sprintf("# cronix:owned app=%s job=%s hash=%s idx=%d", app, job.Name, hash, i),
		)
	}
	return out
}

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

// translate maps a manifest schedule to the 5-field cron form.
func translate(s string) (string, bool) {
	t := strings.TrimSpace(s)
	switch t {
	case "@hourly":
		return "0 * * * *", true
	case "@daily", "@midnight":
		return "0 0 * * *", true
	case "@weekly":
		return "0 0 * * 0", true
	case "@monthly":
		return "0 0 1 * *", true
	case "@yearly", "@annually":
		return "0 0 1 1 *", true
	}
	if rest, ok := strings.CutPrefix(t, "@every"); ok {
		rest = strings.TrimSpace(rest)
		if len(rest) < 2 {
			return "", false
		}
		unit := rest[len(rest)-1]
		num, err := strconv.Atoi(rest[:len(rest)-1])
		if err != nil || num <= 0 {
			return "", false
		}
		switch unit {
		case 'm':
			if num < 60 && 60%num == 0 {
				return fmt.Sprintf("*/%d * * * *", num), true
			}
		case 'h':
			if num < 24 && 24%num == 0 {
				return fmt.Sprintf("0 */%d * * *", num), true
			}
		}
		return "", false
	}
	if len(strings.Fields(t)) == 5 {
		return t, true
	}
	return "", false
}

// hashJobSchedule produces a 16-char change-detection hash for one
// (job, scheduleIndex). It uses the canonical normalized-manifest
// serialization plus an FNV-1a folding over (bytes ⨁ index).
func hashJobSchedule(job manifest.NormalizedJob, idx int) string {
	b, _ := manifest.Canonicalize(&manifest.NormalizedManifest{
		Version: 1,
		App:     "_hash_",
		Jobs:    []manifest.NormalizedJob{job},
	})
	const (
		offset64 = uint64(1469598103934665603)
		prime64  = uint64(1099511628211)
	)
	h := offset64
	for _, x := range b {
		h ^= uint64(x)
		h *= prime64
	}
	h ^= uint64(idx)
	h *= prime64
	return fmt.Sprintf("%016x", h)
}

var _ backends.Backend = (*Backend)(nil)
