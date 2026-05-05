package systemd

import (
	"fmt"
	"strings"
)

// translateOnCalendar maps a manifest schedule expression to systemd's
// OnCalendar= syntax. Covers the @-shortcuts and standard 5-field cron;
// returns an error for anything systemd cannot represent.
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
