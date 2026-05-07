package vercel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
)

func sampleJob(name string, schedules ...string) manifest.NormalizedJob {
	return manifest.NormalizedJob{
		Name:      name,
		Schedules: schedules,
		Request: manifest.NormalizedRequest{
			Method: "POST", URL: "https://example.com/api/v1/scheduled/" + name,
			Headers: map[string]string{}, Body: "",
		},
		Policy: manifest.NormalizedPolicy{
			Concurrency: "Forbid", ConcurrencyScope: "host", TimeoutSeconds: 60,
			Retries: manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 1, MaxSeconds: 60},
		},
		Auth: manifest.NormalizedAuth{SecretRefs: []string{"env:S"}},
	}
}

func newBackend(t *testing.T) (*Backend, string) {
	t.Helper()
	dir := t.TempDir()
	jp := filepath.Join(dir, "vercel.json")
	b, err := New(Options{JsonPath: jp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b, jp
}

// readVercelJSON parses the file directly so tests can assert on the
// shape vercel will see, not just what List returns.
func readVercelJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out
}

func TestCreateOnEmptyVercelJsonWrites5FieldCron(t *testing.T) {
	b, jp := newBackend(t)
	job := sampleJob("reconcile-payments", "*/15 * * * *")
	if err := b.Create(t.Context(), "billing", job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := readVercelJSON(t, jp)
	crons, ok := got["crons"].([]any)
	if !ok || len(crons) != 1 {
		t.Fatalf("want 1 cron entry, got %#v", got["crons"])
	}
	c := crons[0].(map[string]any)
	if c["path"] != "/api/v1/scheduled/reconcile-payments" {
		t.Errorf("path: got %q", c["path"])
	}
	if c["schedule"] != "*/15 * * * *" {
		t.Errorf("schedule: got %q", c["schedule"])
	}
}

func TestCreatePreservesUnknownTopLevelKeys(t *testing.T) {
	b, jp := newBackend(t)
	// Pre-write a vercel.json with non-cron config the user owns.
	initial := `{
  "buildCommand": "pnpm build",
  "framework": "hono",
  "regions": ["fra1"]
}
`
	if err := os.WriteFile(jp, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	job := sampleJob("reconcile-payments", "*/15 * * * *")
	if err := b.Create(t.Context(), "billing", job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := readVercelJSON(t, jp)
	if got["buildCommand"] != "pnpm build" {
		t.Errorf("buildCommand stripped: %#v", got["buildCommand"])
	}
	if got["framework"] != "hono" {
		t.Errorf("framework stripped: %#v", got["framework"])
	}
	regions, ok := got["regions"].([]any)
	if !ok || len(regions) != 1 || regions[0] != "fra1" {
		t.Errorf("regions stripped: %#v", got["regions"])
	}
	if _, ok := got["crons"]; !ok {
		t.Errorf("crons not written: %#v", got)
	}
}

func TestCreatePreservesNonCronixCronEntries(t *testing.T) {
	b, jp := newBackend(t)
	// Pre-existing crons[] with a hand-written entry the user owns.
	initial := `{
  "crons": [
    {"path": "/api/cleanup", "schedule": "0 3 * * *"}
  ]
}
`
	if err := os.WriteFile(jp, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	job := sampleJob("reconcile-payments", "*/15 * * * *")
	if err := b.Create(t.Context(), "billing", job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := readVercelJSON(t, jp)
	crons := got["crons"].([]any)
	if len(crons) != 2 {
		t.Fatalf("want 2 crons, got %d (%#v)", len(crons), crons)
	}
	paths := []string{}
	for _, c := range crons {
		paths = append(paths, c.(map[string]any)["path"].(string))
	}
	hasUserCron, hasCronixCron := false, false
	for _, p := range paths {
		if p == "/api/cleanup" {
			hasUserCron = true
		}
		if p == "/api/v1/scheduled/reconcile-payments" {
			hasCronixCron = true
		}
	}
	if !hasUserCron {
		t.Errorf("user's /api/cleanup cron was stripped — paths=%v", paths)
	}
	if !hasCronixCron {
		t.Errorf("cronix cron wasn't written — paths=%v", paths)
	}
}

func TestDeleteRemovesOnlyCronixOwnedEntries(t *testing.T) {
	b, jp := newBackend(t)
	initial := `{
  "crons": [
    {"path": "/api/cleanup", "schedule": "0 3 * * *"},
    {"path": "/api/v1/scheduled/reconcile-payments", "schedule": "*/15 * * * *"}
  ]
}
`
	if err := os.WriteFile(jp, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := b.Delete(t.Context(), "billing", "reconcile-payments"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got := readVercelJSON(t, jp)
	crons := got["crons"].([]any)
	if len(crons) != 1 || crons[0].(map[string]any)["path"] != "/api/cleanup" {
		t.Errorf("delete stripped non-cronix cron — got %#v", crons)
	}
}

func TestUpdateChangesScheduleOfOwnedEntry(t *testing.T) {
	b, jp := newBackend(t)
	if err := b.Create(t.Context(), "billing", sampleJob("reconcile-payments", "*/15 * * * *")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := b.Update(t.Context(), "billing", sampleJob("reconcile-payments", "*/30 * * * *")); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := readVercelJSON(t, jp)
	c := got["crons"].([]any)[0].(map[string]any)
	if c["schedule"] != "*/30 * * * *" {
		t.Errorf("schedule not updated: %v", c["schedule"])
	}
}

func TestListReturnsOnlyCronixOwnedEntries(t *testing.T) {
	b, jp := newBackend(t)
	initial := `{
  "crons": [
    {"path": "/api/cleanup", "schedule": "0 3 * * *"},
    {"path": "/api/v1/scheduled/reconcile-payments", "schedule": "*/15 * * * *"},
    {"path": "/api/v1/scheduled/send-invoices", "schedule": "0 * * * *"}
  ]
}
`
	if err := os.WriteFile(jp, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	entries, err := b.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d (%#v)", len(entries), entries)
	}
	jobs := map[string]bool{}
	for _, e := range entries {
		jobs[e.Job] = true
	}
	if !jobs["reconcile-payments"] || !jobs["send-invoices"] {
		t.Errorf("missing expected jobs in List output: %#v", entries)
	}
	if jobs["cleanup"] {
		t.Errorf("non-cronix /api/cleanup leaked into List output")
	}
}

func TestValidateRejectsAtShortcutsAndNonUtcTimezones(t *testing.T) {
	b, _ := newBackend(t)

	hourly := sampleJob("ping", "@hourly")
	r := b.Validate(hourly)
	if r.OK {
		t.Errorf("expected @hourly to be rejected")
	}

	tz := sampleJob("nightly", "0 3 * * *")
	tz.Timezone = "Europe/Paris"
	r = b.Validate(tz)
	if r.OK {
		t.Errorf("expected non-UTC timezone to be flagged")
	}
	found := false
	for _, msg := range r.Issues {
		if strings.Contains(msg, "Europe/Paris") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an issue mentioning Europe/Paris, got %v", r.Issues)
	}

	utcExplicit := sampleJob("midnight", "0 0 * * *")
	utcExplicit.Timezone = "UTC"
	r = b.Validate(utcExplicit)
	if !r.OK {
		t.Errorf("UTC timezone should validate, got %v", r.Issues)
	}
}

func TestRoundtripIsIdempotent(t *testing.T) {
	// Create the same job twice in sequence — the second write must
	// produce the byte-identical vercel.json (D-027 idempotency).
	// We verify that by reading the file after the first Create and
	// after a no-op Update produces the same job → same bytes.
	b, jp := newBackend(t)
	job := sampleJob("reconcile-payments", "*/15 * * * *")

	if err := b.Create(t.Context(), "billing", job); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	first, err := os.ReadFile(jp)
	if err != nil {
		t.Fatalf("read after first: %v", err)
	}
	if err := b.Update(t.Context(), "billing", job); err != nil {
		t.Fatalf("Update with same job: %v", err)
	}
	second, err := os.ReadFile(jp)
	if err != nil {
		t.Fatalf("read after update: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("Update with identical job produced different bytes:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestEnsureRejectsMissingDirectory(t *testing.T) {
	b, err := New(Options{JsonPath: "/nonexistent-cronix-test/vercel.json"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Ensure(context.Background()); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected ErrNotExist, got %v", err)
	}
}

func TestHistoryReturnsEmpty(t *testing.T) {
	b, _ := newBackend(t)
	out, err := b.History(t.Context(), backends.HistoryOpts{App: "billing", Job: "reconcile-payments"})
	if err != nil {
		t.Errorf("History returned err: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty history, got %d entries", len(out))
	}
}
