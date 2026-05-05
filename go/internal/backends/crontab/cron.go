package crontab

import (
	"fmt"
	"strconv"
	"strings"
)

// translate maps a manifest schedule expression to the 5-field cron
// form. Returns ok=false for anything classic crontab cannot represent
// (sub-minute @every, non-divisible @every intervals, etc.).
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
		return translateEvery(strings.TrimSpace(rest))
	}
	if len(strings.Fields(t)) == 5 {
		return t, true
	}
	return "", false
}

// translateEvery handles `@every <N><unit>` expressions, supporting
// only intervals classic cron can represent exactly: minute counts that
// divide 60, hour counts that divide 24.
func translateEvery(rest string) (string, bool) {
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
