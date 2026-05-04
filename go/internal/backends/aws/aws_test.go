package aws

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/trigger"
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
			Concurrency: "Forbid", ConcurrencyScope: "global", TimeoutSeconds: 60,
			Retries: manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 1, MaxSeconds: 60},
		},
		Auth: manifest.NormalizedAuth{SecretRefs: []string{"env:S"}},
	}
}

// fakeScheduler implements SchedulerAPI as an in-memory store.
type fakeScheduler struct {
	schedules map[string]*scheduler.GetScheduleOutput // keyed by Name
}

func newFakeScheduler() *fakeScheduler {
	return &fakeScheduler{schedules: map[string]*scheduler.GetScheduleOutput{}}
}

func (f *fakeScheduler) ListSchedules(_ context.Context, in *scheduler.ListSchedulesInput, _ ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error) {
	out := []schedulertypes.ScheduleSummary{}
	prefix := awssdk.ToString(in.NamePrefix)
	for name := range f.schedules {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		out = append(out, schedulertypes.ScheduleSummary{Name: awssdk.String(name)})
	}
	return &scheduler.ListSchedulesOutput{Schedules: out}, nil
}

func (f *fakeScheduler) GetSchedule(_ context.Context, in *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	got, ok := f.schedules[awssdk.ToString(in.Name)]
	if !ok {
		return nil, errors.New("schedule not found")
	}
	return got, nil
}

func (f *fakeScheduler) CreateSchedule(_ context.Context, in *scheduler.CreateScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error) {
	name := awssdk.ToString(in.Name)
	if _, exists := f.schedules[name]; exists {
		return nil, errors.New("schedule already exists")
	}
	arn := "arn:aws:scheduler:::schedule/" + awssdk.ToString(in.GroupName) + "/" + name
	f.schedules[name] = &scheduler.GetScheduleOutput{
		Name:                       in.Name,
		GroupName:                  in.GroupName,
		Arn:                        awssdk.String(arn),
		Description:                in.Description,
		ScheduleExpression:         in.ScheduleExpression,
		ScheduleExpressionTimezone: in.ScheduleExpressionTimezone,
		Target:                     in.Target,
	}
	return &scheduler.CreateScheduleOutput{ScheduleArn: awssdk.String(arn)}, nil
}

func (f *fakeScheduler) UpdateSchedule(_ context.Context, in *scheduler.UpdateScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.UpdateScheduleOutput, error) {
	name := awssdk.ToString(in.Name)
	got, ok := f.schedules[name]
	if !ok {
		return nil, errors.New("schedule not found")
	}
	got.Description = in.Description
	got.ScheduleExpression = in.ScheduleExpression
	got.ScheduleExpressionTimezone = in.ScheduleExpressionTimezone
	got.Target = in.Target
	return &scheduler.UpdateScheduleOutput{}, nil
}

func (f *fakeScheduler) DeleteSchedule(_ context.Context, in *scheduler.DeleteScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error) {
	delete(f.schedules, awssdk.ToString(in.Name))
	return &scheduler.DeleteScheduleOutput{}, nil
}

// fakeSTS — Ensure path test seam.
type fakeSTS struct{ err error }

func (f *fakeSTS) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &sts.GetCallerIdentityOutput{Account: awssdk.String("123456789012"), Arn: awssdk.String("arn:aws:iam::123456789012:user/test")}, nil
}

func newTestBackend(t *testing.T) (*Backend, *fakeScheduler, *fakeSTS) {
	t.Helper()
	sc := newFakeScheduler()
	st := &fakeSTS{}
	b, err := New(context.Background(), Options{
		ScheduleGroup: "default",
		TargetArn:     "arn:aws:lambda:us-east-1:123456789012:function:cronix-trigger",
		RoleArn:       "arn:aws:iam::123456789012:role/cronix-scheduler",
		SecretRefs:    []string{"env:CRONIX_SECRET"},
		Scheduler:     sc,
		STS:           st,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return b, sc, st
}

func TestNewRequiresTargetAndRole(t *testing.T) {
	if _, err := New(context.Background(), Options{Scheduler: newFakeScheduler(), STS: &fakeSTS{}}); err == nil {
		t.Errorf("expected error when TargetArn missing")
	}
	if _, err := New(context.Background(), Options{TargetArn: "x", Scheduler: newFakeScheduler(), STS: &fakeSTS{}}); err == nil {
		t.Errorf("expected error when RoleArn missing")
	}
}

func TestCreateInstallsOneSchedulePerScheduleIndex(t *testing.T) {
	b, sc, _ := newTestBackend(t)
	job := sampleJob("reconcile", "@hourly", "*/15 * * * *")
	if err := b.Create(context.Background(), "billing", job); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(sc.schedules) != 2 {
		t.Fatalf("expected 2 schedules (one per index), got %d", len(sc.schedules))
	}
	got := sc.schedules["cronix-billing-reconcile-0"]
	if got == nil {
		t.Fatalf("missing cronix-billing-reconcile-0")
	}
	if want := "cron(0 * * * ? *)"; awssdk.ToString(got.ScheduleExpression) != want {
		t.Errorf("@hourly expr = %q, want %q", awssdk.ToString(got.ScheduleExpression), want)
	}
	desc := awssdk.ToString(got.Description)
	for _, want := range []string{"cronix-managed", "app=billing", "job=reconcile", "idx=0", "hash="} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q: %q", want, desc)
		}
	}
	if got2 := sc.schedules["cronix-billing-reconcile-1"]; awssdk.ToString(got2.ScheduleExpression) != "cron(*/15 * * * ? *)" {
		t.Errorf("idx=1 expr = %q", awssdk.ToString(got2.ScheduleExpression))
	}
}

func TestListSkipsForeignSchedules(t *testing.T) {
	b, sc, _ := newTestBackend(t)
	if err := b.Create(context.Background(), "billing", sampleJob("reconcile", "@hourly")); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Inject a non-cronix schedule with the same prefix — must be skipped.
	sc.schedules["cronix-foreign"] = &scheduler.GetScheduleOutput{
		Name:        awssdk.String("cronix-foreign"),
		Description: awssdk.String("hand-rolled by an operator"),
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
		t.Errorf("hash not parsed from description")
	}
}

func TestUpdateChangesHash(t *testing.T) {
	b, _, _ := newTestBackend(t)
	if err := b.Create(context.Background(), "billing", sampleJob("reconcile", "@hourly")); err != nil {
		t.Fatalf("create: %v", err)
	}
	pre, _ := b.List(context.Background())
	if err := b.Update(context.Background(), "billing", sampleJob("reconcile", "@daily")); err != nil {
		t.Fatalf("update: %v", err)
	}
	post, _ := b.List(context.Background())
	if len(post) != 1 {
		t.Fatalf("expected 1 entry after update, got %d", len(post))
	}
	if post[0].Hash == pre[0].Hash {
		t.Errorf("expected hash change after schedule update")
	}
}

func TestDeleteRemovesAllForJob(t *testing.T) {
	b, sc, _ := newTestBackend(t)
	if err := b.Create(context.Background(), "billing", sampleJob("reconcile", "@hourly", "*/15 * * * *")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Delete(context.Background(), "billing", "reconcile"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(sc.schedules) != 0 {
		t.Errorf("expected all schedules deleted, %d remain", len(sc.schedules))
	}
}

func TestValidateRejectsAtSyntax(t *testing.T) {
	b, _, _ := newTestBackend(t)
	res := b.Validate(sampleJob("ok", "@every 5m"))
	if res.OK {
		t.Errorf("expected @every to be rejected")
	}
}

func TestValidateAcceptsCommonSchedules(t *testing.T) {
	b, _, _ := newTestBackend(t)
	for _, s := range []string{"@hourly", "0 0 * * *", "*/15 * * * *", "0 9 * * 1-5"} {
		res := b.Validate(sampleJob("ok", s))
		if !res.OK {
			t.Errorf("rejected %q: %v", s, res.Issues)
		}
	}
}

func TestEnsureSucceedsAndFails(t *testing.T) {
	b, _, st := newTestBackend(t)
	if err := b.Ensure(context.Background()); err != nil {
		t.Errorf("Ensure should succeed against happy fakeSTS: %v", err)
	}
	st.err = errors.New("auth nope")
	if err := b.Ensure(context.Background()); err == nil {
		t.Errorf("Ensure should fail when sts errors")
	}
}

func TestTranslateAWSCronShortcuts(t *testing.T) {
	cases := map[string]string{
		"@hourly":      "cron(0 * * * ? *)",
		"@daily":       "cron(0 0 * * ? *)",
		"@midnight":    "cron(0 0 * * ? *)",
		"@weekly":      "cron(0 0 ? * SUN *)",
		"@monthly":     "cron(0 0 1 * ? *)",
		"@yearly":      "cron(0 0 1 1 ? *)",
		"@annually":    "cron(0 0 1 1 ? *)",
		"*/15 * * * *": "cron(*/15 * * * ? *)",
		"0 9 * * 1-5":  "cron(0 9 ? * 1-5 *)",
	}
	for in, want := range cases {
		got, err := translateAWSCron(in)
		if err != nil || got != want {
			t.Errorf("translate(%q) = (%q, %v), want %q", in, got, err, want)
		}
	}
}

func TestCreateEmbedsSpecInScheduleInput(t *testing.T) {
	b, sc, _ := newTestBackend(t)
	job := sampleJob("reconcile", "@hourly")
	if err := b.Create(context.Background(), "billing", job); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := sc.schedules["cronix-billing-reconcile-0"]
	if got == nil || got.Target == nil || got.Target.Input == nil {
		t.Fatalf("expected schedule with target input, got %+v", got)
	}
	var spec trigger.SpecFile
	if err := json.Unmarshal([]byte(awssdk.ToString(got.Target.Input)), &spec); err != nil {
		t.Fatalf("input is not a SpecFile JSON: %v\nraw=%s", err, awssdk.ToString(got.Target.Input))
	}
	if spec.App != "billing" || spec.Job.Name != "reconcile" {
		t.Errorf("spec app/job mismatch: %+v", spec)
	}
	if spec.ScheduleIndex != 0 {
		t.Errorf("expected ScheduleIndex=0, got %d", spec.ScheduleIndex)
	}
	if len(spec.SecretRefs) != 1 || spec.SecretRefs[0] != "env:CRONIX_SECRET" {
		t.Errorf("expected secret_refs from Options, got %v", spec.SecretRefs)
	}
	if spec.Job.Request.URL != "https://example.com/reconcile" {
		t.Errorf("spec job URL not preserved: %q", spec.Job.Request.URL)
	}
}

func TestParseDescriptionRejectsForeign(t *testing.T) {
	for _, s := range []string{"", "hand-rolled", "cronix-managed app=", "random text"} {
		if _, ok := parseDescription(s); ok {
			t.Errorf("expected parseDescription(%q) to reject", s)
		}
	}
}
