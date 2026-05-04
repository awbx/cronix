package kubernetes

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
)

func sampleJob(name string, schedules ...string) manifest.NormalizedJob {
	return manifest.NormalizedJob{
		Name:      name,
		Schedules: schedules,
		Timezone:  "Europe/Paris",
		Request: manifest.NormalizedRequest{
			Method: "POST", URL: "https://example.com/" + name,
			Headers: map[string]string{}, Body: "",
		},
		Policy: manifest.NormalizedPolicy{
			Concurrency: "Forbid", ConcurrencyScope: "global", TimeoutSeconds: 60,
			Retries: manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 1, MaxSeconds: 60},
		},
		Auth: manifest.NormalizedAuth{SecretRefs: []string{"env:S"}},
	}
}

func newTestBackend(t *testing.T) (*Backend, *fake.Clientset) {
	t.Helper()
	client := fake.NewClientset()
	b, err := New(Options{
		Image:      "ghcr.io/awbx/cronix:test",
		Namespace:  "billing",
		SecretRefs: []string{"env:CRON_SECRET"},
		Client:     client,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return b, client
}

func TestRenderManifest(t *testing.T) {
	job := sampleJob("ping", "@hourly")
	yaml, err := RenderManifest("ghcr.io/awbx/cronix:v0.1.0", "billing", "billing", job, 0, "abc1234567890def", "{}")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"kind: CronJob",
		"name: cronix-billing-ping-0",
		"cronix.dev/managed: \"true\"",
		"cronix.dev/app: billing",
		"cronix.dev/job: ping",
		"cronix.dev/hash: abc1234567890def",
		`schedule: "0 * * * *"`,
		"timeZone: Europe/Paris",
		"concurrencyPolicy: Forbid",
		`args: ["trigger", "billing.ping"]`,
		"image: ghcr.io/awbx/cronix:v0.1.0",
		"kind: ConfigMap",
		"name: cronix-billing-ping-0-spec",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("missing %q in:\n%s", want, yaml)
		}
	}
}

func TestRenderRejectsUnknownShortcut(t *testing.T) {
	if _, err := RenderManifest("img", "ns", "app", sampleJob("j", "@every 5m"), 0, "h", "{}"); err == nil {
		t.Fatalf("expected error on @every shortcut for k8s")
	}
}

func TestValidateRejectsLongName(t *testing.T) {
	b, _ := newTestBackend(t)
	long := strings.Repeat("a", 60)
	res := b.Validate(sampleJob(long, "@hourly"))
	if res.OK {
		t.Errorf("expected validation failure for long name")
	}
}

func TestValidateAcceptsCommonSchedules(t *testing.T) {
	b, _ := newTestBackend(t)
	for _, s := range []string{"@hourly", "0 0 * * *", "*/15 * * * *"} {
		res := b.Validate(sampleJob("ok", s))
		if !res.OK {
			t.Errorf("schedule %q rejected: %v", s, res.Issues)
		}
	}
}

func TestCreateInstallsCronJobAndConfigMap(t *testing.T) {
	b, client := newTestBackend(t)
	job := sampleJob("reconcile", "@hourly", "*/15 * * * *")
	if err := b.Create(context.Background(), "billing", job); err != nil {
		t.Fatalf("create: %v", err)
	}
	cjs, err := client.BatchV1().CronJobs("billing").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list cronjobs: %v", err)
	}
	if len(cjs.Items) != 2 {
		t.Fatalf("expected 2 CronJobs (one per schedule), got %d", len(cjs.Items))
	}
	cms, err := client.CoreV1().ConfigMaps("billing").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list configmaps: %v", err)
	}
	if len(cms.Items) != 2 {
		t.Fatalf("expected 2 ConfigMaps, got %d", len(cms.Items))
	}
	for _, cj := range cjs.Items {
		for _, want := range []string{LabelManaged, LabelApp, LabelJob, LabelHash, LabelIndex} {
			if _, ok := cj.Labels[want]; !ok {
				t.Errorf("CronJob %s missing label %s", cj.Name, want)
			}
		}
		if cj.Labels[LabelApp] != "billing" || cj.Labels[LabelJob] != "reconcile" {
			t.Errorf("CronJob %s wrong app/job labels: %v", cj.Name, cj.Labels)
		}
		if cj.Spec.Schedule == "" {
			t.Errorf("CronJob %s missing schedule", cj.Name)
		}
		if cj.Labels[LabelIndex] == "0" && cj.Spec.Schedule != "0 * * * *" {
			t.Errorf("CronJob %s expected schedule '0 * * * *', got %q", cj.Name, cj.Spec.Schedule)
		}
	}
	for _, cm := range cms.Items {
		key := "billing.reconcile.json"
		if _, ok := cm.Data[key]; !ok {
			t.Errorf("ConfigMap %s missing data key %s", cm.Name, key)
		}
		if !strings.Contains(cm.Data[key], `"app": "billing"`) {
			t.Errorf("ConfigMap %s spec missing app field: %s", cm.Name, cm.Data[key])
		}
	}
}

func TestListReturnsOnlyOwnedEntries(t *testing.T) {
	b, client := newTestBackend(t)
	if err := b.Create(context.Background(), "billing", sampleJob("reconcile", "@hourly")); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Inject an unmanaged CronJob — List must skip it.
	unmanaged := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "user-managed-cronjob", Namespace: "billing"},
		Spec:       batchv1.CronJobSpec{Schedule: "0 0 * * *"},
	}
	if _, err := client.BatchV1().CronJobs("billing").Create(context.Background(), unmanaged, metav1.CreateOptions{}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	entries, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 owned entry, got %d", len(entries))
	}
	if entries[0].App != "billing" || entries[0].Job != "reconcile" {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
	if entries[0].Hash == "" {
		t.Errorf("hash label missing")
	}
}

func TestUpdateReplacesEntries(t *testing.T) {
	b, client := newTestBackend(t)
	first := sampleJob("reconcile", "@hourly")
	if err := b.Create(context.Background(), "billing", first); err != nil {
		t.Fatalf("create: %v", err)
	}
	originalEntries, _ := b.List(context.Background())
	originalHash := originalEntries[0].Hash

	updated := sampleJob("reconcile", "@daily")
	if err := b.Update(context.Background(), "billing", updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	entries, _ := b.List(context.Background())
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after update, got %d", len(entries))
	}
	if entries[0].Hash == originalHash {
		t.Errorf("expected hash to change after update")
	}
	cjs, _ := client.BatchV1().CronJobs("billing").List(context.Background(), metav1.ListOptions{})
	for _, cj := range cjs.Items {
		if !strings.HasPrefix(cj.Name, "cronix-") {
			continue
		}
		if cj.Spec.Schedule != "0 0 * * *" {
			t.Errorf("expected schedule '0 0 * * *' after @daily update, got %q", cj.Spec.Schedule)
		}
	}
}

func TestDeleteRemovesAllOwnedResources(t *testing.T) {
	b, client := newTestBackend(t)
	if err := b.Create(context.Background(), "billing", sampleJob("reconcile", "@hourly", "*/15 * * * *")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Delete(context.Background(), "billing", "reconcile"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// The fake clientset's DeleteCollection deletes immediately — no foreground GC simulation needed.
	cjs, _ := client.BatchV1().CronJobs("billing").List(context.Background(), metav1.ListOptions{LabelSelector: LabelManaged + "=true"})
	if len(cjs.Items) != 0 {
		t.Errorf("expected 0 owned CronJobs after delete, got %d", len(cjs.Items))
	}
	cms, _ := client.CoreV1().ConfigMaps("billing").List(context.Background(), metav1.ListOptions{LabelSelector: LabelManaged + "=true"})
	if len(cms.Items) != 0 {
		t.Errorf("expected 0 owned ConfigMaps after delete, got %d", len(cms.Items))
	}
}

func TestEnsureSucceedsAgainstFakeAPIServer(t *testing.T) {
	b, _ := newTestBackend(t)
	if err := b.Ensure(context.Background()); err != nil {
		t.Errorf("expected Ensure to succeed against fake API server, got %v", err)
	}
}

func TestEnsureFailsWhenAPIErrors(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("get", "*", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("synthetic")
	})
	b, _ := New(Options{Image: "x", Client: client})
	if err := b.Ensure(context.Background()); err == nil {
		t.Errorf("expected Ensure to fail when API server is unreachable")
	}
}

func TestHistoryRequiresAppAndJob(t *testing.T) {
	b, _ := newTestBackend(t)
	if _, err := b.History(context.Background(), backendsHistoryOpts("", "")); err == nil {
		t.Errorf("expected error when App+Job are empty")
	}
}

func TestHistoryListsPodsByLabel(t *testing.T) {
	b, client := newTestBackend(t)
	// Inject a pod with the right labels so List returns something.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "billing-reconcile-pod-1",
			Namespace: "billing",
			Labels: map[string]string{
				LabelManaged: "true",
				LabelApp:     "billing",
				LabelJob:     "reconcile",
				LabelIndex:   "0",
			},
		},
	}
	if _, err := client.CoreV1().Pods("billing").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("inject pod: %v", err)
	}
	// Fake clientset's GetLogs returns "fake logs" by default — not parseable
	// as shim slog JSON, so History yields zero entries (proves the listing
	// + parse path runs without erroring).
	entries, err := b.History(context.Background(), backendsHistoryOpts("billing", "reconcile"))
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries from non-parseable fake logs, got %d", len(entries))
	}
}

func backendsHistoryOpts(app, job string) backends.HistoryOpts {
	return backends.HistoryOpts{App: app, Job: job}
}

// silence unused import linters when corev1 isn't referenced from non-test code paths
var _ = corev1.ConfigMap{}
