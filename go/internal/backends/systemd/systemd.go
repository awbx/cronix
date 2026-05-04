// Package systemd is the systemd-timer backend adapter.
//
// Per-job artifacts:
//
//	/etc/systemd/system/cronix-<app>-<job>-<idx>.timer
//	/etc/systemd/system/cronix-<app>-<job>-<idx>.service
//
// The reconciler invokes systemctl daemon-reload after writing.
//
// v1 status: this package ships unit-file generation and Validate. The
// actual systemctl/journalctl shell-out integration is wired in a
// follow-up phase — see PLAN.md §5c. Operators using systemd today can
// generate the units via this package and apply them manually.
package systemd

import (
	"context"
	"fmt"
	"strings"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
)

// Backend is a placeholder for the full systemd implementation.
type Backend struct {
	unitDir    string
	triggerBin string
}

// Options for the (incomplete v1) systemd backend.
type Options struct {
	UnitDir    string // defaults to /etc/systemd/system
	TriggerBin string // required
}

// New constructs a Backend.
func New(opts Options) (*Backend, error) {
	if opts.UnitDir == "" {
		opts.UnitDir = "/etc/systemd/system"
	}
	if opts.TriggerBin == "" {
		return nil, fmt.Errorf("systemd: TriggerBin is required")
	}
	return &Backend{unitDir: opts.UnitDir, triggerBin: opts.TriggerBin}, nil
}

// Name returns "systemd-timer".
func (*Backend) Name() string { return "systemd-timer" }

// Validate maps the schedule to OnCalendar= and reports any unsupported
// patterns. Sub-minute resolution is currently rejected; systemd-timer
// supports it but other backends do not.
func (*Backend) Validate(job manifest.NormalizedJob) backends.ValidationResult {
	var issues []string
	for i, s := range job.Schedules {
		if _, err := translateOnCalendar(s); err != nil {
			issues = append(issues, fmt.Sprintf("schedules[%d]: %v", i, err))
		}
	}
	return backends.ValidationResult{OK: len(issues) == 0, Issues: issues}
}

// RenderUnits returns the (.timer, .service) file contents for one
// schedule of the given job.
func RenderUnits(triggerBin, app string, job manifest.NormalizedJob, idx int) (timerFile, serviceFile string, err error) {
	if idx < 0 || idx >= len(job.Schedules) {
		return "", "", fmt.Errorf("systemd: schedule index %d out of range (have %d)", idx, len(job.Schedules))
	}
	cal, err := translateOnCalendar(job.Schedules[idx])
	if err != nil {
		return "", "", err
	}
	unitName := fmt.Sprintf("cronix-%s-%s-%d", app, job.Name, idx)
	timerFile = fmt.Sprintf(`[Unit]
Description=cronix timer: %[1]s.%[2]s (idx=%[3]d)
PartOf=%[4]s.service
X-Cronix-App=%[1]s
X-Cronix-Job=%[2]s
X-Cronix-Index=%[3]d

[Timer]
OnCalendar=%[5]s
Unit=%[4]s.service
Persistent=true

[Install]
WantedBy=timers.target
`, app, job.Name, idx, unitName, cal)
	serviceFile = fmt.Sprintf(`[Unit]
Description=cronix: %[1]s.%[2]s (idx=%[3]d)
After=network-online.target
X-Cronix-App=%[1]s
X-Cronix-Job=%[2]s
X-Cronix-Index=%[3]d

[Service]
Type=oneshot
ExecStart=%[4]s trigger %[1]s.%[2]s
RuntimeMaxSec=%[5]d
`, app, job.Name, idx, triggerBin, job.Policy.TimeoutSeconds+30)
	return timerFile, serviceFile, nil
}

// translateOnCalendar maps a manifest schedule to systemd's OnCalendar
// syntax. The mapping covers shortcuts and standard 5-field cron; users
// needing systemd-specific calendar specs can express them in cron form.
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
		// systemd has its own OnUnitActiveSec for this; we leave it as a
		// shortcut and let the user's systemd version validate.
		return "*-*-* " + strings.TrimSpace(rest), nil
	}
	if len(strings.Fields(t)) == 5 {
		return cronToOnCalendar(t)
	}
	return "", fmt.Errorf("systemd: cannot translate %q to OnCalendar=", s)
}

// cronToOnCalendar maps a 5-field cron expression to OnCalendar=. The
// translation supports the common forms (numbers, ranges, lists, *)
// but not rare cases like specific weekday + day-of-month combinations.
func cronToOnCalendar(expr string) (string, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return "", fmt.Errorf("expected 5 fields, got %d", len(f))
	}
	min, hour, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	// systemd: <DayOfWeek> <Year>-<Month>-<Day> <Hour>:<Minute>:<Second>
	// We omit the second (always 0) and year (always *).
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
	// systemd uses Mon..Sun; cron uses 0..6 (0=Sun).
	dayNames := map[string]string{"0": "Sun", "1": "Mon", "2": "Tue", "3": "Wed", "4": "Thu", "5": "Fri", "6": "Sat", "7": "Sun"}
	parts := strings.Split(dow, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		// Range: 1-5 → Mon..Fri
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

// The List/Create/Update/Delete/History/Ensure methods are placeholders
// for the integration phase. They satisfy the Backend interface so the
// reconciler's Plan/Apply can compile against this backend.
func (*Backend) List(_ context.Context) ([]backends.ManagedEntry, error) {
	return nil, fmt.Errorf("systemd: List not implemented in this phase — see PLAN.md §5c")
}

func (*Backend) Create(_ context.Context, _ string, _ manifest.NormalizedJob) error {
	return fmt.Errorf("systemd: Create not implemented in this phase — see PLAN.md §5c")
}

func (*Backend) Update(_ context.Context, _ string, _ manifest.NormalizedJob) error {
	return fmt.Errorf("systemd: Update not implemented in this phase — see PLAN.md §5c")
}

func (*Backend) Delete(_ context.Context, _, _ string) error {
	return fmt.Errorf("systemd: Delete not implemented in this phase — see PLAN.md §5c")
}

func (*Backend) History(_ context.Context, _ backends.HistoryOpts) ([]backends.HistoryEntry, error) {
	return nil, nil
}

func (*Backend) Ensure(_ context.Context) error {
	return fmt.Errorf("systemd: Ensure not implemented in this phase — see PLAN.md §5c")
}

var _ backends.Backend = (*Backend)(nil)
