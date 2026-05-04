// Package kubernetes is the Kubernetes CronJob backend adapter.
//
// Per-schedule artifacts (one pair per (job, scheduleIndex)):
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
//	---
//	apiVersion: v1
//	kind: ConfigMap
//	metadata:
//	  name: cronix-<app>-<job>-<idx>-spec
//	  labels: { same as above }
//	data:
//	  <app>.<job>.json: |
//	    { ...trigger spec... }
//
// Ownership lives on the labels — the backend never touches resources
// missing `cronix.dev/managed=true`.
//
// Concurrency: the K8s API server is the lock. Multiple `cronix apply`
// runs against the same cluster are safe; conflicting writes surface
// as `ResourceVersion` conflicts that callers can retry.
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/trigger"
)

// Label keys placed on every cronix-owned CronJob and ConfigMap.
const (
	LabelManaged = "cronix.dev/managed"
	LabelApp     = "cronix.dev/app"
	LabelJob     = "cronix.dev/job"
	LabelHash    = "cronix.dev/hash"
	LabelIndex   = "cronix.dev/index"
)

// specMountDir is where the spec ConfigMap is mounted into the pod
// running `cronix trigger`. Must agree with the shim's default.
const specMountDir = "/etc/cronix/jobs"

// Backend is the Kubernetes CronJob backend.
type Backend struct {
	client     kubernetes.Interface
	image      string
	namespace  string
	secretRefs []string
}

// Options for constructing a Backend.
type Options struct {
	// Image is the cronix container image (e.g. awbx/cronix:v0.2.0).
	Image string
	// Namespace defaults to "default".
	Namespace string
	// SecretRefs are baked into the per-job ConfigMap so the trigger
	// shim inside the pod can resolve secrets at fire time.
	SecretRefs []string

	// One of Client / Kubeconfig / InCluster wires the API client.
	// Client takes precedence (used by tests with the fake clientset).
	// InCluster takes precedence over Kubeconfig.
	// If none are set, the default kubeconfig loading rules apply
	// (KUBECONFIG env, then ~/.kube/config, then in-cluster).
	Client     kubernetes.Interface
	Kubeconfig string
	InCluster  bool
}

// New constructs a Backend.
func New(opts Options) (*Backend, error) {
	if opts.Image == "" {
		return nil, fmt.Errorf("kubernetes: Image is required")
	}
	ns := opts.Namespace
	if ns == "" {
		ns = "default"
	}
	client := opts.Client
	if client == nil {
		cfg, err := buildConfig(opts)
		if err != nil {
			return nil, err
		}
		c, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: client: %w", err)
		}
		client = c
	}
	return &Backend{
		client:     client,
		image:      opts.Image,
		namespace:  ns,
		secretRefs: append([]string(nil), opts.SecretRefs...),
	}, nil
}

func buildConfig(opts Options) (*rest.Config, error) {
	if opts.InCluster {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("kubernetes: in-cluster config: %w", err)
		}
		return cfg, nil
	}
	if opts.Kubeconfig != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", opts.Kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: kubeconfig %s: %w", opts.Kubeconfig, err)
		}
		return cfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	rc, err := cfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubernetes: load kubeconfig: %w", err)
	}
	return rc, nil
}

// Name returns "kubernetes".
func (*Backend) Name() string { return "kubernetes" }

// List enumerates owned CronJobs. ConfigMaps are inferred from CronJob
// names (`<cronjob-name>-spec`) so a single label query suffices.
func (b *Backend) List(ctx context.Context) ([]backends.ManagedEntry, error) {
	sel := LabelManaged + "=true"
	list, err := b.client.BatchV1().CronJobs(b.namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, fmt.Errorf("kubernetes: list cronjobs: %w", err)
	}
	out := make([]backends.ManagedEntry, 0, len(list.Items))
	for i := range list.Items {
		cj := &list.Items[i]
		idx, _ := strconv.Atoi(cj.Labels[LabelIndex])
		out = append(out, backends.ManagedEntry{
			App:   cj.Labels[LabelApp],
			Job:   cj.Labels[LabelJob],
			Hash:  cj.Labels[LabelHash],
			Index: idx,
			Raw:   cj,
		})
	}
	return out, nil
}

// Create installs CronJob + ConfigMap pairs for every schedule of `job`.
func (b *Backend) Create(ctx context.Context, app string, job manifest.NormalizedJob) error {
	if err := validateName(app); err != nil {
		return fmt.Errorf("kubernetes: app id: %w", err)
	}
	if err := validateName(job.Name); err != nil {
		return fmt.Errorf("kubernetes: job name: %w", err)
	}
	for i, sched := range job.Schedules {
		cj, cm, err := b.buildResources(app, job, i, sched)
		if err != nil {
			return err
		}
		if _, err := b.client.CoreV1().ConfigMaps(b.namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("kubernetes: create configmap %s: %w", cm.Name, err)
		}
		if _, err := b.client.BatchV1().CronJobs(b.namespace).Create(ctx, cj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("kubernetes: create cronjob %s: %w", cj.Name, err)
		}
	}
	return nil
}

// Update replaces all owned (CronJob, ConfigMap) pairs for (app, job.Name)
// with the freshly rendered form. Implemented as Delete + Create — a brief
// gap is acceptable in CI deploy contexts and the API server prevents
// concurrent observers from seeing an inconsistent partial state per
// resource.
func (b *Backend) Update(ctx context.Context, app string, job manifest.NormalizedJob) error {
	if err := b.Delete(ctx, app, job.Name); err != nil {
		return err
	}
	return b.Create(ctx, app, job)
}

// Delete removes all owned resources for (app, jobName). Implemented as
// List + per-item Delete so the operation behaves identically against
// the live API server and the testing fake clientset (whose
// DeleteCollection does not consistently honor label selectors).
func (b *Backend) Delete(ctx context.Context, app, jobName string) error {
	if err := validateName(app); err != nil {
		return fmt.Errorf("kubernetes: app id: %w", err)
	}
	if err := validateName(jobName); err != nil {
		return fmt.Errorf("kubernetes: job name: %w", err)
	}
	sel := fmt.Sprintf("%s=true,%s=%s,%s=%s", LabelManaged, LabelApp, app, LabelJob, jobName)
	listOpts := metav1.ListOptions{LabelSelector: sel}
	fg := metav1.DeletePropagationForeground
	delOpts := metav1.DeleteOptions{PropagationPolicy: &fg}

	cjs, err := b.client.BatchV1().CronJobs(b.namespace).List(ctx, listOpts)
	if err != nil {
		return fmt.Errorf("kubernetes: list cronjobs: %w", err)
	}
	for i := range cjs.Items {
		name := cjs.Items[i].Name
		if err := b.client.BatchV1().CronJobs(b.namespace).Delete(ctx, name, delOpts); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("kubernetes: delete cronjob %s: %w", name, err)
		}
	}
	cms, err := b.client.CoreV1().ConfigMaps(b.namespace).List(ctx, listOpts)
	if err != nil {
		return fmt.Errorf("kubernetes: list configmaps: %w", err)
	}
	for i := range cms.Items {
		name := cms.Items[i].Name
		if err := b.client.CoreV1().ConfigMaps(b.namespace).Delete(ctx, name, delOpts); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("kubernetes: delete configmap %s: %w", name, err)
		}
	}
	return nil
}

// Validate checks that the job fits within K8s naming and schedule constraints.
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

// History reads K8s Events + Pod logs. Phase 6 — returns nil for now.
func (*Backend) History(_ context.Context, _ backends.HistoryOpts) ([]backends.HistoryEntry, error) {
	return nil, nil
}

// Ensure verifies the API server is reachable.
func (b *Backend) Ensure(ctx context.Context) error {
	_ = ctx
	if _, err := b.client.Discovery().ServerVersion(); err != nil {
		return fmt.Errorf("kubernetes: api server: %w", err)
	}
	return nil
}

// buildResources assembles the CronJob + ConfigMap for one schedule index.
func (b *Backend) buildResources(app string, job manifest.NormalizedJob, idx int, schedule string) (*batchv1.CronJob, *corev1.ConfigMap, error) {
	sched, err := translateK8sSchedule(schedule)
	if err != nil {
		return nil, nil, err
	}
	spec := &trigger.SpecFile{
		App:           app,
		Job:           job,
		SecretRefs:    append([]string(nil), b.secretRefs...),
		ScheduleIndex: idx,
	}
	specJSON, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("kubernetes: marshal spec: %w", err)
	}
	hash := hashJobSchedule(job, idx)
	name := fmt.Sprintf("cronix-%s-%s-%d", app, job.Name, idx)
	specName := name + "-spec"
	labels := map[string]string{
		LabelManaged: "true",
		LabelApp:     app,
		LabelJob:     job.Name,
		LabelHash:    hash,
		LabelIndex:   strconv.Itoa(idx),
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      specName,
			Namespace: b.namespace,
			Labels:    labels,
		},
		Data: map[string]string{
			fmt.Sprintf("%s.%s.json", app, job.Name): string(specJSON),
		},
	}
	tz := job.Timezone
	if tz == "" {
		tz = "UTC"
	}
	historyLimit := int32(3)
	backoff := int32(0)
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.namespace,
			Labels:    labels,
		},
		Spec: batchv1.CronJobSpec{
			Schedule:                   sched,
			TimeZone:                   &tz,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			SuccessfulJobsHistoryLimit: &historyLimit,
			FailedJobsHistoryLimit:     &historyLimit,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: batchv1.JobSpec{
					BackoffLimit: &backoff,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{{
								Name:  "cronix",
								Image: b.image,
								Args:  []string{"trigger", fmt.Sprintf("%s.%s", app, job.Name)},
								Env: []corev1.EnvVar{
									{Name: "CRONIX_JOB_SPEC_DIR", Value: specMountDir},
									{Name: "CRONIX_RUN_FROM_KUBERNETES", Value: "true"},
								},
								VolumeMounts: []corev1.VolumeMount{{
									Name: "job-spec", MountPath: specMountDir, ReadOnly: true,
								}},
							}},
							Volumes: []corev1.Volume{{
								Name: "job-spec",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: specName},
									},
								},
							}},
						},
					},
				},
			},
		},
	}
	return cj, cm, nil
}

// RenderManifest returns the YAML for the CronJob and the supporting
// ConfigMap that mounts the trigger spec into the pod. Kept for users
// who want to render YAML standalone (e.g. for `kubectl apply -f`)
// without instantiating a Backend.
func RenderManifest(image, namespace, app string, job manifest.NormalizedJob, idx int, hash, specJSON string) (string, error) {
	if idx < 0 || idx >= len(job.Schedules) {
		return "", fmt.Errorf("kubernetes: schedule index %d out of range", idx)
	}
	cron, err := translateK8sSchedule(job.Schedules[idx])
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("cronix-%s-%s-%d", app, job.Name, idx)
	specName := name + "-spec"
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

// translateK8sSchedule maps cron @-shortcuts to 5-field cron, since K8s
// doesn't support `@hourly` etc. natively.
func translateK8sSchedule(s string) (string, error) {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "@") {
		if len(strings.Fields(t)) != 5 {
			return "", fmt.Errorf("kubernetes: schedule %q is not 5-field cron", s)
		}
		return t, nil
	}
	switch t {
	case "@hourly":
		return "0 * * * *", nil
	case "@daily", "@midnight":
		return "0 0 * * *", nil
	case "@weekly":
		return "0 0 * * 0", nil
	case "@monthly":
		return "0 0 1 * *", nil
	case "@yearly", "@annually":
		return "0 0 1 1 *", nil
	default:
		return "", fmt.Errorf("kubernetes: schedule %q not supported", s)
	}
}

// validK8sCron is a permissive check used by Validate. The API server
// itself enforces full validity at Create time.
func validK8sCron(s string) bool {
	_, err := translateK8sSchedule(s)
	return err == nil
}

// validateName mirrors K8s label-value constraints (DNS-1123 subset).
func validateName(s string) error {
	if s == "" {
		return fmt.Errorf("name is empty")
	}
	if len(s) > 63 {
		return fmt.Errorf("name %q is too long (max 63)", s)
	}
	for i, r := range s {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		isHyphen := r == '-'
		if !(isLower || isDigit || isHyphen) {
			return fmt.Errorf("name %q has invalid char at %d", s, i)
		}
	}
	return nil
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// hashJobSchedule produces the same 16-char change-detection hash the
// reconciler uses (FNV-1a over the canonicalized manifest, salted by
// schedule index). Match expected by reconcile.computeDesiredHashes.
func hashJobSchedule(job manifest.NormalizedJob, idx int) string {
	b, _ := manifest.Canonicalize(&manifest.NormalizedManifest{
		Version: 1,
		App:     "_hash_",
		Jobs:    []manifest.NormalizedJob{job},
	})
	const (
		offset64 = uint64(1469598103934665603)
		prime64  = uint64(1099511628211)
	)
	h := offset64
	for _, x := range b {
		h ^= uint64(x)
		h *= prime64
	}
	h ^= uint64(idx)
	h *= prime64
	return fmt.Sprintf("%016x", h)
}

var _ backends.Backend = (*Backend)(nil)
