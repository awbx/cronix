package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalYAML = `
log_level: debug
manifest_sources:
  - app: billing-service
    url: https://billing.internal/.well-known/cron-manifest
    secret_refs:
      - env:CRON_SECRET_V2
      - env:CRON_SECRET_V1
locks:
  default: flock
  flock:
    dir: /tmp/cronix-locks
defaults:
  timeout_seconds: 120
  retries:
    max_attempts: 5
    min_seconds: 2
    max_seconds: 30
`

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte(minimalYAML), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("log_level: want debug, got %s", cfg.LogLevel)
	}
	if len(cfg.ManifestSources) != 1 {
		t.Fatalf("expected 1 manifest source, got %d", len(cfg.ManifestSources))
	}
	if cfg.ManifestSources[0].App != "billing-service" {
		t.Fatalf("app mismatch: %s", cfg.ManifestSources[0].App)
	}
	if cfg.Defaults.TimeoutSeconds != 120 {
		t.Fatalf("timeout: want 120, got %d", cfg.Defaults.TimeoutSeconds)
	}
}

func TestLoadEmptyPathReturnsDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("default log_level: %s", cfg.LogLevel)
	}
	if cfg.Locks.Default != "flock" {
		t.Fatalf("default lock backend: %s", cfg.Locks.Default)
	}
	if cfg.Defaults.TimeoutSeconds != 60 {
		t.Fatalf("default timeout: %d", cfg.Defaults.TimeoutSeconds)
	}
}

func TestRejectInvalidLogLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte(`log_level: trace`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "log_level") {
		t.Fatalf("expected log_level error, got %v", err)
	}
}

func TestRejectMissingManifestURL(t *testing.T) {
	yamlSrc := `manifest_sources:
  - app: x
    secret_refs: ["env:S"]`
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte(yamlSrc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("expected url error, got %v", err)
	}
}

func TestRejectInsecureManifestURL(t *testing.T) {
	yamlSrc := `manifest_sources:
  - app: x
    url: http://example.com/manifest
    secret_refs: ["env:S"]`
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte(yamlSrc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected insecure-url error, got %v", err)
	}
}

func TestAcceptLocalhostHTTP(t *testing.T) {
	yamlSrc := `manifest_sources:
  - app: x
    url: http://localhost:3000/.well-known/cron-manifest
    secret_refs: ["env:S"]`
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte(yamlSrc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("expected localhost http to be accepted, got %v", err)
	}
}

func TestRejectMalformedSecretRef(t *testing.T) {
	yamlSrc := `manifest_sources:
  - app: x
    url: https://example.com/manifest
    secret_refs: ["just-a-name"]`
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte(yamlSrc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "secret_refs") {
		t.Fatalf("expected secret_refs error, got %v", err)
	}
}

func TestRejectRetryMinGreaterThanMax(t *testing.T) {
	yamlSrc := `defaults:
  retries:
    min_seconds: 30
    max_seconds: 10`
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte(yamlSrc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "min_seconds") {
		t.Fatalf("expected retries error, got %v", err)
	}
}

func TestRejectUnknownFields(t *testing.T) {
	yamlSrc := `unknown_field: 1`
	dir := t.TempDir()
	path := filepath.Join(dir, "cronix.yaml")
	if err := os.WriteFile(path, []byte(yamlSrc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown_field") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}
