package manifest

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/robfig/cron/v3"
)

var shortcuts = map[string]struct{}{
	"@hourly":   {},
	"@daily":    {},
	"@midnight": {},
	"@weekly":   {},
	"@monthly":  {},
	"@yearly":   {},
	"@annually": {},
}

var stdCron = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// validateSchedule returns an empty string if the schedule expression is
// well-formed, otherwise a human-readable error message. The accepted forms
// are 5-field cron, the documented shortcuts (D-004), and `@every <N>{s|m|h}`
// where N seconds resolve to ≥ 60.
func validateSchedule(expr string) string {
	if expr == "" {
		return "schedule must be a non-empty string"
	}
	t := strings.TrimSpace(expr)
	if _, ok := shortcuts[t]; ok {
		return ""
	}
	if rest, ok := strings.CutPrefix(t, "@every"); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" || len(rest) < 2 {
			return "@every duration must be a positive integer with unit s|m|h"
		}
		unit := rest[len(rest)-1]
		num, err := strconv.Atoi(rest[:len(rest)-1])
		if err != nil || num <= 0 {
			return "@every duration must be a positive integer"
		}
		var seconds int
		switch unit {
		case 's', 'S':
			seconds = num
		case 'm', 'M':
			seconds = num * 60
		case 'h', 'H':
			seconds = num * 3600
		default:
			return "@every duration unit must be s, m, or h"
		}
		if seconds < 60 {
			return "@every duration must be at least 60 seconds (v1 resolution floor)"
		}
		return ""
	}
	fields := strings.Fields(t)
	if len(fields) != 5 {
		return fmt.Sprintf("schedule must be 5 cron fields or a documented shortcut, got %d field(s)", len(fields))
	}
	if _, err := stdCron.Parse(t); err != nil {
		return fmt.Sprintf("invalid cron expression: %v", err)
	}
	return ""
}
