package crontab

import (
	"fmt"

	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/policy"
)

// render produces the 2-line blocks for every schedule of `job`. Each
// block is:
//
//	<5-field cron> <triggerBin> trigger <app>.<job>
//	# cronix:owned app=<app> job=<job> hash=<hash> idx=<idx>
//
// The owner line is what List/Update/Delete match against to detect
// owned blocks; the schedule line preceding it is what cron actually
// fires.
func render(app, triggerBin string, job manifest.NormalizedJob) []string {
	out := make([]string, 0, 2*len(job.Schedules))
	for i, sched := range job.Schedules {
		cron, ok := translate(sched)
		if !ok {
			// Validate() would have caught this; defensive skip to avoid
			// emitting a malformed crontab line.
			continue
		}
		hash := policy.Hash(job, i)
		out = append(out,
			fmt.Sprintf("%s %s trigger %s.%s", cron, triggerBin, app, job.Name),
			fmt.Sprintf("%s app=%s job=%s hash=%s idx=%d", ownerMarker, app, job.Name, hash, i),
		)
	}
	return out
}
