package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// SchedulerAPI is the subset of EventBridge Scheduler operations the
// backend uses. Defined as an interface so tests inject an in-memory
// fake and skip the real AWS round-trips.
type SchedulerAPI interface {
	ListSchedules(ctx context.Context, in *scheduler.ListSchedulesInput, opts ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error)
	GetSchedule(ctx context.Context, in *scheduler.GetScheduleInput, opts ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error)
	CreateSchedule(ctx context.Context, in *scheduler.CreateScheduleInput, opts ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error)
	UpdateSchedule(ctx context.Context, in *scheduler.UpdateScheduleInput, opts ...func(*scheduler.Options)) (*scheduler.UpdateScheduleOutput, error)
	DeleteSchedule(ctx context.Context, in *scheduler.DeleteScheduleInput, opts ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error)
}

// STSAPI is the subset of STS operations Backend.Ensure uses (the auth
// probe). Test fakes can return errors to verify Ensure surfaces auth
// failures.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}
