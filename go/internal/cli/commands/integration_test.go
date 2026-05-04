package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleManifest = `{
  "version": 1,
  "app": "billing",
  "jobs": [
    { "name": "ping", "schedule": "@hourly",
      "request": { "url": "https://billing.example.com/api/v1/scheduled/ping" } }
  ]
}`

const updatedManifest = `{
  "version": 1,
  "app": "billing",
  "jobs": [
    { "name": "ping", "schedule": "*/15 * * * *",
      "request": { "url": "https://billing.example.com/api/v1/scheduled/ping" } }
  ]
}`

func runRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

func TestValidateAcceptsValid(t *testing.T) {
	dir := t.TempDir()
	mp := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(mp, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stdout, _, err := runRoot(t, "validate", mp)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !strings.Contains(stdout, "OK  app=billing jobs=1") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestValidateRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	mp := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(mp, []byte(`{"version":1,"app":"BadApp","jobs":[]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, stderr, err := runRoot(t, "validate", mp)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr, "INVALID") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestApplyCreateUpdateDeleteNoop(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	crontabPath := filepath.Join(dir, "crontab")
	specDir := filepath.Join(dir, "specs")

	if err := os.WriteFile(manifestPath, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	commonArgs := []string{
		"apply",
		"--manifest", manifestPath,
		"--backend", "crontab",
		"--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix",
		"--spec-dir", specDir,
		"--secret-ref", "raw:test",
		"-o", "json",
	}

	// 1) apply: should create.
	stdout, _, err := runRoot(t, commonArgs...)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	var first applySummary
	if err := json.Unmarshal([]byte(stdout), &first); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout)
	}
	if first.Created != 1 || first.Updated != 0 || first.Deleted != 0 {
		t.Errorf("first apply summary = %+v, want created=1", first)
	}

	// 2) apply same manifest: should be noop (skipped=1).
	stdout, _, err = runRoot(t, commonArgs...)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	var second applySummary
	if err := json.Unmarshal([]byte(stdout), &second); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if second.Created+second.Updated+second.Deleted != 0 {
		t.Errorf("noop expected, got %+v", second)
	}

	// 3) update manifest: should update.
	if err := os.WriteFile(manifestPath, []byte(updatedManifest), 0o644); err != nil {
		t.Fatalf("write updated: %v", err)
	}
	stdout, _, err = runRoot(t, commonArgs...)
	if err != nil {
		t.Fatalf("update apply: %v", err)
	}
	var third applySummary
	if err := json.Unmarshal([]byte(stdout), &third); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if third.Updated != 1 {
		t.Errorf("update expected, got %+v", third)
	}

	// 4) list: should show one entry.
	stdout, _, err = runRoot(t,
		"list", "--backend", "crontab", "--crontab-path", crontabPath, "-o", "json",
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stdout, `"App": "billing"`) || !strings.Contains(stdout, `"Job": "ping"`) {
		t.Errorf("list output missing entry:\n%s", stdout)
	}

	// 5) drift after applying the same manifest: should be noop.
	stdout, _, err = runRoot(t,
		"drift", "--manifest", manifestPath, "--backend", "crontab", "--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix", "-o", "json",
	)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if !strings.Contains(stdout, `"noop": true`) {
		t.Errorf("expected noop drift after apply, got:\n%s", stdout)
	}

	// 6) plan with a manifest change: should show update.
	if err := os.WriteFile(manifestPath, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("revert manifest: %v", err)
	}
	stdout, _, err = runRoot(t,
		"plan", "--manifest", manifestPath, "--backend", "crontab", "--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix", "--secret-ref", "raw:test", "-o", "json",
	)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !strings.Contains(stdout, `"action": "update"`) {
		t.Errorf("plan should show update, got:\n%s", stdout)
	}

	// 7) Verify the spec file the trigger shim would read.
	specPath := filepath.Join(specDir, "billing.ping.json")
	if _, err := os.Stat(specPath); err != nil {
		t.Errorf("expected spec %s: %v", specPath, err)
	}
}

const twoJobManifest = `{
  "version": 1,
  "app": "billing",
  "jobs": [
    { "name": "ping", "schedule": "@hourly",
      "request": { "url": "https://billing.example.com/api/v1/scheduled/ping" } },
    { "name": "pong", "schedule": "@daily",
      "request": { "url": "https://billing.example.com/api/v1/scheduled/pong" } }
  ]
}`

// TestApplyRemovesOrphanSpecOnDelete pins the bug found by exercising the
// reconcile lifecycle end-to-end: applying a manifest that drops a previously
// declared job must remove its <app>.<job>.json spec from --spec-dir, so the
// trigger shim cannot re-fire a stale spec.
func TestApplyRemovesOrphanSpecOnDelete(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	crontabPath := filepath.Join(dir, "crontab")
	specDir := filepath.Join(dir, "specs")

	args := []string{
		"apply", "--manifest", manifestPath,
		"--backend", "crontab", "--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix",
		"--spec-dir", specDir,
		"--secret-ref", "raw:test", "-o", "json",
	}

	if err := os.WriteFile(manifestPath, []byte(twoJobManifest), 0o644); err != nil {
		t.Fatalf("write 2-job manifest: %v", err)
	}
	if _, _, err := runRoot(t, args...); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	for _, name := range []string{"billing.ping.json", "billing.pong.json"} {
		if _, err := os.Stat(filepath.Join(specDir, name)); err != nil {
			t.Fatalf("expected spec %s after first apply: %v", name, err)
		}
	}

	if err := os.WriteFile(manifestPath, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("write 1-job manifest: %v", err)
	}
	if _, _, err := runRoot(t, args...); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(specDir, "billing.ping.json")); err != nil {
		t.Errorf("ping spec should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(specDir, "billing.pong.json")); !os.IsNotExist(err) {
		t.Errorf("pong spec should have been removed, got err=%v", err)
	}
}

func TestPlanNoopFromCleanCrontab(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	crontabPath := filepath.Join(dir, "crontab")
	if err := os.WriteFile(manifestPath, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stdout, _, err := runRoot(t,
		"plan", "--manifest", manifestPath, "--backend", "crontab",
		"--crontab-path", crontabPath, "--trigger-bin", "/usr/local/bin/cronix",
		"--secret-ref", "raw:x", "-o", "json",
	)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !strings.Contains(stdout, `"action": "create"`) {
		t.Errorf("expected create on clean crontab, got:\n%s", stdout)
	}
}

func TestCompletionEmitsScript(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		stdout, _, err := runRoot(t, "completion", shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		if !strings.Contains(stdout, "cronix") {
			t.Errorf("%s output missing program name (len=%d)", shell, len(stdout))
		}
	}
}

func TestVersionStill(t *testing.T) {
	stdout, _, err := runRoot(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(stdout, "cronix") {
		t.Errorf("missing program name: %q", stdout)
	}
}

type applySummary struct {
	Backend string `json:"backend"`
	Created int    `json:"created"`
	Updated int    `json:"updated"`
	Deleted int    `json:"deleted"`
	Skipped int    `json:"skipped"`
	Noop    bool   `json:"noop"`
}
