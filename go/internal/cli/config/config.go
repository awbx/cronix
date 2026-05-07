// Package config loads the operator-owned cronix CLI config (the list
// of backends `cronix global-status` should query).
//
// This file is configuration, not state — cronix never writes it. The
// backends themselves remain the source of truth for what is installed.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load reads and validates the YAML config at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path) //#nosec G304 — operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// DefaultPaths returns candidate config locations in lookup order.
// First-existing wins.
func DefaultPaths() []string {
	out := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".cronix", "config.yaml"))
	}
	out = append(out, "/etc/cronix/config.yaml")
	return out
}

// ResolvedPath returns the first DefaultPaths entry that exists, or "" if none.
func ResolvedPath() string {
	for _, p := range DefaultPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Config is the parsed YAML.
type Config struct {
	Version  int            `yaml:"version"`
	Backends []BackendEntry `yaml:"backends"`
}

// BackendEntry is one (operator-labeled) backend the CLI knows about.
// Field names mirror the existing --flag names so docs and mental model
// stay aligned.
type BackendEntry struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`

	// crontab
	CrontabPath string `yaml:"crontab_path,omitempty"`
	TriggerBin  string `yaml:"trigger_bin,omitempty"`

	// systemd-timer
	UnitDir string `yaml:"unit_dir,omitempty"`

	// kubernetes
	Namespace  string `yaml:"namespace,omitempty"`
	Kubeconfig string `yaml:"kubeconfig,omitempty"`
	InCluster  bool   `yaml:"in_cluster,omitempty"`
	Image      string `yaml:"image,omitempty"`

	// aws-scheduler
	Region        string `yaml:"region,omitempty"`
	ScheduleGroup string `yaml:"schedule_group,omitempty"`
	TargetArn     string `yaml:"target_arn,omitempty"`
	RoleArn       string `yaml:"role_arn,omitempty"`

	// vercel
	VercelJsonPath    string `yaml:"vercel_json_path,omitempty"`
	VercelTriggerPath string `yaml:"vercel_trigger_prefix,omitempty"`
}

// SupportedTypes is the closed set of backend types config recognizes.
var SupportedTypes = []string{"crontab", "systemd-timer", "kubernetes", "aws-scheduler", "vercel"}

// Validate checks the config is internally consistent.
func (c *Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("config: version must be 1, got %d", c.Version)
	}
	seen := make(map[string]struct{}, len(c.Backends))
	for i, e := range c.Backends {
		if e.Name == "" {
			return fmt.Errorf("config: backends[%d]: name is required", i)
		}
		if _, dup := seen[e.Name]; dup {
			return fmt.Errorf("config: duplicate backend name %q", e.Name)
		}
		seen[e.Name] = struct{}{}
		if err := e.Validate(); err != nil {
			return fmt.Errorf("config: backends[%d] (%s): %w", i, e.Name, err)
		}
	}
	return nil
}

// Validate checks one entry's required fields per its type.
func (e BackendEntry) Validate() error {
	switch e.Type {
	case "crontab":
		if e.CrontabPath == "" {
			return fmt.Errorf("crontab_path is required for type=crontab")
		}
	case "systemd-timer":
		if e.UnitDir == "" {
			return fmt.Errorf("unit_dir is required for type=systemd-timer")
		}
	case "kubernetes":
		// kubeconfig + in_cluster are both optional — defaulted in the
		// kubernetes backend constructor.
	case "aws-scheduler":
		// region defaults to SDK chain; target/role only matter for
		// apply, not for List() which global-status uses.
	case "":
		return fmt.Errorf("type is required")
	default:
		return fmt.Errorf("unknown type %q (supported: %v)", e.Type, SupportedTypes)
	}
	return nil
}
