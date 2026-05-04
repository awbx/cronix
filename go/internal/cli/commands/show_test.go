package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShowReportsInSyncAfterApply(t *testing.T) {
	dir := t.TempDir()
	crontabPath := filepath.Join(dir, "crontab")
	specDir := filepath.Join(dir, "specs")
	mp := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(mp, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	commonFlags := []string{
		"--backend", "crontab",
		"--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix",
	}
	if _, _, err := runRoot(t, append([]string{"apply", "--manifest", mp, "--spec-dir", specDir}, commonFlags...)...); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stdout, _, err := runRoot(t, append([]string{"show", "billing.ping", "--manifest", mp, "--output", "json"}, commonFlags...)...)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var rep struct {
		Backend string `json:"backend"`
		App     string `json:"app"`
		Job     string `json:"job"`
		Found   bool   `json:"found"`
		InSync  *bool  `json:"in_sync"`
		Entries []any  `json:"entries"`
	}
	if err := json.NewDecoder(strings.NewReader(stdout)).Decode(&rep); err != nil {
		t.Fatalf("decode show: %v\noutput:%s", err, stdout)
	}
	if !rep.Found || rep.App != "billing" || rep.Job != "ping" || rep.Backend != "crontab" {
		t.Errorf("unexpected report: %+v", rep)
	}
	if rep.InSync == nil || !*rep.InSync {
		t.Errorf("expected in_sync=true, got %v", rep.InSync)
	}
	if len(rep.Entries) == 0 {
		t.Errorf("expected at least one entry")
	}
}

func TestShowReportsDriftWhenManifestUpdated(t *testing.T) {
	dir := t.TempDir()
	crontabPath := filepath.Join(dir, "crontab")
	specDir := filepath.Join(dir, "specs")
	mpV1 := filepath.Join(dir, "v1.json")
	mpV2 := filepath.Join(dir, "v2.json")
	if err := os.WriteFile(mpV1, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	if err := os.WriteFile(mpV2, []byte(updatedManifest), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	commonFlags := []string{
		"--backend", "crontab",
		"--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix",
	}
	// Install v1, then ask `show` to compare against v2 — expect drift.
	if _, _, err := runRoot(t, append([]string{"apply", "--manifest", mpV1, "--spec-dir", specDir}, commonFlags...)...); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	stdout, _, err := runRoot(t, append([]string{"show", "billing.ping", "--manifest", mpV2, "--output", "json"}, commonFlags...)...)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var rep struct {
		InSync *bool `json:"in_sync"`
	}
	_ = json.NewDecoder(strings.NewReader(stdout)).Decode(&rep)
	if rep.InSync == nil || *rep.InSync {
		t.Errorf("expected drift (in_sync=false), got %v", rep.InSync)
	}
}

func TestShowReturnsNotFoundForUnknownJob(t *testing.T) {
	dir := t.TempDir()
	crontabPath := filepath.Join(dir, "crontab")
	if err := os.WriteFile(crontabPath, []byte(""), 0o644); err != nil {
		t.Fatalf("seed crontab: %v", err)
	}
	stdout, _, err := runRoot(t,
		"show", "billing.ghost",
		"--backend", "crontab",
		"--crontab-path", crontabPath,
		"--trigger-bin", "/usr/local/bin/cronix",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var rep struct {
		Found bool `json:"found"`
	}
	_ = json.NewDecoder(strings.NewReader(stdout)).Decode(&rep)
	if rep.Found {
		t.Errorf("expected found=false for unknown job, got true")
	}
}
