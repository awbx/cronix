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
// a thin Lambda function (deploy/aws/cronix-trigger-lambda/) that
// receives the spec inline as the schedule input, signs the canonical
// request, and POSTs to the application.
//
// File layout:
//
//	policy.go    backend-local conventions (DefaultScheduleGroup,
//	             descriptionPrefix, nameMaxLen)
//	parse.go     parseDescription (decode owned schedules)
//	cron.go      schedule expression → AWS cron(...) translator
//	render.go    buildTargetInput (inline SpecFile JSON), buildDescription
//	client.go    SchedulerAPI / STSAPI interfaces (test seams)
//	backend.go   Backend type + Options + Backend interface methods
package aws

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/policy"
)

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
	// schedules live in. Defaults to DefaultScheduleGroup. Operators
	// using a dedicated group MUST create it out-of-band.
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
		group = DefaultScheduleGroup
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
			NamePrefix: awssdk.String(policy.SchedulePrefix),
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

// Create installs one EventBridge Schedule per schedule of `job`.
func (b *Backend) Create(ctx context.Context, app string, job manifest.NormalizedJob) error {
	for i, sched := range job.Schedules {
		expr, err := translateAWSCron(sched)
		if err != nil {
			return err
		}
		hash := policy.Hash(job, i)
		name := policy.ScheduleName(app, job.Name, i)
		payload, err := buildTargetInput(app, job, i, b.secretRefs)
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
		name := policy.ScheduleName(app, jobName, e.Index)
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
	if n := len(policy.ScheduleName("dummyapp", job.Name, 0)); n > nameMaxLen {
		issues = append(issues, fmt.Sprintf("schedule name would exceed AWS %d-char limit (got %d)", nameMaxLen, n))
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

var _ backends.Backend = (*Backend)(nil)
