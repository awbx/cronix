package policy

import (
	"strings"
	"testing"

	"github.com/awbx/cronix/go/internal/manifest"
)

func TestScheduleName(t *testing.T) {
	got := ScheduleName("billing", "reconcile", 0)
	want := "cronix-billing-reconcile-0"
	if got != want {
		t.Errorf("ScheduleName = %q, want %q", got, want)
	}
}

func TestScheduleNameUsesPrefix(t *testing.T) {
	got := ScheduleName("a", "b", 7)
	if !strings.HasPrefix(got, SchedulePrefix) {
		t.Errorf("expected %q to start with %q", got, SchedulePrefix)
	}
}

func sampleJob(schedules ...string) manifest.NormalizedJob {
	return manifest.NormalizedJob{
		Name:      "reconcile",
		Schedules: schedules,
		Timezone:  "UTC",
		Request: manifest.NormalizedRequest{
			Method: "POST", URL: "https://example.com/reconcile",
			Headers: map[string]string{}, Body: "",
		},
		Policy: manifest.NormalizedPolicy{
			Concurrency: "Forbid", ConcurrencyScope: "global", TimeoutSeconds: 60,
			Retries: manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 1, MaxSeconds: 60},
		},
	}
}

func TestHashStableAcrossInvocations(t *testing.T) {
	job := sampleJob("@hourly")
	a := Hash(job, 0)
	b := Hash(job, 0)
	if a != b {
		t.Errorf("hash not stable: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("expected 16 hex chars, got %d (%q)", len(a), a)
	}
}

func TestHashIndexSaltsResult(t *testing.T) {
	job := sampleJob("@hourly", "@daily")
	if Hash(job, 0) == Hash(job, 1) {
		t.Errorf("expected per-index hash to differ, both %q", Hash(job, 0))
	}
}

func TestHashChangesWhenScheduleChanges(t *testing.T) {
	a := Hash(sampleJob("@hourly"), 0)
	b := Hash(sampleJob("@daily"), 0)
	if a == b {
		t.Errorf("expected hash to change with schedule, both %q", a)
	}
}
