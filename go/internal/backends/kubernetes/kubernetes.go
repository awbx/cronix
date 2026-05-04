// Package kubernetes is the Kubernetes CronJob backend adapter.
//
// Per-schedule artifacts:
//
//	apiVersion: batch/v1
//	kind: CronJob
//	metadata:
//	  name: cronix-<app>-<job>-<idx>
//	  labels:
//	    cronix.dev/managed: "true"
//	    cronix.dev/app: <app>
//	    cronix.dev/job: <job>
//	    cronix.dev/hash: <hash>
//	    cronix.dev/index: "<idx>"
//	spec:
//	  schedule: <cron>
//	  ...
//
// v1 status: this package ships YAML rendering and Validate. The
// client-go integration (List/Create/Update/Delete via the K8s API) is
// wired in a follow-up phase — see PLAN.md §5d. Operators using
// Kubernetes today can render YAML via this package and apply it with
// `kubectl apply -f`.
package kubernetes

import (
	"context"
	"fmt"
	"strings"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
)

// Backend is a placeholder for the full kubernetes implementation.
type Backend struct {
	image     string
	namespace string
}

// Options for the (incomplete v1) kubernetes backend.
type Options struct {
	// Image is the cronix container image (e.g. ghcr.io/awbx/cronix:latest).
	Image string
	// Namespace defaults to "default".
	Namespace string
}

// New constructs a Backend.
func New(opts Options) (*Backend, error) {
	if opts.Image == "" {
		return nil, fmt.Errorf("kubernetes: Image is required")
	}
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	return &Backend{image: opts.Image, namespace: opts.Namespace}, nil
}

// Name returns "kubernetes".
func (*Backend) Name() string { return "kubernetes" }

// Validate checks that the job fits within K8s resource-name length
// (`cronix-<app>-<job>-<idx>` ≤ 52 chars to leave room for hash+idx,
// since the K8s name max is 253 but the label value max is 63 — and
// our naming is the more restrictive constraint).
func (*Backend) Validate(job manifest.NormalizedJob) backends.ValidationResult {
	var issues []string
	for i, s := range job.Schedules {
		if !validK8sCron(s) {
			issues = append(issues, fmt.Sprintf("schedules[%d]: %q is not a valid K8s CronJob schedule", i, s))
		}
	}
	if len(job.Name) > 50 {
		issues = append(issues, fmt.Sprintf("job name %q is too long for K8s naming convention (max 50)", job.Name))
	}
	return backends.ValidationResult{OK: len(issues) == 0, Issues: issues}
}

// RenderManifest returns the YAML for the CronJob and the supporting
// ConfigMap that mounts the trigger spec into the pod.
func RenderManifest(image, namespace, app string, job manifest.NormalizedJob, idx int, hash, specJSON string) (string, error) {
	if idx < 0 || idx >= len(job.Schedules) {
		return "", fmt.Errorf("kubernetes: schedule index %d out of range", idx)
	}
	cron := job.Schedules[idx]
	// K8s does not support `@every`. Translate the way crontab does.
	if strings.HasPrefix(cron, "@") {
		switch cron {
		case "@hourly":
			cron = "0 * * * *"
		case "@daily", "@midnight":
			cron = "0 0 * * *"
		case "@weekly":
			cron = "0 0 * * 0"
		case "@monthly":
			cron = "0 0 1 * *"
		case "@yearly", "@annually":
			cron = "0 0 1 1 *"
		default:
			return "", fmt.Errorf("kubernetes: schedule %q not supported", cron)
		}
	}
	name := fmt.Sprintf("cronix-%s-%s-%d", app, job.Name, idx)
	specName := fmt.Sprintf("%s-spec", name)
	tz := job.Timezone
	if tz == "" {
		tz = "UTC"
	}
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %[7]s
  namespace: %[2]s
  labels:
    cronix.dev/managed: "true"
    cronix.dev/app: %[3]s
    cronix.dev/job: %[4]s
    cronix.dev/index: "%[5]d"
data:
  %[3]s.%[4]s.json: |
%[8]s
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: %[6]s
  namespace: %[2]s
  labels:
    cronix.dev/managed: "true"
    cronix.dev/app: %[3]s
    cronix.dev/job: %[4]s
    cronix.dev/hash: %[10]s
    cronix.dev/index: "%[5]d"
spec:
  schedule: "%[9]s"
  timeZone: %[11]s
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 3
  jobTemplate:
    spec:
      backoffLimit: 0
      template:
        spec:
          restartPolicy: Never
          containers:
          - name: cronix
            image: %[1]s
            args: ["trigger", "%[3]s.%[4]s"]
            env:
            - name: CRONIX_JOB_SPEC_DIR
              value: /etc/cronix/jobs
            - name: CRONIX_RUN_FROM_KUBERNETES
              value: "true"
            volumeMounts:
            - name: job-spec
              mountPath: /etc/cronix/jobs
              readOnly: true
          volumes:
          - name: job-spec
            configMap:
              name: %[7]s
`,
		image, namespace, app, job.Name, idx, name, specName,
		indent(specJSON, 4), cron, hash, tz,
	), nil
}

// validK8sCron is a permissive check — K8s itself enforces full validity.
func validK8sCron(s string) bool {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "@") {
		switch t {
		case "@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@yearly", "@annually":
			return true
		default:
			return false
		}
	}
	return len(strings.Fields(t)) == 5
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// The List/Create/Update/Delete/History/Ensure methods are placeholders
// for the client-go integration phase. They satisfy the Backend interface.
func (*Backend) List(_ context.Context) ([]backends.ManagedEntry, error) {
	return nil, fmt.Errorf("kubernetes: List not implemented in this phase — see PLAN.md §5d")
}

func (*Backend) Create(_ context.Context, _ string, _ manifest.NormalizedJob) error {
	return fmt.Errorf("kubernetes: Create not implemented in this phase — see PLAN.md §5d")
}

func (*Backend) Update(_ context.Context, _ string, _ manifest.NormalizedJob) error {
	return fmt.Errorf("kubernetes: Update not implemented in this phase — see PLAN.md §5d")
}

func (*Backend) Delete(_ context.Context, _, _ string) error {
	return fmt.Errorf("kubernetes: Delete not implemented in this phase — see PLAN.md §5d")
}

func (*Backend) History(_ context.Context, _ backends.HistoryOpts) ([]backends.HistoryEntry, error) {
	return nil, nil
}

func (*Backend) Ensure(_ context.Context) error {
	return fmt.Errorf("kubernetes: Ensure not implemented in this phase — see PLAN.md §5d")
}

var _ backends.Backend = (*Backend)(nil)
