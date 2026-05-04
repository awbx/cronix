package reconcile

import (
	"path/filepath"
	"testing"

	"github.com/awbx/cronix/go/internal/backends/crontab"
	"github.com/awbx/cronix/go/internal/manifest"
)

func newCrontab(t *testing.T) *crontab.Backend {
	t.Helper()
	dir := t.TempDir()
	b, err := crontab.New(crontab.Options{
		Path:       filepath.Join(dir, "crontab"),
		TriggerBin: "/usr/local/bin/cronix",
	})
	if err != nil {
		t.Fatalf("crontab.New: %v", err)
	}
	return b
}

func sampleManifest(t *testing.T, jobs ...manifestJobSpec) *manifest.NormalizedManifest {
	t.Helper()
	m := &manifest.NormalizedManifest{Version: 1, App: "billing", Jobs: make([]manifest.NormalizedJob, 0, len(jobs))}
	for _, j := range jobs {
		m.Jobs = append(m.Jobs, manifest.NormalizedJob{
			Name:      j.name,
			Schedules: j.schedules,
			Timezone:  "UTC",
			Request: manifest.NormalizedRequest{
				Method: "POST", URL: "https://example.com/api/v1/scheduled/" + j.name,
				Headers: map[string]string{}, Body: "",
			},
			Policy: manifest.NormalizedPolicy{
				Concurrency: "Forbid", ConcurrencyScope: "host", TimeoutSeconds: 60,
				Retries: manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 1, MaxSeconds: 60},
			},
			Auth: manifest.NormalizedAuth{SecretRefs: []string{}},
		})
	}
	return m
}

type manifestJobSpec struct {
	name      string
	schedules []string
}

func TestComputeCreatesOnEmpty(t *testing.T) {
	b := newCrontab(t)
	m := sampleManifest(t, manifestJobSpec{"ping", []string{"@hourly"}})
	plan, err := Compute(t.Context(), m, b)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(plan.Ops) != 1 || plan.Ops[0].Action != ActionCreate {
		t.Fatalf("expected one Create op, got %+v", plan.Ops)
	}
}

func TestApplyCreatesAndIsThenIdempotent(t *testing.T) {
	b := newCrontab(t)
	m := sampleManifest(t, manifestJobSpec{"ping", []string{"@hourly"}})
	plan, err := Compute(t.Context(), m, b)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	res, err := Apply(t.Context(), plan, b)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("created = %d, want 1", res.Created)
	}
	// Re-compute → should be all-skip (idempotent).
	plan2, err := Compute(t.Context(), m, b)
	if err != nil {
		t.Fatalf("re-compute: %v", err)
	}
	if !plan2.IsNoop() {
		t.Fatalf("expected noop, got %+v", plan2.Ops)
	}
	res2, err := Apply(t.Context(), plan2, b)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if res2.Created != 0 || res2.Updated != 0 || res2.Deleted != 0 {
		t.Fatalf("expected no changes, got %+v", res2)
	}
	if res2.Skipped != 1 {
		t.Fatalf("expected 1 skip, got %d", res2.Skipped)
	}
}

func TestComputeUpdateOnHashChange(t *testing.T) {
	b := newCrontab(t)
	m1 := sampleManifest(t, manifestJobSpec{"ping", []string{"@hourly"}})
	plan, _ := Compute(t.Context(), m1, b)
	if _, err := Apply(t.Context(), plan, b); err != nil {
		t.Fatalf("apply m1: %v", err)
	}
	// Same job, different schedule.
	m2 := sampleManifest(t, manifestJobSpec{"ping", []string{"@daily"}})
	plan2, err := Compute(t.Context(), m2, b)
	if err != nil {
		t.Fatalf("compute m2: %v", err)
	}
	if len(plan2.Ops) != 1 || plan2.Ops[0].Action != ActionUpdate {
		t.Fatalf("expected one Update op, got %+v", plan2.Ops)
	}
	res, err := Apply(t.Context(), plan2, b)
	if err != nil {
		t.Fatalf("apply m2: %v", err)
	}
	if res.Updated != 1 {
		t.Fatalf("updated = %d, want 1", res.Updated)
	}
}

func TestComputeDeleteOnRemoval(t *testing.T) {
	b := newCrontab(t)
	m1 := sampleManifest(t,
		manifestJobSpec{"ping", []string{"@hourly"}},
		manifestJobSpec{"watchdog", []string{"*/5 * * * *"}},
	)
	plan, _ := Compute(t.Context(), m1, b)
	if _, err := Apply(t.Context(), plan, b); err != nil {
		t.Fatalf("apply m1: %v", err)
	}
	// Drop watchdog from desired.
	m2 := sampleManifest(t, manifestJobSpec{"ping", []string{"@hourly"}})
	plan2, err := Compute(t.Context(), m2, b)
	if err != nil {
		t.Fatalf("compute m2: %v", err)
	}
	deletes := 0
	for _, op := range plan2.Ops {
		if op.Action == ActionDelete {
			deletes++
			if op.JobName != "watchdog" {
				t.Errorf("expected delete on watchdog, got %s", op.JobName)
			}
		}
	}
	if deletes != 1 {
		t.Fatalf("expected 1 delete, got %d (ops=%+v)", deletes, plan2.Ops)
	}
	res, err := Apply(t.Context(), plan2, b)
	if err != nil {
		t.Fatalf("apply m2: %v", err)
	}
	if res.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", res.Deleted)
	}
}

func TestApplyOrderDeletesUpdatesCreates(t *testing.T) {
	// Set up: install A, B; new manifest has B (changed), C (new), no A.
	b := newCrontab(t)
	m1 := sampleManifest(t,
		manifestJobSpec{"a", []string{"@hourly"}},
		manifestJobSpec{"b", []string{"@hourly"}},
	)
	plan1, _ := Compute(t.Context(), m1, b)
	if _, err := Apply(t.Context(), plan1, b); err != nil {
		t.Fatalf("apply m1: %v", err)
	}
	m2 := sampleManifest(t,
		manifestJobSpec{"b", []string{"@daily"}},
		manifestJobSpec{"c", []string{"@hourly"}},
	)
	plan2, err := Compute(t.Context(), m2, b)
	if err != nil {
		t.Fatalf("compute m2: %v", err)
	}
	res, err := Apply(t.Context(), plan2, b)
	if err != nil {
		t.Fatalf("apply m2: %v", err)
	}
	if res.Deleted != 1 || res.Updated != 1 || res.Created != 1 {
		t.Fatalf("res = %+v, want 1/1/1", res)
	}

	// Re-compute should be noop now.
	plan3, _ := Compute(t.Context(), m2, b)
	if !plan3.IsNoop() {
		t.Fatalf("expected noop after convergence, got %+v", plan3.Ops)
	}
}

func TestDriftIdenticalToCompute(t *testing.T) {
	b := newCrontab(t)
	m := sampleManifest(t, manifestJobSpec{"ping", []string{"@hourly"}})
	plan, err := Compute(t.Context(), m, b)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	drift, err := Drift(t.Context(), m, b)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if len(plan.Ops) != len(drift.Ops) {
		t.Fatalf("plan and drift differ in length")
	}
}
