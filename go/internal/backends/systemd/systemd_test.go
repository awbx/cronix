package systemd

import (
	"strings"
	"testing"

	"github.com/awbx/cronix/go/internal/manifest"
)

func sampleJob(name string, schedules ...string) manifest.NormalizedJob {
	return manifest.NormalizedJob{
		Name:      name,
		Schedules: schedules,
		Timezone:  "UTC",
		Request: manifest.NormalizedRequest{
			Method: "POST", URL: "https://example.com/" + name,
			Headers: map[string]string{}, Body: "",
		},
		Policy: manifest.NormalizedPolicy{
			Concurrency: "Forbid", ConcurrencyScope: "host", TimeoutSeconds: 60,
			Retries: manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 1, MaxSeconds: 60},
		},
		Auth: manifest.NormalizedAuth{SecretRefs: []string{}},
	}
}

func TestTranslateShortcuts(t *testing.T) {
	cases := map[string]string{
		"@hourly":    "hourly",
		"@daily":     "daily",
		"@midnight":  "daily",
		"@weekly":    "weekly",
		"@monthly":   "monthly",
		"@yearly":    "yearly",
		"@annually":  "yearly",
	}
	for in, want := range cases {
		got, err := translateOnCalendar(in)
		if err != nil || got != want {
			t.Errorf("translate(%q) = (%q, %v), want %q", in, got, err, want)
		}
	}
}

func TestTranslate5Field(t *testing.T) {
	got, err := translateOnCalendar("0 2 * * *")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !strings.Contains(got, "2:0:00") || !strings.Contains(got, "*-*-*") {
		t.Errorf("got %q", got)
	}
}

func TestTranslateWithDOW(t *testing.T) {
	got, err := translateOnCalendar("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !strings.HasPrefix(got, "Mon..Fri") {
		t.Errorf("expected Mon..Fri prefix, got %q", got)
	}
}

func TestRenderUnits(t *testing.T) {
	job := sampleJob("ping", "@hourly")
	timerFile, serviceFile, err := RenderUnits("/usr/local/bin/cronix", "billing", job, 0)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"OnCalendar=hourly", "X-Cronix-App=billing", "X-Cronix-Job=ping", "PartOf=cronix-billing-ping-0.service"} {
		if !strings.Contains(timerFile, want) {
			t.Errorf("timer missing %q:\n%s", want, timerFile)
		}
	}
	for _, want := range []string{"ExecStart=/usr/local/bin/cronix trigger billing.ping", "RuntimeMaxSec=90", "X-Cronix-Job=ping"} {
		if !strings.Contains(serviceFile, want) {
			t.Errorf("service missing %q:\n%s", want, serviceFile)
		}
	}
}

func TestValidateRejectsUnsupportedSchedule(t *testing.T) {
	b, err := New(Options{TriggerBin: "/usr/local/bin/cronix"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	res := b.Validate(sampleJob("x", "not-a-cron"))
	if res.OK {
		t.Errorf("expected validation failure")
	}
}
