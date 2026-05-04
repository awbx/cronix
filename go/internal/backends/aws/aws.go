// Package aws is the AWS EventBridge Scheduler backend adapter.
//
// Per (job, scheduleIndex) cronix manages one EventBridge Schedule:
//
//	Name:        cronix-<app>-<job>-<idx>
//	Group:       <scheduleGroup>            (operator-configured)
//	Target:      <targetArn>                (Lambda / HTTPS / SQS — operator-configured)
//	Description: cronix-managed app=<app> job=<job> idx=<idx> hash=<hash>
//
// Ownership lives in the name prefix `cronix-` and the structured
// Description field; both are returned by GetSchedule so List needs
// no extra API calls. The backend never touches schedules whose
// description doesn't match the cronix-managed pattern.
//
// # Target shape
//
// EventBridge Scheduler supports HTTPS targets directly, but the
// request payload and headers are defined at schedule-create time and
// therefore cannot carry a fresh-per-fire HMAC signature. cronix's
// signed-trigger contract requires a per-fire timestamp + signature
// over the canonical body, so the AWS backend's recommended target is
// a thin Lambda function that:
//
//  1. Receives the schedule's input (the cronix run payload encoded
//     as base64 JSON, including the (app, job) identifier).
//  2. Resolves the secret from SSM Parameter Store / Secrets Manager.
//  3. Signs the canonical request per spec/RFC.md §Authentication.
//  4. Issues the HTTPS POST to the application's
//     /api/v1/scheduled/<job> endpoint.
//
// The Lambda is deployed once per AWS account; the backend creates one
// EventBridge Schedule per (app, job, index) targeting that single
// Lambda with per-job input. Reference Lambda code lives in
// deploy/aws/cronix-trigger-lambda/ (follow-up).
//
// # Status
//
// Apply / drift / list / prune work end-to-end. History wires a
// deferred CloudWatch Logs reader; returns nil for now.
package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/trigger"
)

// descriptionPrefix marks every cronix-owned EventBridge Schedule.
// Description format: `cronix-managed app=<app> job=<job> idx=<idx> hash=<hex>`.
const descriptionPrefix = "cronix-managed"

// SchedulerAPI is the subset of EventBridge Scheduler operations the
// backend uses. Defined as an interface so tests can inject a fake
// without spinning up real AWS calls.
type SchedulerAPI interface {
	ListSchedules(ctx context.Context, in *scheduler.ListSchedulesInput, opts ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error)
	GetSchedule(ctx context.Context, in *scheduler.GetScheduleInput, opts ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error)
	CreateSchedule(ctx context.Context, in *scheduler.CreateScheduleInput, opts ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error)
	UpdateSchedule(ctx context.Context, in *scheduler.UpdateScheduleInput, opts ...func(*scheduler.Options)) (*scheduler.UpdateScheduleOutput, error)
	DeleteSchedule(ctx context.Context, in *scheduler.DeleteScheduleInput, opts ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error)
}

// STSAPI is the subset of STS operations used by Ensure (auth probe).
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Backend is the AWS EventBridge Scheduler backend.
type Backend struct {
	scheduler     SchedulerAPI
	sts           STSAPI
	scheduleGroup string
	targetArn     string
	roleArn       string
	secretRefs    []string
}

// Options for constructing a Backend.
type Options struct {
	// Region for the scheduler client (defaults to whatever the SDK
	// resolves: env, ~/.aws/config, EC2/EKS metadata).
	Region string
	// ScheduleGroup names the EventBridge schedule group cronix's
	// schedules live in. Operators MUST create the group out-of-band
	// (or accept the AWS-managed "default" group).
	ScheduleGroup string
	// TargetArn is the ARN of the Lambda / HTTPS endpoint the schedule
	// invokes. Required.
	TargetArn string
	// RoleArn is the IAM role EventBridge assumes to invoke TargetArn.
	// Required.
	RoleArn string
	// SecretRefs are the operator-supplied --secret-ref values forwarded
	// to the Lambda shim via the EventBridge Schedule Input. The Lambda
	// resolves them at fire time (env: / file: / raw: locally; ssm: /
	// secretsmanager: via AWS APIs).
	SecretRefs []string

	// Scheduler / STS clients. When nil, a real SDK client is built
	// from the SDK's default config chain. Tests inject fakes.
	Scheduler SchedulerAPI
	STS       STSAPI
}

// New constructs a Backend.
func New(ctx context.Context, opts Options) (*Backend, error) {
	if opts.TargetArn == "" {
		return nil, fmt.Errorf("aws: TargetArn is required")
	}
	if opts.RoleArn == "" {
		return nil, fmt.Errorf("aws: RoleArn is required")
	}
	group := opts.ScheduleGroup
	if group == "" {
		group = "default"
	}
	scl := opts.Scheduler
	stc := opts.STS
	if scl == nil || stc == nil {
		var loadOpts []func(*config.LoadOptions) error
		if opts.Region != "" {
			loadOpts = append(loadOpts, config.WithRegion(opts.Region))
		}
		cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return nil, fmt.Errorf("aws: load SDK config: %w", err)
		}
		if scl == nil {
			scl = scheduler.NewFromConfig(cfg)
		}
		if stc == nil {
			stc = sts.NewFromConfig(cfg)
		}
	}
	return &Backend{
		scheduler:     scl,
		sts:           stc,
		scheduleGroup: group,
		targetArn:     opts.TargetArn,
		roleArn:       opts.RoleArn,
		secretRefs:    append([]string(nil), opts.SecretRefs...),
	}, nil
}

// Name returns "aws-scheduler".
func (*Backend) Name() string { return "aws-scheduler" }

// List enumerates owned schedules in the configured group.
func (b *Backend) List(ctx context.Context) ([]backends.ManagedEntry, error) {
	out := make([]backends.ManagedEntry, 0)
	var next *string
	for {
		resp, err := b.scheduler.ListSchedules(ctx, &scheduler.ListSchedulesInput{
			GroupName:  awssdk.String(b.scheduleGroup),
			NamePrefix: awssdk.String("cronix-"),
			NextToken:  next,
		})
		if err != nil {
			return nil, fmt.Errorf("aws: list schedules: %w", err)
		}
		for i := range resp.Schedules {
			summary := &resp.Schedules[i]
			full, err := b.scheduler.GetSchedule(ctx, &scheduler.GetScheduleInput{
				Name:      summary.Name,
				GroupName: awssdk.String(b.scheduleGroup),
			})
			if err != nil {
				return nil, fmt.Errorf("aws: get schedule %s: %w", awssdk.ToString(summary.Name), err)
			}
			entry, ok := parseDescription(awssdk.ToString(full.Description))
			if !ok {
				continue
			}
			entry.Raw = awssdk.ToString(full.Arn)
			out = append(out, entry)
		}
		if resp.NextToken == nil || awssdk.ToString(resp.NextToken) == "" {
			break
		}
		next = resp.NextToken
	}
	return out, nil
}

// parseDescription reads ownership fields out of the schedule
// description. Format: `cronix-managed app=<app> job=<job> idx=<n> hash=<hex>`.
func parseDescription(s string) (backends.ManagedEntry, bool) {
	if !strings.HasPrefix(s, descriptionPrefix) {
		return backends.ManagedEntry{}, false
	}
	rest := strings.TrimPrefix(s, descriptionPrefix)
	fields := strings.Fields(rest)
	out := backends.ManagedEntry{}
	for _, f := range fields {
		eq := strings.IndexByte(f, '=')
		if eq < 0 {
			continue
		}
		k, v := f[:eq], f[eq+1:]
		switch k {
		case "app":
			out.App = v
		case "job":
			out.Job = v
		case "hash":
			out.Hash = v
		case "idx":
			n, _ := strconv.Atoi(v)
			out.Index = n
		}
	}
	if out.App == "" || out.Job == "" {
		return backends.ManagedEntry{}, false
	}
	return out, true
}

// Create installs one EventBridge Schedule per schedule of `job`.
func (b *Backend) Create(ctx context.Context, app string, job manifest.NormalizedJob) error {
	for i, sched := range job.Schedules {
		expr, err := translateAWSCron(sched)
		if err != nil {
			return err
		}
		hash := hashJobSchedule(job, i)
		name := scheduleName(app, job.Name, i)
		payload, err := b.buildTargetInput(app, job, i)
		if err != nil {
			return fmt.Errorf("aws: build target input for %s: %w", name, err)
		}
		input := &scheduler.CreateScheduleInput{
			Name:               awssdk.String(name),
			GroupName:          awssdk.String(b.scheduleGroup),
			ScheduleExpression: awssdk.String(expr),
			Description:        awssdk.String(buildDescription(app, job.Name, i, hash)),
			FlexibleTimeWindow: &schedulertypes.FlexibleTimeWindow{
				Mode: schedulertypes.FlexibleTimeWindowModeOff,
			},
			Target: &schedulertypes.Target{
				Arn:     awssdk.String(b.targetArn),
				RoleArn: awssdk.String(b.roleArn),
				Input:   awssdk.String(payload),
			},
			ScheduleExpressionTimezone: awssdk.String(timezone(job)),
		}
		if _, err := b.scheduler.CreateSchedule(ctx, input); err != nil {
			return fmt.Errorf("aws: create schedule %s: %w", name, err)
		}
	}
	return nil
}

// Update replaces all owned schedules for (app, job.Name).
func (b *Backend) Update(ctx context.Context, app string, job manifest.NormalizedJob) error {
	if err := b.Delete(ctx, app, job.Name); err != nil {
		return err
	}
	return b.Create(ctx, app, job)
}

// Delete removes all owned schedules for (app, jobName).
func (b *Backend) Delete(ctx context.Context, app, jobName string) error {
	all, err := b.List(ctx)
	if err != nil {
		return err
	}
	for _, e := range all {
		if e.App != app || e.Job != jobName {
			continue
		}
		name := scheduleName(app, jobName, e.Index)
		if _, err := b.scheduler.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
			Name:      awssdk.String(name),
			GroupName: awssdk.String(b.scheduleGroup),
		}); err != nil {
			return fmt.Errorf("aws: delete schedule %s: %w", name, err)
		}
	}
	return nil
}

// Validate checks the schedule maps to AWS Scheduler's cron(...) syntax.
func (*Backend) Validate(job manifest.NormalizedJob) backends.ValidationResult {
	var issues []string
	for i, s := range job.Schedules {
		if _, err := translateAWSCron(s); err != nil {
			issues = append(issues, fmt.Sprintf("schedules[%d]: %v", i, err))
		}
	}
	if n := len(scheduleName("dummyapp", job.Name, 0)); n > 64 {
		issues = append(issues, fmt.Sprintf("schedule name would exceed AWS 64-char limit (got %d)", n))
	}
	return backends.ValidationResult{OK: len(issues) == 0, Issues: issues}
}

// History reads CloudWatch Logs for invocation records. Wiring a CWL
// query is non-trivial (Logs Insights or filter-pattern) and depends
// on operator log-group conventions; v1 returns nil.
func (*Backend) History(_ context.Context, _ backends.HistoryOpts) ([]backends.HistoryEntry, error) {
	return nil, nil
}

// Ensure verifies AWS credentials resolve to a callable identity.
func (b *Backend) Ensure(ctx context.Context) error {
	if _, err := b.sts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err != nil {
		return fmt.Errorf("aws: sts:GetCallerIdentity: %w", err)
	}
	return nil
}

func scheduleName(app, job string, idx int) string {
	return fmt.Sprintf("cronix-%s-%s-%d", app, job, idx)
}

// buildTargetInput is the JSON payload EventBridge passes to the
// Lambda shim. We embed the full SpecFile inline so the Lambda is
// self-contained — no separate spec store (S3, SSM Parameter Store)
// required for v1. The Lambda parses this directly into a SpecFile.
//
// AWS's documented Schedule Input limit is 256 KiB. Typical bodies fit
// comfortably; oversize bodies are a future concern (offload to S3,
// pass the s3 URI inline).
func (b *Backend) buildTargetInput(app string, job manifest.NormalizedJob, idx int) (string, error) {
	spec := trigger.SpecFile{
		App:           app,
		Job:           job,
		SecretRefs:    b.secretRefs,
		ScheduleIndex: idx,
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal spec: %w", err)
	}
	return string(raw), nil
}

func buildDescription(app, jobName string, idx int, hash string) string {
	return fmt.Sprintf("%s app=%s job=%s idx=%d hash=%s", descriptionPrefix, app, jobName, idx, hash)
}

func timezone(job manifest.NormalizedJob) string {
	if job.Timezone == "" {
		return "UTC"
	}
	return job.Timezone
}

// translateAWSCron maps a manifest schedule to EventBridge's
// `cron(min hr day month day-of-week year)` form. Differences vs
// classic 5-field cron:
//
//   - 6 fields total (the trailing year, usually `*`).
//   - day-of-month and day-of-week are mutually exclusive: exactly one
//     must be `?`. AWS rejects `*` on both at the same time.
//
// Standard 5-field cron with `*` on day-of-month becomes `?` here, and
// a non-`*` day-of-week likewise forces day-of-month to `?`.
func translateAWSCron(s string) (string, error) {
	t := strings.TrimSpace(s)
	switch t {
	case "@hourly":
		return "cron(0 * * * ? *)", nil
	case "@daily", "@midnight":
		return "cron(0 0 * * ? *)", nil
	case "@weekly":
		return "cron(0 0 ? * SUN *)", nil
	case "@monthly":
		return "cron(0 0 1 * ? *)", nil
	case "@yearly", "@annually":
		return "cron(0 0 1 1 ? *)", nil
	}
	if strings.HasPrefix(t, "@every") {
		return "", fmt.Errorf("aws-scheduler: @every shortcuts not supported; use rate(...) form which cronix doesn't model yet")
	}
	f := strings.Fields(t)
	if len(f) != 5 {
		return "", fmt.Errorf("aws-scheduler: expected 5-field cron, got %q", s)
	}
	min, hr, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	// AWS requires exactly one of dom/dow to be `?`. Convert standard
	// cron's `*` semantics: prefer `?` on whichever the manifest left
	// unconstrained.
	if dow == "*" && dom != "*" {
		dow = "?"
	} else if dom == "*" && dow != "*" {
		dom = "?"
	} else if dom == "*" && dow == "*" {
		dow = "?"
	} else {
		// Both constrained — AWS will reject. Prefer dom, blank dow.
		dow = "?"
	}
	return fmt.Sprintf("cron(%s %s %s %s %s *)", min, hr, dom, mon, dow), nil
}

// hashJobSchedule mirrors the algorithm used by other backends so the
// reconciler's plan/drift comparison stays backend-agnostic.
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
