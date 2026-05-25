//go:build integration

// Real-cluster integration tests for the kubernetes backend. Gated
// behind the `integration` build tag so `go test ./...` stays fast
// and offline; CI runs them via `.github/workflows/integration.yml`
// against a kind cluster.
//
// Run locally against an existing cluster:
//
//	export KUBECONFIG=$HOME/.kube/config
//	go test -tags integration -v ./internal/backends/kubernetes/...
//
// The unit tests in kubernetes_test.go cover behavior via
// k8s.io/client-go/kubernetes/fake. The fake client glosses over real
// API quirks the production code path can hit: admission controllers,
// defaulting, immutable fields, optimistic concurrency 409s. These
// integration tests catch regressions the fake client misses.

package kubernetes

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/awbx/cronix/go/internal/policy"
)

// newIntegrationBackend builds a real-cluster Backend bound to a
// fresh per-test namespace. t.Cleanup deletes the namespace
// (cascade-deletes every resource cronix created).
func newIntegrationBackend(t *testing.T) (*Backend, kubernetes.Interface, string) {
	t.Helper()

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG not set; integration tests require a real cluster")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("build kubeconfig: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}

	// Random namespace per test for isolation.
	nsName := fmt.Sprintf("cronix-it-%d", time.Now().UnixNano())
	ctx := context.Background()
	if _, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: nsName},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace %q: %v", nsName, err)
	}
	t.Cleanup(func() {
		// Background delete; the test cluster is throwaway anyway.
		_ = cs.CoreV1().Namespaces().Delete(context.Background(), nsName, metav1.DeleteOptions{})
	})

	b, err := New(Options{
		Image:     "awbx/cronix:test",
		Namespace: nsName,
		Client:    cs,
	})
	if err != nil {
		t.Fatalf("New backend: %v", err)
	}
	return b, cs, nsName
}

func TestIntegration_CreateListUpdateDelete(t *testing.T) {
	b, cs, ns := newIntegrationBackend(t)
	ctx := context.Background()

	job := sampleJob("reconcile", "@hourly")

	// Create
	if err := b.Create(ctx, "billing", job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cjs, err := cs.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list CronJobs: %v", err)
	}
	if len(cjs.Items) != 1 {
		t.Fatalf("expected 1 CronJob, got %d", len(cjs.Items))
	}
	cj := cjs.Items[0]
	if cj.Labels["cronix.dev/managed"] != "true" {
		t.Errorf("CronJob missing managed label: %v", cj.Labels)
	}
	if cj.Labels["cronix.dev/app"] != "billing" {
		t.Errorf("CronJob app label = %q, want %q", cj.Labels["cronix.dev/app"], "billing")
	}

	// Companion ConfigMap exists with the canonical spec.
	cms, err := cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "cronix.dev/managed=true",
	})
	if err != nil {
		t.Fatalf("list ConfigMaps: %v", err)
	}
	if len(cms.Items) != 1 {
		t.Fatalf("expected 1 ConfigMap, got %d", len(cms.Items))
	}

	// List returns the entry with a correct hash.
	entries, err := b.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	wantHash := policy.Hash(job, 0)
	if entries[0].Hash != wantHash {
		t.Errorf("entry hash = %q, want %q", entries[0].Hash, wantHash)
	}

	// Update with a new schedule produces a different hash + replaces the CronJob.
	updated := sampleJob("reconcile", "@daily")
	if err := b.Update(ctx, "billing", updated); err != nil {
		t.Fatalf("Update: %v", err)
	}
	entries, err = b.List(ctx)
	if err != nil {
		t.Fatalf("List after Update: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List after Update returned %d entries, want 1", len(entries))
	}
	if entries[0].Hash == wantHash {
		t.Errorf("hash unchanged after Update — Update was a no-op")
	}

	// Delete clears everything cronix created in the namespace.
	if err := b.Delete(ctx, "billing", "reconcile"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	cjs, _ = cs.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{})
	if len(cjs.Items) != 0 {
		t.Errorf("expected 0 CronJobs after Delete, got %d", len(cjs.Items))
	}
	cms, _ = cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "cronix.dev/managed=true",
	})
	if len(cms.Items) != 0 {
		t.Errorf("expected 0 managed ConfigMaps after Delete, got %d", len(cms.Items))
	}
}

func TestIntegration_DriftDetectedOnHandEdit(t *testing.T) {
	b, cs, ns := newIntegrationBackend(t)
	ctx := context.Background()
	job := sampleJob("reconcile", "@hourly")

	if err := b.Create(ctx, "billing", job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	entriesBefore, _ := b.List(ctx)

	// Out-of-band edit: change the CronJob's schedule field, the kind
	// of mutation an operator might do with `kubectl edit`. The hash
	// inside cronix.dev/hash label stays the same — that's exactly
	// the drift signal cronix surfaces.
	cjs, _ := cs.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{})
	if len(cjs.Items) != 1 {
		t.Fatalf("setup: expected 1 CronJob, got %d", len(cjs.Items))
	}
	cj := &cjs.Items[0]
	cj.Spec.Schedule = "*/5 * * * *" // hand-edit
	if _, err := cs.BatchV1().CronJobs(ns).Update(ctx, cj, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("manual update: %v", err)
	}

	// The drift surface here is: List still sees the entry with the
	// original hash, while a freshly computed hash from the rendered
	// CronJob's schedule would differ. drift command compares
	// these — we just verify List still surfaces the original
	// (proving the marker is what cronix trusts, not the live spec).
	entriesAfter, _ := b.List(ctx)
	if len(entriesAfter) != 1 {
		t.Fatalf("List after manual edit: %d entries, want 1", len(entriesAfter))
	}
	if entriesAfter[0].Hash != entriesBefore[0].Hash {
		t.Errorf("hash changed unexpectedly after manual edit; the label hash should be invariant")
	}
}

func TestIntegration_OnlyOwnedEntriesAreListed(t *testing.T) {
	b, cs, ns := newIntegrationBackend(t)
	ctx := context.Background()

	// Plant an unmanaged CronJob in the same namespace — simulates an
	// operator-created entry that cronix MUST NOT touch.
	unmanaged := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hand-managed-cron",
			Namespace: ns,
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "*/15 * * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyOnFailure,
							Containers: []corev1.Container{
								{Name: "echo", Image: "busybox:1.36", Command: []string{"echo", "hi"}},
							},
						},
					},
				},
			},
		},
	}
	if _, err := cs.BatchV1().CronJobs(ns).Create(ctx, unmanaged, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create unmanaged CronJob: %v", err)
	}

	// Create one cronix-managed entry.
	if err := b.Create(ctx, "billing", sampleJob("reconcile", "@hourly")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// List MUST return only the cronix entry, not the unmanaged one.
	entries, err := b.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1 (must skip unmanaged CronJob)", len(entries))
	}
	if entries[0].App != "billing" || entries[0].Job != "reconcile" {
		t.Errorf("List returned wrong entry: %+v", entries[0])
	}

	// The unmanaged CronJob must still exist in the namespace.
	if _, err := cs.BatchV1().CronJobs(ns).Get(ctx, "hand-managed-cron", metav1.GetOptions{}); err != nil {
		t.Errorf("unmanaged CronJob was modified or deleted: %v", err)
	}
}
