// Package systemd is the systemd-timer backend adapter.
//
// Per-job artifacts:
//
//	<unitDir>/cronix-<app>-<job>-<idx>.timer
//	<unitDir>/cronix-<app>-<job>-<idx>.service
//
// Ownership lives in `X-Cronix-*` annotations inside the unit files —
// systemd ignores `X-` prefixed fields, so we use them as our marker:
//
//	X-Cronix-App=<app>
//	X-Cronix-Job=<job>
//	X-Cronix-Index=<idx>
//	X-Cronix-Hash=<hash>
//
// The reconciler invokes `systemctl daemon-reload`, `enable --now`, and
// `disable --now` via a configurable executor so tests can drive the
// backend without an actual init system.
package systemd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
)

// SystemctlExecutor runs `systemctl` invocations. Production uses
// `os/exec`; tests pass a recorder.
type SystemctlExecutor interface {
	Run(ctx context.Context, args ...string) error
}

// Backend writes systemd timer + service unit pairs and drives systemctl.
type Backend struct {
	unitDir    string
	triggerBin string
	systemctl  SystemctlExecutor
}

// Options for constructing a Backend.
type Options struct {
	// UnitDir defaults to /etc/systemd/system.
	UnitDir string
	// TriggerBin is the absolute path to the cronix binary on the host. Required.
	TriggerBin string
	// Systemctl runs `systemctl ...` invocations. Defaults to os/exec
	// against the system PATH.
	Systemctl SystemctlExecutor
}

// New constructs a Backend.
func New(opts Options) (*Backend, error) {
	if opts.UnitDir == "" {
		opts.UnitDir = "/etc/systemd/system"
	}
	if opts.TriggerBin == "" {
		return nil, fmt.Errorf("systemd: TriggerBin is required")
	}
	exec := opts.Systemctl
	if exec == nil {
		exec = defaultSystemctl{}
	}
	return &Backend{unitDir: opts.UnitDir, triggerBin: opts.TriggerBin, systemctl: exec}, nil
}

// Name returns "systemd-timer".
func (*Backend) Name() string { return "systemd-timer" }

// List enumerates owned timer units. ConfigMap-style ownership is
// tracked via X-Cronix-* annotations inside each unit file.
func (b *Backend) List(_ context.Context) ([]backends.ManagedEntry, error) {
	matches, err := filepath.Glob(filepath.Join(b.unitDir, "cronix-*.timer"))
	if err != nil {
		return nil, fmt.Errorf("systemd: glob %s: %w", b.unitDir, err)
	}
	out := make([]backends.ManagedEntry, 0, len(matches))
	for _, p := range matches {
		raw, err := os.ReadFile(p) //#nosec G304 — operator-managed dir
		if err != nil {
			return nil, fmt.Errorf("systemd: read %s: %w", p, err)
		}
		entry, ok := parseUnit(string(raw))
		if !ok {
			continue
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
		hash := hashJobSchedule(job, i)
		timerFile, serviceFile, err := renderUnitsWithHash(b.triggerBin, app, job, i, hash)
		if err != nil {
			return err
		}
		unit := unitName(app, job.Name, i)
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
		unit := unitName(app, job.Name, i) + ".timer"
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
	prefix := fmt.Sprintf("cronix-%s-%s-", app, jobName)
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

// History reads journalctl. Phase 6 wires this; returns nil for now.
func (*Backend) History(_ context.Context, _ backends.HistoryOpts) ([]backends.HistoryEntry, error) {
	return nil, nil
}

// Ensure verifies systemd is the init system and the unit dir exists.
func (b *Backend) Ensure(_ context.Context) error {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return fmt.Errorf("systemd: not running under systemd (/run/systemd/system: %w)", err)
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

func unitName(app, job string, idx int) string {
	return fmt.Sprintf("cronix-%s-%s-%d", app, job, idx)
}

// RenderUnits returns the (.timer, .service) file contents for one
// schedule of the given job. Kept as a public helper; equivalent to
// renderUnitsWithHash with an empty hash.
func RenderUnits(triggerBin, app string, job manifest.NormalizedJob, idx int) (timerFile, serviceFile string, err error) {
	return renderUnitsWithHash(triggerBin, app, job, idx, "")
}

func renderUnitsWithHash(triggerBin, app string, job manifest.NormalizedJob, idx int, hash string) (timerFile, serviceFile string, err error) {
	if idx < 0 || idx >= len(job.Schedules) {
		return "", "", fmt.Errorf("systemd: schedule index %d out of range (have %d)", idx, len(job.Schedules))
	}
	cal, err := translateOnCalendar(job.Schedules[idx])
	if err != nil {
		return "", "", err
	}
	unit := unitName(app, job.Name, idx)
	hashLine := ""
	if hash != "" {
		hashLine = fmt.Sprintf("X-Cronix-Hash=%s\n", hash)
	}
	timerFile = fmt.Sprintf(`[Unit]
Description=cronix timer: %[1]s.%[2]s (idx=%[3]d)
PartOf=%[4]s.service
X-Cronix-App=%[1]s
X-Cronix-Job=%[2]s
X-Cronix-Index=%[3]d
%[6]s
[Timer]
OnCalendar=%[5]s
Unit=%[4]s.service
Persistent=true

[Install]
WantedBy=timers.target
`, app, job.Name, idx, unit, cal, hashLine)
	serviceFile = fmt.Sprintf(`[Unit]
Description=cronix: %[1]s.%[2]s (idx=%[3]d)
After=network-online.target
X-Cronix-App=%[1]s
X-Cronix-Job=%[2]s
X-Cronix-Index=%[3]d
%[5]s
[Service]
Type=oneshot
ExecStart=%[4]s trigger %[1]s.%[2]s
RuntimeMaxSec=%[6]d
`, app, job.Name, idx, triggerBin, hashLine, job.Policy.TimeoutSeconds+30)
	return timerFile, serviceFile, nil
}

var (
	appLineRe   = regexp.MustCompile(`(?m)^X-Cronix-App=(.+)$`)
	jobLineRe   = regexp.MustCompile(`(?m)^X-Cronix-Job=(.+)$`)
	hashLineRe  = regexp.MustCompile(`(?m)^X-Cronix-Hash=(.+)$`)
	indexLineRe = regexp.MustCompile(`(?m)^X-Cronix-Index=(\d+)$`)
)

// parseUnit extracts ManagedEntry from a unit file's X-Cronix-* annotations.
func parseUnit(contents string) (backends.ManagedEntry, bool) {
	app := firstMatch(appLineRe, contents)
	job := firstMatch(jobLineRe, contents)
	idxStr := firstMatch(indexLineRe, contents)
	if app == "" || job == "" || idxStr == "" {
		return backends.ManagedEntry{}, false
	}
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		return backends.ManagedEntry{}, false
	}
	return backends.ManagedEntry{
		App:   app,
		Job:   job,
		Hash:  firstMatch(hashLineRe, contents),
		Index: idx,
	}, true
}

func firstMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// translateOnCalendar maps a manifest schedule to systemd's OnCalendar
// syntax. Covers shortcuts and standard 5-field cron.
func translateOnCalendar(s string) (string, error) {
	t := strings.TrimSpace(s)
	switch t {
	case "@hourly":
		return "hourly", nil
	case "@daily", "@midnight":
		return "daily", nil
	case "@weekly":
		return "weekly", nil
	case "@monthly":
		return "monthly", nil
	case "@yearly", "@annually":
		return "yearly", nil
	}
	if rest, ok := strings.CutPrefix(t, "@every"); ok {
		return "*-*-* " + strings.TrimSpace(rest), nil
	}
	if len(strings.Fields(t)) == 5 {
		return cronToOnCalendar(t)
	}
	return "", fmt.Errorf("systemd: cannot translate %q to OnCalendar=", s)
}

func cronToOnCalendar(expr string) (string, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return "", fmt.Errorf("expected 5 fields, got %d", len(f))
	}
	min, hour, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	dowStr := translateDOW(dow)
	timeStr := fmt.Sprintf("%s:%s:00", hour, min)
	if isStar(hour) {
		timeStr = fmt.Sprintf("*:%s:00", min)
	}
	dateStr := fmt.Sprintf("*-%s-%s", mon, dom)
	if dowStr != "" {
		return fmt.Sprintf("%s %s %s", dowStr, dateStr, timeStr), nil
	}
	return fmt.Sprintf("%s %s", dateStr, timeStr), nil
}

func translateDOW(dow string) string {
	if isStar(dow) {
		return ""
	}
	dayNames := map[string]string{"0": "Sun", "1": "Mon", "2": "Tue", "3": "Wed", "4": "Thu", "5": "Fri", "6": "Sat", "7": "Sun"}
	parts := strings.Split(dow, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.Contains(p, "-") {
			ends := strings.Split(p, "-")
			if len(ends) == 2 {
				a, b := dayNames[ends[0]], dayNames[ends[1]]
				if a != "" && b != "" {
					out = append(out, a+".."+b)
					continue
				}
			}
		}
		if name, ok := dayNames[p]; ok {
			out = append(out, name)
		} else {
			return ""
		}
	}
	return strings.Join(out, ",")
}

func isStar(field string) bool { return field == "*" }

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

// hashJobSchedule produces the same 16-char change-detection hash the
// reconciler uses (FNV-1a over the canonicalized manifest, salted by
// schedule index). Matches reconcile.computeDesiredHashes.
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

// defaultSystemctl shells out to the system `systemctl` binary.
type defaultSystemctl struct{}

func (defaultSystemctl) Run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...) //#nosec G204 — args are constructed internally
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

var _ backends.Backend = (*Backend)(nil)
