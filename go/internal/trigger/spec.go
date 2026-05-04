// Package trigger implements the per-fire executor invoked by the host
// scheduler. The host scheduler entry only invokes `cronix trigger
// <app>.<name>`; everything that happens at and after the fire — secret
// resolution, lock acquisition, HMAC signing, HTTP request, timeout,
// retry, structured logging — is the shim's job (D-028, synthesis-first).
package trigger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/awbx/cronix/go/internal/manifest"
)

// SpecFile is the on-disk job spec the reconciler writes for the shim
// to read at fire time (D-024). One file per (app, job) tuple at
// /etc/cronix/jobs/<app>.<name>.json (or under $CRONIX_JOB_SPEC_DIR
// when running in K8s).
//
// The Job is the post-defaults NormalizedJob — no further processing
// required at fire time. The App is the manifest's top-level app id.
// SecretRefs are denormalized from the manifest+operator-config so the
// shim never touches the manifest source itself.
type SpecFile struct {
	App        string                 `json:"app"`
	Job        manifest.NormalizedJob `json:"job"`
	SecretRefs []string               `json:"secret_refs"`
	// ScheduleIndex distinguishes multi-schedule jobs at fire time.
	ScheduleIndex int `json:"schedule_index"`
}

// LoadSpec reads the spec file for `<app>.<name>` from the configured
// directory. Resolution order: explicit `dir` arg, $CRONIX_JOB_SPEC_DIR,
// /etc/cronix/jobs.
func LoadSpec(dir, app, jobName string) (*SpecFile, error) {
	if dir == "" {
		dir = os.Getenv("CRONIX_JOB_SPEC_DIR")
	}
	if dir == "" {
		dir = "/etc/cronix/jobs"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s.%s.json", app, jobName))
	raw, err := os.ReadFile(path) //#nosec G304 — operator-managed path
	if err != nil {
		return nil, fmt.Errorf("trigger: load spec %s: %w", path, err)
	}
	var spec SpecFile
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("trigger: parse spec %s: %w", path, err)
	}
	if spec.App != app {
		return nil, fmt.Errorf("trigger: spec %s: app=%q does not match expected %q", path, spec.App, app)
	}
	if spec.Job.Name != jobName {
		return nil, fmt.Errorf("trigger: spec %s: job=%q does not match expected %q", path, spec.Job.Name, jobName)
	}
	return &spec, nil
}

// Save writes a SpecFile to dir as <app>.<job>.json with mode 0640.
// Used by the reconciler (Phase 5e).
func (s *SpecFile) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("trigger: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s.%s.json", s.App, s.Job.Name))
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("trigger: marshal: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		return fmt.Errorf("trigger: write %s: %w", path, err)
	}
	return nil
}
