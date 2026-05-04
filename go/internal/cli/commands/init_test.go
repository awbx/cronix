package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/awbx/cronix/go/internal/config"
)

func TestInitWritesValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if _, _, err := runRoot(t,
		"init",
		"--config", path,
		"--app", "billing",
		"--manifest-url", "https://billing.example.com/.well-known/cron-manifest",
		"--secret-ref", "env:CRON_SECRET",
	); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("scaffolded config does not load: %v", err)
	}
	if len(cfg.ManifestSources) != 1 || cfg.ManifestSources[0].App != "billing" {
		t.Errorf("manifest_sources not pre-filled: %+v", cfg.ManifestSources)
	}
}

func TestInitRefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte("# already there\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := runRoot(t, "init", "--config", path)
	if err == nil {
		t.Errorf("expected init to refuse overwriting existing file")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error didn't mention overwrite: %v", err)
	}
}

func TestInitForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte("# already there\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := runRoot(t, "init", "--config", path, "--force"); err != nil {
		t.Fatalf("init --force: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "log_level: info") {
		t.Errorf("force did not overwrite with template:\n%s", raw)
	}
}

func TestInitDefaultTemplateLoadsCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if _, _, err := runRoot(t, "init", "--config", path); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := config.Load(path); err != nil {
		t.Errorf("default template doesn't validate: %v", err)
	}
}
