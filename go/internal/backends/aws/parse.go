package aws

import (
	"strconv"
	"strings"

	"github.com/awbx/cronix/go/internal/backends"
)

// parseDescription reads ownership fields out of the schedule
// description. Format:
//
//	cronix-managed app=<app> job=<job> idx=<n> hash=<hex>
//
// Returns ok=false for descriptions that do not start with descriptionPrefix
// (foreign schedules, hand-rolled by operators) or that are missing
// the required app/job fields.
func parseDescription(s string) (backends.ManagedEntry, bool) {
	if !strings.HasPrefix(s, descriptionPrefix) {
		return backends.ManagedEntry{}, false
	}
	rest := strings.TrimPrefix(s, descriptionPrefix)
	out := backends.ManagedEntry{}
	for _, f := range strings.Fields(rest) {
		eq := strings.IndexByte(f, '=')
		if eq < 0 {
			continue
		}
		k, v := f[:eq], f[eq+1:]
		switch k {
		case "app":
			out.App = v
		case "job":
			out.Job = v
		case "hash":
			out.Hash = v
		case "idx":
			n, _ := strconv.Atoi(v)
			out.Index = n
		}
	}
	if out.App == "" || out.Job == "" {
		return backends.ManagedEntry{}, false
	}
	return out, true
}
