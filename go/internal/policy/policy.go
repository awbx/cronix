// Package policy holds the cross-backend conventions that every backend
// (and the reconciler) must agree on.
//
// Backends differ in how they store ownership metadata (k8s labels vs
// systemd unit annotations vs AWS schedule descriptions vs crontab
// comments), but two things are universal across the project and must
// produce byte-identical output everywhere they are computed:
//
//   - The schedule name template — `cronix-<app>-<job>-<idx>` — is the
//     identity backends use to enumerate owned resources. Changing it
//     would orphan every existing deployment, so it lives in one place.
//   - The change-detection hash — FNV-1a 64-bit over the canonicalized
//     manifest, salted by schedule index — is what `cronix drift` and
//     `cronix plan` compare across backends. The reconciler computes
//     the desired hash; backends report the observed hash from
//     ownership metadata; the two must use the exact same algorithm or
//     every plan would surface a phantom diff.
//
// Anything genuinely backend-specific (label keys, annotation prefixes,
// description formats) belongs in the backend's own package, not here.
package policy

import (
	"fmt"

	"github.com/awbx/cronix/go/internal/manifest"
)

// SchedulePrefix is the literal prefix every owned-resource name starts
// with. Backends use it both to construct names and to filter remote
// listings (e.g. AWS `ListSchedules` `NamePrefix`).
const SchedulePrefix = "cronix-"

// ScheduleName returns the canonical resource name for one (app, job,
// schedule-index) tuple: `cronix-<app>-<job>-<idx>`. Every backend uses
// this exact form for its primary owned resource (timer unit name,
// CronJob name, EventBridge schedule name, etc.).
func ScheduleName(app, job string, idx int) string {
	return fmt.Sprintf("%s%s-%s-%d", SchedulePrefix, app, job, idx)
}

// Hash returns the change-detection hash for (job, schedule-index).
// FNV-1a 64-bit over the canonicalized manifest, salted by schedule
// index, hex-encoded as 16 lowercase chars.
//
// The salt makes multi-schedule jobs distinguishable: two schedules of
// the same job differ only in their index field, so without the salt
// they would collide.
//
// This must stay byte-identical across the reconciler and every
// backend; see the package doc for why.
func Hash(job manifest.NormalizedJob, idx int) string {
	b, _ := manifest.Canonicalize(&manifest.NormalizedManifest{
		Version: 1,
		App:     "_hash_",
		Jobs:    []manifest.NormalizedJob{job},
	})
	const (
		fnvOffset64 = uint64(1469598103934665603)
		fnvPrime64  = uint64(1099511628211)
	)
	h := fnvOffset64
	for _, x := range b {
		h ^= uint64(x)
		h *= fnvPrime64
	}
	h ^= uint64(idx)
	h *= fnvPrime64
	return fmt.Sprintf("%016x", h)
}

// DriftSpecEdited is the sentinel hash backends emit when they detect
// that the actual deployed spec (e.g. a CronJob's `schedule:` field, a
// systemd unit's `OnCalendar=` line) no longer matches what cronix
// rendered, even when the ownership-recorded hash was left untouched.
//
// The reconciler treats it as "always different from desired", so
// `cronix drift` surfaces manual edits operators made out-of-band.
const DriftSpecEdited = "drift-spec-edited"
