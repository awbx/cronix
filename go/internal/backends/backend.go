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
