// Package config loads and validates the operator configuration
// (cronix.yaml). Resolved order (first match wins):
//
//  1. --config <path> on the CLI
//  2. $CRONIX_CONFIG environment variable
//  3. ~/.cronix/cronix.yaml
//  4. /etc/cronix/cronix.yaml
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the operator-side configuration document.
type Config struct {
	// LogLevel is one of "debug", "info", "warn", "error". Default "info".
	LogLevel string `yaml:"log_level"`
	// ManifestSources lists each app whose manifest cronix should reconcile.
	ManifestSources []ManifestSource `yaml:"manifest_sources"`
	// Locks configures the lock backends.
	Locks LocksConfig `yaml:"locks"`
	// Defaults are the per-job defaults applied when the manifest does not
	// specify them; they MUST stay in sync with the manifest spec defaults
	// (Phase 1) so that operator-side and app-side defaults agree.
	Defaults DefaultsConfig `yaml:"defaults"`
}

// ManifestSource is one app whose manifest cronix reconciles.
type ManifestSource struct {
	// App is the expected `manifest.app` value. Mismatched manifests are rejected.
	App string `yaml:"app"`
	// URL is an `https://...` (or `file://...` per D-025) location.
	URL string `yaml:"url"`
	// SecretRefs name the secrets to use for both manifest fetching and
	// trigger signing. Resolution syntax (resolved by config_secrets.go):
	//   env:NAME       — process environment variable
	//   file:/path/x   — file contents (whitespace trimmed)
	//   raw:literal    — inline literal (development only — emits a warning)
	SecretRefs []string `yaml:"secret_refs"`
}

// LocksConfig groups lock backend selection and connection info.
type LocksConfig struct {
	// Default is the lock backend used when a job's concurrency_scope is
	// "host" — almost always "flock". Operators MAY override to "redis"
	// to make all locks distributed.
	Default string `yaml:"default"`
	// Flock holds flock-specific settings.
	Flock FlockConfig `yaml:"flock"`
	// Redis holds Redis-specific settings; required when any job uses
	// concurrency_scope: global.
	Redis *RedisConfig `yaml:"redis,omitempty"`
}

// FlockConfig sets the directory for flock files.
type FlockConfig struct {
	// Dir defaults to /var/lock/cronix. Created at startup if missing.
	Dir string `yaml:"dir"`
}

// RedisConfig describes a Redis endpoint for the global-scope lock backend.
type RedisConfig struct {
	Addr      string `yaml:"addr"`
	DB        int    `yaml:"db"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	KeyPrefix string `yaml:"key_prefix"`
}

// DefaultsConfig matches manifest defaults so manifest validation and
// operator defaults agree. See manifest.NormalizedPolicy.
type DefaultsConfig struct {
	TimeoutSeconds int            `yaml:"timeout_seconds"`
	Retries        RetriesDefault `yaml:"retries"`
}

type RetriesDefault struct {
	MaxAttempts int `yaml:"max_attempts"`
	MinSeconds  int `yaml:"min_seconds"`
	MaxSeconds  int `yaml:"max_seconds"`
}

// Defaults returns a Config pre-populated with documented defaults. Use it
// as a base and overlay with the YAML.
func Defaults() Config {
	return Config{
		LogLevel: "info",
		Locks: LocksConfig{
			Default: "flock",
			Flock:   FlockConfig{Dir: "/var/lock/cronix"},
		},
		Defaults: DefaultsConfig{
			TimeoutSeconds: 60,
			Retries:        RetriesDefault{MaxAttempts: 3, MinSeconds: 1, MaxSeconds: 60},
		},
	}
}

// Load reads, parses, and validates a config file. The empty path yields
// the documented defaults.
func Load(path string) (*Config, error) {
	cfg := Defaults()
	if path == "" {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		return &cfg, nil
	}
	raw, err := os.ReadFile(path) //#nosec G304 — operator config path is intentional input
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &cfg, nil
}

// Validate performs structural and cross-field checks.
func (c *Config) Validate() error {
	var issues []string
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		issues = append(issues, fmt.Sprintf("log_level must be debug|info|warn|error, got %q", c.LogLevel))
	}

	for i, src := range c.ManifestSources {
		if src.App == "" {
			issues = append(issues, fmt.Sprintf("manifest_sources[%d].app is required", i))
		}
		if src.URL == "" {
			issues = append(issues, fmt.Sprintf("manifest_sources[%d].url is required", i))
		} else if !strings.HasPrefix(src.URL, "https://") &&
			!strings.HasPrefix(src.URL, "file://") &&
			!strings.HasPrefix(src.URL, "http://localhost") &&
			!strings.HasPrefix(src.URL, "http://127.0.0.1") {
			issues = append(issues, fmt.Sprintf("manifest_sources[%d].url must be https://, file://, or http://localhost (got %q)", i, src.URL))
		}
		if len(src.SecretRefs) == 0 {
			issues = append(issues, fmt.Sprintf("manifest_sources[%d].secret_refs must contain at least one ref", i))
		}
		for j, ref := range src.SecretRefs {
			if !secretRefRe.MatchString(ref) {
				issues = append(issues, fmt.Sprintf("manifest_sources[%d].secret_refs[%d] must start with env: file: or raw: (got %q)", i, j, ref))
			}
		}
	}

	switch c.Locks.Default {
	case "":
		c.Locks.Default = "flock"
	case "flock", "redis":
	default:
		issues = append(issues, fmt.Sprintf("locks.default must be flock|redis, got %q", c.Locks.Default))
	}
	if c.Locks.Flock.Dir == "" {
		c.Locks.Flock.Dir = "/var/lock/cronix"
	}
	if c.Locks.Redis != nil && c.Locks.Redis.Addr == "" {
		issues = append(issues, "locks.redis.addr is required when locks.redis is set")
	}

	if c.Defaults.TimeoutSeconds == 0 {
		c.Defaults.TimeoutSeconds = 60
	}
	if c.Defaults.TimeoutSeconds < 1 || c.Defaults.TimeoutSeconds > 600 {
		issues = append(issues, fmt.Sprintf("defaults.timeout_seconds must be 1..600, got %d", c.Defaults.TimeoutSeconds))
	}
	if c.Defaults.Retries.MaxAttempts == 0 {
		c.Defaults.Retries.MaxAttempts = 3
	}
	if c.Defaults.Retries.MaxAttempts < 1 || c.Defaults.Retries.MaxAttempts > 10 {
		issues = append(issues, fmt.Sprintf("defaults.retries.max_attempts must be 1..10, got %d", c.Defaults.Retries.MaxAttempts))
	}
	if c.Defaults.Retries.MinSeconds == 0 {
		c.Defaults.Retries.MinSeconds = 1
	}
	if c.Defaults.Retries.MaxSeconds == 0 {
		c.Defaults.Retries.MaxSeconds = 60
	}
	if c.Defaults.Retries.MinSeconds > c.Defaults.Retries.MaxSeconds {
		issues = append(issues, "defaults.retries.min_seconds must be <= max_seconds")
	}

	if len(issues) == 0 {
		return nil
	}
	return errors.New("config invalid: " + strings.Join(issues, "; "))
}

var secretRefRe = regexp.MustCompile(`^(env|file|raw):.+$`)
