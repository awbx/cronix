package aws

import (
	"fmt"
	"strings"
)

// translateAWSCron maps a manifest schedule to EventBridge's
// `cron(min hr day month day-of-week year)` form. Differences vs
// classic 5-field cron:
//
//   - 6 fields total (the trailing year, usually `*`).
//   - day-of-month and day-of-week are mutually exclusive: exactly one
//     must be `?`. AWS rejects `*` on both at the same time.
//
// Standard 5-field cron with `*` on day-of-month becomes `?` here, and
// a non-`*` day-of-week likewise forces day-of-month to `?`.
func translateAWSCron(s string) (string, error) {
	t := strings.TrimSpace(s)
	switch t {
	case "@hourly":
		return "cron(0 * * * ? *)", nil
	case "@daily", "@midnight":
		return "cron(0 0 * * ? *)", nil
	case "@weekly":
		return "cron(0 0 ? * SUN *)", nil
	case "@monthly":
		return "cron(0 0 1 * ? *)", nil
	case "@yearly", "@annually":
		return "cron(0 0 1 1 ? *)", nil
	}
	if strings.HasPrefix(t, "@every") {
		return "", fmt.Errorf("aws-scheduler: @every shortcuts not supported; use rate(...) form which cronix doesn't model yet")
	}
	f := strings.Fields(t)
	if len(f) != 5 {
		return "", fmt.Errorf("aws-scheduler: expected 5-field cron, got %q", s)
	}
	min, hr, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	// AWS requires exactly one of dom/dow to be `?`. Convert standard
	// cron's `*` semantics: prefer `?` on whichever the manifest left
	// unconstrained.
	switch {
	case dow == "*" && dom != "*":
		dow = "?"
	case dom == "*" && dow != "*":
		dom = "?"
	case dom == "*" && dow == "*":
		dow = "?"
	default:
		// Both constrained — AWS will reject. Prefer dom, blank dow.
		dow = "?"
	}
	return fmt.Sprintf("cron(%s %s %s %s %s *)", min, hr, dom, mon, dow), nil
}
