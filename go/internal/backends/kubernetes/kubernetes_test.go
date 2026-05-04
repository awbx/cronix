package kubernetes

import (
	"strings"
	"testing"

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
	b, err := New(Options{Image: "img"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	long := strings.Repeat("a", 60)
	res := b.Validate(sampleJob(long, "@hourly"))
	if res.OK {
		t.Errorf("expected validation failure for long name")
	}
}

func TestValidateAcceptsCommonSchedules(t *testing.T) {
	b, err := New(Options{Image: "img"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, s := range []string{"@hourly", "0 0 * * *", "*/15 * * * *"} {
		res := b.Validate(sampleJob("ok", s))
		if !res.OK {
			t.Errorf("schedule %q rejected: %v", s, res.Issues)
		}
	}
}
