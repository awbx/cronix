// Package backends defines the Backend interface that every host scheduler
// adapter (crontab, systemd-timer, kubernetes) implements. Phase 4 ships
// the interface and its supporting types; Phase 5 ships the adapters.
package backends

import (
	"context"
	"time"

	"github.com/awbx/cronix/go/internal/manifest"
)

// Backend is the host-scheduler adapter contract.
//
// Implementations are responsible for translating the language-neutral
// `NormalizedJob` into backend-native artifacts (a crontab line, a
// systemd .timer/.service pair, a Kubernetes CronJob), tracking ownership
// (D-026) so cronix never modifies entries it did not create, and
// surfacing recent run history from backend-native sources (D-024).
//
// Backends MUST be safe to call concurrently for read methods (List,
// History, Validate); writers (Create, Update, Delete) MAY require
// process-wide serialization, which the reconciler provides via a
// global flock at /var/lock/cronix/apply.lock.
type Backend interface {
	// Name returns the stable backend identifier ("crontab",
	// "systemd-timer", "kubernetes"). Used in operator output and config.
	Name() string

	// List enumerates entries owned by cronix on this backend. Entries
	// not owned by cronix MUST NOT appear.
	List(ctx context.Context) ([]ManagedEntry, error)

	// Create installs a new entry for the given normalized job under the
	// named app. Multi-schedule jobs produce N entries with distinct
	// ManagedEntry.Index values.
	Create(ctx context.Context, app string, job manifest.NormalizedJob) error

	// Update replaces all owned entries for (app, job.Name) with the
	// freshly rendered form of `job`. It MUST be a no-op when the
	// rendered form already matches (D-027).
	Update(ctx context.Context, app string, job manifest.NormalizedJob) error

	// Delete removes all owned entries for (app, jobName). Implementations
	// MUST refuse to delete entries not owned by cronix.
	Delete(ctx context.Context, app, jobName string) error

	// Validate checks whether the backend can faithfully express the
	// job. Returns the set of issues, or no issues if the job is fully
	// supported.
	Validate(job manifest.NormalizedJob) ValidationResult

	// History reads recent run records from backend-native sources
	// (journald for systemd, K8s Events + Pod logs for kubernetes,
	// syslog or MAILTO output for crontab).
	History(ctx context.Context, opts HistoryOpts) ([]HistoryEntry, error)

	// Ensure verifies prerequisites: the backend's tooling is reachable,
	// required directories are writable, the API server responds, etc.
	// Returns an error describing what would prevent reconciliation.
	Ensure(ctx context.Context) error
}

// ManagedEntry identifies a single host-scheduler entry owned by cronix.
//
// For multi-schedule jobs there is one ManagedEntry per schedule, sharing
// App+Job but with distinct Index values.
type ManagedEntry struct {
	App   string
	Job   string
	Hash  string
	Index int
	// Raw is backend-specific: the parsed crontab line, the *batchv1.CronJob,
	// the systemd unit file pair. Reconciler code does not interpret it.
	Raw any
}

// ValidationResult reports whether a backend can express a job.
type ValidationResult struct {
	OK     bool
	Issues []string
}

// HistoryOpts narrows a History query.
type HistoryOpts struct {
	App    string
	Job    string
	Since  time.Time
	Until  time.Time
	Status string // "" all, "ok", "failed"
	Limit  int
}

// HistoryEntry is one observed run record.
type HistoryEntry struct {
	App        string
	Job        string
	RunID      string
	Attempt    int
	StartedAt  time.Time
	FinishedAt time.Time
	// Status is one of "ok", "failed", "lock-contended", "timeout", "unknown".
	Status string
	// Source identifies where this record came from ("journald",
	// "k8s-event", "k8s-pod-log", "syslog", "mailto").
	Source string
	Detail string
}

// Adopter is an optional Backend capability — adopting a pre-existing
// scheduler entry that already invokes cronix trigger (but lacks the
// D-026 ownership markers) into cronix's managed set, without
// disrupting the entry semantically.
//
// Backends that do not implement Adopter cannot be adopted into; the
// CLI surfaces a clear error in that case. Each v1 backend ships
// Adopter support in its own follow-up PR.
type Adopter interface {
	// Adopt searches the backend for an entry that matches the manifest
	// job and applies the cronix ownership markers without re-creating
	// it. Returns AdoptResult describing what was found and what action
	// was taken.
	//
	// When the candidate entry exists but diverges from the manifest
	// (different schedule, different command line, etc.), Adopt MUST
	// leave the entry untouched and return AdoptResult with
	// Diverged=true plus a human-readable description of every
	// divergence. The caller can then choose to Delete+Create instead.
	//
	// When opts.DryRun is true, Adopt MUST NOT modify the backend.
	Adopt(ctx context.Context, app string, job manifest.NormalizedJob, opts AdoptOpts) (AdoptResult, error)
}

// AdoptOpts narrows an Adopt call.
type AdoptOpts struct {
	// DryRun means "report what would happen without modifying the backend."
	DryRun bool
}

// AdoptResult is the outcome of an Adopt call.
type AdoptResult struct {
	// Found means the backend has at least one candidate entry that
	// looks like it could be adopted (e.g. a crontab line invoking
	// `cronix trigger <app>.<job>`).
	Found bool
	// Adopted means ownership markers were applied. False when DryRun
	// or when Diverged.
	Adopted bool
	// AlreadyManaged means the entry was already a cronix-owned entry —
	// adopt was a no-op. Not an error; common when re-running adopt.
	AlreadyManaged bool
	// Diverged means a candidate was found but it disagrees with the
	// manifest in a way that would change semantics if cronix took over
	// management. Divergences enumerates each difference.
	Diverged bool
	// Divergences is a human-readable list of differences (cron
	// expression, command-line tail, etc.). Empty when Diverged=false.
	Divergences []string
	// Entries enumerates the entries that were (or would be) adopted.
	// When DryRun=true these describe the candidate state; otherwise
	// they describe the post-adopt state.
	Entries []ManagedEntry
}
