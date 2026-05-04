package crontab

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/awbx/cronix/go/internal/manifest"
)

func newBackend(t *testing.T, initial string) *Backend {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "crontab")
	if initial != "" {
		if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
			t.Fatalf("write initial: %v", err)
		}
	}
	b, err := New(Options{Path: path, TriggerBin: "/usr/local/bin/cronix"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return b
}

func read(t *testing.T, b *Backend) string {
	t.Helper()
	raw, err := os.ReadFile(b.path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(raw)
}

func sampleJob(name string, schedules ...string) manifest.NormalizedJob {
	return manifest.NormalizedJob{
		Name:      name,
		Schedules: schedules,
		Timezone:  "UTC",
		Request: manifest.NormalizedRequest{
			Method:  "POST",
			URL:     "https://example.com/api/v1/scheduled/" + name,
			Headers: map[string]string{},
			Body:    "",
		},
		Policy: manifest.NormalizedPolicy{
			Concurrency:      "Forbid",
			ConcurrencyScope: "host",
			TimeoutSeconds:   60,
			Retries:          manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 1, MaxSeconds: 60},
		},
		Auth: manifest.NormalizedAuth{SecretRefs: []string{}},
	}
}

func TestCreatePreservesNonOwnedLines(t *testing.T) {
	initial := "# user comment\nMAILTO=ops@example.com\n0 0 * * * /opt/manual.sh\n"
	b := newBackend(t, initial)

	if err := b.Create(context.TODO(), "billing", sampleJob("ping", "@hourly")); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := read(t, b)
	for _, want := range []string{"# user comment", "MAILTO=ops@example.com", "0 0 * * * /opt/manual.sh"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing preserved line %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "0 * * * * /usr/local/bin/cronix trigger billing.ping") {
		t.Errorf("missing schedule line:\n%s", got)
	}
	if !strings.Contains(got, "# cronix:owned app=billing job=ping hash=") {
		t.Errorf("missing ownership marker:\n%s", got)
	}
}

func TestListReportsOnlyOwned(t *testing.T) {
	initial := "0 0 * * * /opt/unrelated.sh\n# unrelated comment\n"
	b := newBackend(t, initial)
	if err := b.Create(context.TODO(), "billing", sampleJob("ping", "@hourly")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Create(context.TODO(), "ops", sampleJob("watchdog", "*/5 * * * *")); err != nil {
		t.Fatalf("create: %v", err)
	}
	entries, err := b.List(context.TODO())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	apps := map[string]string{entries[0].App: entries[0].Job, entries[1].App: entries[1].Job}
	if apps["billing"] != "ping" || apps["ops"] != "watchdog" {
		t.Errorf("unexpected entries: %+v", entries)
	}
}

func TestUpdateChangesScheduleNotOthers(t *testing.T) {
	initial := "0 0 * * * /opt/unrelated.sh\n"
	b := newBackend(t, initial)
	if err := b.Create(context.TODO(), "billing", sampleJob("ping", "@hourly")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Update(context.TODO(), "billing", sampleJob("ping", "*/5 * * * *")); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := read(t, b)
	if !strings.Contains(got, "*/5 * * * * /usr/local/bin/cronix trigger billing.ping") {
		t.Errorf("missing updated schedule:\n%s", got)
	}
	if strings.Contains(got, "0 * * * * /usr/local/bin/cronix trigger billing.ping") {
		t.Errorf("old schedule still present:\n%s", got)
	}
	if !strings.Contains(got, "0 0 * * * /opt/unrelated.sh") {
		t.Errorf("non-owned line removed:\n%s", got)
	}
}

func TestDeleteRemovesOnlyOwnedBlocks(t *testing.T) {
	initial := "0 0 * * * /opt/unrelated.sh\n"
	b := newBackend(t, initial)
	if err := b.Create(context.TODO(), "billing", sampleJob("ping", "@hourly")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Delete(context.TODO(), "billing", "ping"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got := read(t, b)
	if strings.Contains(got, "billing.ping") {
		t.Errorf("owned block still present:\n%s", got)
	}
	if !strings.Contains(got, "0 0 * * * /opt/unrelated.sh") {
		t.Errorf("unrelated line removed:\n%s", got)
	}
}

func TestMultiScheduleJobProducesMultipleBlocks(t *testing.T) {
	b := newBackend(t, "")
	job := sampleJob("watchdog", "*/5 9-17 * * 1-5", "0 * * * 0,6")
	if err := b.Create(context.TODO(), "ops", job); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := read(t, b)
	if !strings.Contains(got, "*/5 9-17 * * 1-5 /usr/local/bin/cronix trigger ops.watchdog") {
		t.Errorf("missing first schedule:\n%s", got)
	}
	if !strings.Contains(got, "0 * * * 0,6 /usr/local/bin/cronix trigger ops.watchdog") {
		t.Errorf("missing second schedule:\n%s", got)
	}
	entries, err := b.List(context.TODO())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	idxs := map[int]bool{entries[0].Index: true, entries[1].Index: true}
	if !idxs[0] || !idxs[1] {
		t.Errorf("expected idx 0 and 1, got %+v", entries)
	}
}

func TestHashChangesWithSchedule(t *testing.T) {
	a := sampleJob("ping", "@hourly")
	b := sampleJob("ping", "@daily")
	hashA := hashJobSchedule(a, 0)
	hashB := hashJobSchedule(b, 0)
	if hashA == hashB {
		t.Errorf("expected different hashes for different schedules, got %q", hashA)
	}
}

func TestHashStableForSameInput(t *testing.T) {
	a := sampleJob("ping", "@hourly")
	first := hashJobSchedule(a, 0)
	second := hashJobSchedule(a, 0)
	if first != second {
		t.Errorf("hash not deterministic: %q vs %q", first, second)
	}
}

func TestHashDistinctPerScheduleIndex(t *testing.T) {
	job := sampleJob("watchdog", "@hourly", "@daily")
	if hashJobSchedule(job, 0) == hashJobSchedule(job, 1) {
		t.Errorf("expected distinct hashes per index")
	}
}

func TestValidateRejectsSubMinute(t *testing.T) {
	b := newBackend(t, "")
	res := b.Validate(sampleJob("x", "@every 30s"))
	if res.OK {
		t.Errorf("expected validation failure for @every 30s")
	}
}

func TestValidateFlagsTimezone(t *testing.T) {
	b := newBackend(t, "")
	job := sampleJob("x", "@hourly")
	job.Timezone = "Europe/Paris"
	res := b.Validate(job)
	if res.OK {
		t.Errorf("expected validation issue for non-UTC timezone")
	}
}

func TestTranslateShortcuts(t *testing.T) {
	cases := map[string]string{
		"@hourly":     "0 * * * *",
		"@daily":      "0 0 * * *",
		"@midnight":   "0 0 * * *",
		"@weekly":     "0 0 * * 0",
		"@monthly":    "0 0 1 * *",
		"@yearly":     "0 0 1 1 *",
		"@annually":   "0 0 1 1 *",
		"@every 5m":   "*/5 * * * *",
		"@every 10m":  "*/10 * * * *",
		"@every 6h":   "0 */6 * * *",
		"*/15 * * * *": "*/15 * * * *",
	}
	for in, want := range cases {
		got, ok := translate(in)
		if !ok || got != want {
			t.Errorf("translate(%q) = (%q, %v), want (%q, true)", in, got, ok, want)
		}
	}
}

func TestTranslateRejects(t *testing.T) {
	for _, bad := range []string{"@every 30s", "@every 7m", "0 0 * * * *", "not-a-cron"} {
		if _, ok := translate(bad); ok {
			t.Errorf("translate(%q) should fail", bad)
		}
	}
}

func TestEnsureCreatesFile(t *testing.T) {
	b := newBackend(t, "")
	// Remove the file the helper created.
	_ = os.Remove(b.path)
	if err := b.Ensure(context.TODO()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := os.Stat(b.path); err != nil {
		t.Fatalf("expected ensure to create %s: %v", b.path, err)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	b := newBackend(t, "0 0 * * * /opt/unrelated.sh\n")
	job := sampleJob("ping", "@hourly")
	if err := b.Create(context.TODO(), "billing", job); err != nil {
		t.Fatalf("create: %v", err)
	}
	first := read(t, b)
	// Update with the same job — content should be identical.
	if err := b.Update(context.TODO(), "billing", job); err != nil {
		t.Fatalf("update: %v", err)
	}
	second := read(t, b)
	if first != second {
		t.Errorf("update with same job changed content:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
