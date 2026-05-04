package systemd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/awbx/cronix/go/internal/manifest"
)

func sampleJob(name string, schedules ...string) manifest.NormalizedJob {
	return manifest.NormalizedJob{
		Name:      name,
		Schedules: schedules,
		Timezone:  "UTC",
		Request: manifest.NormalizedRequest{
			Method: "POST", URL: "https://example.com/" + name,
			Headers: map[string]string{}, Body: "",
		},
		Policy: manifest.NormalizedPolicy{
			Concurrency: "Forbid", ConcurrencyScope: "host", TimeoutSeconds: 60,
			Retries: manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 1, MaxSeconds: 60},
		},
		Auth: manifest.NormalizedAuth{SecretRefs: []string{}},
	}
}

// recordingExec captures every systemctl invocation so tests can assert order.
type recordingExec struct {
	calls    [][]string
	failNext error
}

func (r *recordingExec) Run(_ context.Context, args ...string) error {
	r.calls = append(r.calls, append([]string(nil), args...))
	if r.failNext != nil {
		err := r.failNext
		r.failNext = nil
		return err
	}
	return nil
}

func newTestBackend(t *testing.T) (*Backend, *recordingExec, string) {
	t.Helper()
	dir := t.TempDir()
	exec := &recordingExec{}
	b, err := New(Options{
		UnitDir:    dir,
		TriggerBin: "/usr/local/bin/cronix",
		Systemctl:  exec,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return b, exec, dir
}

func TestTranslateShortcuts(t *testing.T) {
	cases := map[string]string{
		"@hourly":   "hourly",
		"@daily":    "daily",
		"@midnight": "daily",
		"@weekly":   "weekly",
		"@monthly":  "monthly",
		"@yearly":   "yearly",
		"@annually": "yearly",
	}
	for in, want := range cases {
		got, err := translateOnCalendar(in)
		if err != nil || got != want {
			t.Errorf("translate(%q) = (%q, %v), want %q", in, got, err, want)
		}
	}
}

func TestTranslate5Field(t *testing.T) {
	got, err := translateOnCalendar("0 2 * * *")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !strings.Contains(got, "2:0:00") || !strings.Contains(got, "*-*-*") {
		t.Errorf("got %q", got)
	}
}

func TestTranslateWithDOW(t *testing.T) {
	got, err := translateOnCalendar("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !strings.HasPrefix(got, "Mon..Fri") {
		t.Errorf("expected Mon..Fri prefix, got %q", got)
	}
}

func TestRenderUnits(t *testing.T) {
	job := sampleJob("ping", "@hourly")
	timerFile, serviceFile, err := RenderUnits("/usr/local/bin/cronix", "billing", job, 0)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"OnCalendar=hourly", "X-Cronix-App=billing", "X-Cronix-Job=ping", "PartOf=cronix-billing-ping-0.service"} {
		if !strings.Contains(timerFile, want) {
			t.Errorf("timer missing %q:\n%s", want, timerFile)
		}
	}
	for _, want := range []string{"ExecStart=/usr/local/bin/cronix trigger billing.ping", "RuntimeMaxSec=90", "X-Cronix-Job=ping"} {
		if !strings.Contains(serviceFile, want) {
			t.Errorf("service missing %q:\n%s", want, serviceFile)
		}
	}
}

func TestValidateRejectsUnsupportedSchedule(t *testing.T) {
	b, _, _ := newTestBackend(t)
	res := b.Validate(sampleJob("x", "not-a-cron"))
	if res.OK {
		t.Errorf("expected validation failure")
	}
}

func TestCreateWritesUnitsAndEnables(t *testing.T) {
	b, exec, dir := newTestBackend(t)
	job := sampleJob("reconcile", "@hourly", "*/15 * * * *")
	if err := b.Create(context.Background(), "billing", job); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, suffix := range []string{
		"cronix-billing-reconcile-0.timer",
		"cronix-billing-reconcile-0.service",
		"cronix-billing-reconcile-1.timer",
		"cronix-billing-reconcile-1.service",
	} {
		path := filepath.Join(dir, suffix)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected unit file %s: %v", path, err)
		}
		if !strings.Contains(string(raw), "X-Cronix-Hash=") {
			t.Errorf("unit %s missing X-Cronix-Hash", suffix)
		}
	}
	wantCalls := [][]string{
		{"daemon-reload"},
		{"enable", "--now", "cronix-billing-reconcile-0.timer"},
		{"enable", "--now", "cronix-billing-reconcile-1.timer"},
	}
	if len(exec.calls) != len(wantCalls) {
		t.Fatalf("got %d systemctl calls, want %d: %v", len(exec.calls), len(wantCalls), exec.calls)
	}
	for i, want := range wantCalls {
		if !equalSlices(exec.calls[i], want) {
			t.Errorf("call[%d] = %v, want %v", i, exec.calls[i], want)
		}
	}
}

func TestListSkipsForeignUnits(t *testing.T) {
	b, _, dir := newTestBackend(t)
	if err := b.Create(context.Background(), "billing", sampleJob("reconcile", "@hourly")); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Drop a non-cronix timer into the unit dir — List must skip it.
	foreign := filepath.Join(dir, "cronix-foreign.timer")
	if err := os.WriteFile(foreign, []byte("[Unit]\nDescription=manual\n"), 0o644); err != nil {
		t.Fatalf("write foreign: %v", err)
	}
	entries, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 owned entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].App != "billing" || entries[0].Job != "reconcile" {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
	if entries[0].Hash == "" {
		t.Errorf("expected hash to be parsed from X-Cronix-Hash")
	}
}

func TestUpdateReplacesOldUnits(t *testing.T) {
	b, _, dir := newTestBackend(t)
	if err := b.Create(context.Background(), "billing", sampleJob("reconcile", "@hourly")); err != nil {
		t.Fatalf("create: %v", err)
	}
	originals, _ := b.List(context.Background())
	originalHash := originals[0].Hash

	updated := sampleJob("reconcile", "@daily")
	if err := b.Update(context.Background(), "billing", updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	entries, _ := b.List(context.Background())
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after update, got %d", len(entries))
	}
	if entries[0].Hash == originalHash {
		t.Errorf("expected hash to change after schedule update")
	}
	timerPath := filepath.Join(dir, "cronix-billing-reconcile-0.timer")
	raw, _ := os.ReadFile(timerPath)
	if !strings.Contains(string(raw), "OnCalendar=daily") {
		t.Errorf("timer not updated to @daily; got:\n%s", raw)
	}
}

func TestDeleteRemovesAllOwnedUnits(t *testing.T) {
	b, exec, dir := newTestBackend(t)
	if err := b.Create(context.Background(), "billing", sampleJob("reconcile", "@hourly", "*/15 * * * *")); err != nil {
		t.Fatalf("create: %v", err)
	}
	exec.calls = nil
	if err := b.Delete(context.Background(), "billing", "reconcile"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "cronix-billing-reconcile-*"))
	if len(matches) != 0 {
		t.Errorf("expected 0 owned files, got: %v", matches)
	}
	// Disable was called for each timer, then daemon-reload exactly once.
	disables := 0
	reloads := 0
	for _, c := range exec.calls {
		if len(c) >= 1 && c[0] == "disable" {
			disables++
		}
		if len(c) == 1 && c[0] == "daemon-reload" {
			reloads++
		}
	}
	if disables != 2 {
		t.Errorf("expected 2 disable calls, got %d (%v)", disables, exec.calls)
	}
	if reloads != 1 {
		t.Errorf("expected 1 daemon-reload after delete, got %d (%v)", reloads, exec.calls)
	}
}

func TestEnsureFailsWhenNotSystemd(t *testing.T) {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		t.Skip("running on a systemd host; skip negative-path Ensure test")
	}
	b, _, _ := newTestBackend(t)
	if err := b.Ensure(context.Background()); err == nil {
		t.Errorf("expected Ensure to fail when /run/systemd/system is absent")
	}
}

func equalSlices(a, b []string) bool {
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
