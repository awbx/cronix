// Package reconcile implements Plan/Apply/Diff/Drift orchestration over
// the Backend interface. The reconciler is stateless — it reads the
// desired state from the manifest and the actual state from the backend,
// computes a diff, and (for Apply) executes it.
package reconcile

import (
	"context"
	"fmt"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/policy"
)

// Action is one operation in a Plan.
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
	ActionSkip   Action = "skip"
)

// Op is a single planned operation against a backend.
type Op struct {
	Action  Action
	App     string
	JobName string
	// Job is the desired NormalizedJob for Create/Update; empty Name for Delete/Skip.
	Job manifest.NormalizedJob
	// Hashes record what we observed and what we want. For Skip, both
	// match. For Update, they differ. For Create, OldHash is empty. For
	// Delete, NewHash is empty.
	OldHash string
	NewHash string
	// Indices is the set of schedule indices this op affects. For
	// crontab/systemd, this is just informational (Update replaces all
	// owned blocks for the job at once); for k8s, each index is a
	// separate CronJob resource.
	Indices []int
}

// Plan groups all ops that Apply will execute in order.
type Plan struct {
	Backend string
	Ops     []Op
}

// Compute builds a Plan by diffing the desired manifest against the
// backend's current state.
//
// `desired` is the canonical normalized manifest. The backend is queried
// for its current ManagedEntries; the result is grouped by (app, job) so
// multi-schedule jobs are diffed as a single unit (Backend semantics
// match: Update/Delete operate per (app, job) — see backends.Backend).
func Compute(ctx context.Context, desired *manifest.NormalizedManifest, backend backends.Backend) (*Plan, error) {
	current, err := backend.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconcile: list: %w", err)
	}

	// Group current entries by (app, job).
	type currentJob struct {
		hashes  []string // by index
		indices []int
	}
	currentBy := make(map[appJob]*currentJob)
	for _, e := range current {
		key := appJob{App: e.App, Job: e.Job}
		cj, ok := currentBy[key]
		if !ok {
			cj = &currentJob{}
			currentBy[key] = cj
		}
		// Index hashes by their idx so multi-schedule jobs compare
		// schedule-by-schedule.
		for len(cj.hashes) <= e.Index {
			cj.hashes = append(cj.hashes, "")
		}
		cj.hashes[e.Index] = e.Hash
		cj.indices = append(cj.indices, e.Index)
	}

	plan := &Plan{Backend: backend.Name()}
	desiredKeys := map[appJob]struct{}{}

	// Walk desired jobs.
	for _, job := range desired.Jobs {
		key := appJob{App: desired.App, Job: job.Name}
		desiredKeys[key] = struct{}{}

		desiredHashes := computeDesiredHashes(job)

		cj, exists := currentBy[key]
		if !exists {
			plan.Ops = append(plan.Ops, Op{
				Action:  ActionCreate,
				App:     desired.App,
				JobName: job.Name,
				Job:     job,
				NewHash: joinHashes(desiredHashes),
				Indices: indices(len(desiredHashes)),
			})
			continue
		}

		// Compare hash sets. If number of schedules or any hash differs,
		// it's an Update; otherwise Skip.
		if hashesEqual(cj.hashes, desiredHashes) {
			plan.Ops = append(plan.Ops, Op{
				Action:  ActionSkip,
				App:     desired.App,
				JobName: job.Name,
				Job:     job,
				OldHash: joinHashes(cj.hashes),
				NewHash: joinHashes(desiredHashes),
				Indices: indices(len(desiredHashes)),
			})
		} else {
			plan.Ops = append(plan.Ops, Op{
				Action:  ActionUpdate,
				App:     desired.App,
				JobName: job.Name,
				Job:     job,
				OldHash: joinHashes(cj.hashes),
				NewHash: joinHashes(desiredHashes),
				Indices: indices(len(desiredHashes)),
			})
		}
	}

	// Anything currently installed that's no longer desired → Delete.
	// We delete only entries belonging to the same app as the desired
	// manifest. Other apps' owned entries are not our concern in this
	// Compute call (the reconciler invokes Compute once per app).
	for key, cj := range currentBy {
		if _, ok := desiredKeys[key]; ok {
			continue
		}
		if key.App != desired.App {
			continue
		}
		plan.Ops = append(plan.Ops, Op{
			Action:  ActionDelete,
			App:     key.App,
			JobName: key.Job,
			OldHash: joinHashes(cj.hashes),
			Indices: cj.indices,
		})
	}

	return plan, nil
}

// Apply executes the Plan in the order deletes → updates → creates,
// skipping ActionSkip entries entirely.
//
// On the first error it stops and returns that error along with a
// Result describing which ops actually ran. Callers MAY retry by calling
// Compute again; the reconciler is idempotent.
type Result struct {
	Created int
	Updated int
	Deleted int
	Skipped int
}

// Apply executes the plan against the backend.
func Apply(ctx context.Context, plan *Plan, backend backends.Backend) (Result, error) {
	var res Result
	// Pass 1: deletes.
	for _, op := range plan.Ops {
		if op.Action != ActionDelete {
			continue
		}
		if err := backend.Delete(ctx, op.App, op.JobName); err != nil {
			return res, fmt.Errorf("reconcile: delete %s.%s: %w", op.App, op.JobName, err)
		}
		res.Deleted++
	}
	// Pass 2: updates.
	for _, op := range plan.Ops {
		if op.Action != ActionUpdate {
			continue
		}
		if err := backend.Update(ctx, op.App, op.Job); err != nil {
			return res, fmt.Errorf("reconcile: update %s.%s: %w", op.App, op.JobName, err)
		}
		res.Updated++
	}
	// Pass 3: creates.
	for _, op := range plan.Ops {
		if op.Action != ActionCreate {
			continue
		}
		if err := backend.Create(ctx, op.App, op.Job); err != nil {
			return res, fmt.Errorf("reconcile: create %s.%s: %w", op.App, op.JobName, err)
		}
		res.Created++
	}
	for _, op := range plan.Ops {
		if op.Action == ActionSkip {
			res.Skipped++
		}
	}
	return res, nil
}

// Drift reports the same diff as Compute but expressed as drift (the
// `cronix drift` CLI surface). Internally identical to Compute; named
// separately for readability of caller code.
func Drift(ctx context.Context, desired *manifest.NormalizedManifest, backend backends.Backend) (*Plan, error) {
	return Compute(ctx, desired, backend)
}

// IsNoop reports whether a Plan would not change anything. Operators
// running `cronix apply` from CI rely on this for log silence (D-027).
func (p *Plan) IsNoop() bool {
	for _, op := range p.Ops {
		if op.Action != ActionSkip {
			return false
		}
	}
	return true
}

// appJob is the (app, job) compound key used for plan grouping.
type appJob struct{ App, Job string }

// computeDesiredHashes returns one policy.Hash per schedule index for
// the given job. The reconciler compares these against the hashes
// backends report through List() to decide create/update/no-op.
//
// The hash algorithm itself lives in internal/policy so backends and
// the reconciler stay byte-identical when computing it.
func computeDesiredHashes(job manifest.NormalizedJob) []string {
	out := make([]string, len(job.Schedules))
	for i := range job.Schedules {
		out[i] = policy.Hash(job, i)
	}
	return out
}

func hashesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func joinHashes(h []string) string {
	out := ""
	for i, s := range h {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

func indices(n int) []int {
	out := make([]int, n)
	for i := range n {
		out[i] = i
	}
	return out
}
