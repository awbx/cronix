package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPruneCrontabRemovesAllEntries seeds a crontab with two cronix-owned
// jobs (one billing, one reports), runs prune --yes, and asserts both are
// removed and unmanaged lines are preserved.
func TestPruneCrontabRemovesAllEntries(t *testing.T) {
	dir := t.TempDir()
	crontabPath := filepath.Join(dir, "crontab")
	specDir := filepath.Join(dir, "specs")

	billing := filepath.Join(dir, "billing.json")
	reports := filepath.Join(dir, "reports.json")
	if err := os.WriteFile(billing, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("write billing: %v", err)
	}
	const reportsManifest = `{
	  "version": 1,
	  "app": "reports",
	  "jobs": [
	    { "name": "daily-roll", "schedule": "@daily",
	      "request": { "url": "https://reports.example.com/api/v1/scheduled/daily-roll" } }
	  ]
	}`
	if err := os.WriteFile(reports, []byte(reportsManifest), 0o644); err != nil {
		t.Fatalf("write reports: %v", err)
	}

	// Seed the crontab with one unmanaged line that prune must NOT touch.
	if err := os.WriteFile(crontabPath, []byte("# user-managed: do not delete\n0 5 * * * /usr/local/bin/manual-task\n"), 0o644); err != nil {
		t.Fatalf("seed crontab: %v", err)
	}

	commonFlags := []string{
		"--backend", "crontab",
		"--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix",
		"--spec-dir", specDir,
	}
	if _, _, err := runRoot(t, append([]string{"apply", "--manifest", billing}, commonFlags...)...); err != nil {
		t.Fatalf("apply billing: %v", err)
	}
	if _, _, err := runRoot(t, append([]string{"apply", "--manifest", reports}, commonFlags...)...); err != nil {
		t.Fatalf("apply reports: %v", err)
	}

	stdout, _, err := runRoot(t,
		"prune",
		"--backend", "crontab",
		"--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix",
		"--yes",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	var report struct {
		Backend string `json:"backend"`
		Pruned  int    `json:"pruned"`
		Jobs    []struct {
			App string `json:"app"`
			Job string `json:"job"`
		} `json:"jobs"`
	}
	if err := json.NewDecoder(strings.NewReader(stdout)).Decode(&report); err != nil {
		t.Fatalf("decode prune output: %v\noutput: %s", err, stdout)
	}
	if report.Backend != "crontab" || report.Pruned != 2 || len(report.Jobs) != 2 {
		t.Errorf("unexpected prune report: %+v", report)
	}

	raw, _ := os.ReadFile(crontabPath)
	if !strings.Contains(string(raw), "manual-task") {
		t.Errorf("prune destroyed unmanaged line:\n%s", raw)
	}
	if strings.Contains(string(raw), "cronix:owned") {
		t.Errorf("prune left cronix-owned entries:\n%s", raw)
	}
}

// TestPruneAppFilter ensures --app only removes entries belonging to that app.
func TestPruneAppFilter(t *testing.T) {
	dir := t.TempDir()
	crontabPath := filepath.Join(dir, "crontab")
	specDir := filepath.Join(dir, "specs")

	billing := filepath.Join(dir, "billing.json")
	reports := filepath.Join(dir, "reports.json")
	const reportsManifest = `{
	  "version": 1,
	  "app": "reports",
	  "jobs": [
	    { "name": "daily-roll", "schedule": "@daily",
	      "request": { "url": "https://reports.example.com/api/v1/scheduled/daily-roll" } }
	  ]
	}`
	if err := os.WriteFile(billing, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("write billing: %v", err)
	}
	if err := os.WriteFile(reports, []byte(reportsManifest), 0o644); err != nil {
		t.Fatalf("write reports: %v", err)
	}

	commonFlags := []string{
		"--backend", "crontab",
		"--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix",
		"--spec-dir", specDir,
	}
	for _, src := range []string{billing, reports} {
		if _, _, err := runRoot(t, append([]string{"apply", "--manifest", src}, commonFlags...)...); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}

	stdout, _, err := runRoot(t,
		"prune",
		"--backend", "crontab",
		"--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix",
		"--yes",
		"--app", "billing",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if !strings.Contains(stdout, `"app": "billing"`) || strings.Contains(stdout, `"app": "reports"`) {
		t.Errorf("expected only billing pruned, got: %s", stdout)
	}

	raw, _ := os.ReadFile(crontabPath)
	if strings.Contains(string(raw), "app=billing") {
		t.Errorf("billing entries still present after --app=billing prune:\n%s", raw)
	}
	if !strings.Contains(string(raw), "app=reports") {
		t.Errorf("reports entries should remain:\n%s", raw)
	}
}
