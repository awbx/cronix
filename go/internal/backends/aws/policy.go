package aws

// Backend-local conventions. Cross-backend rules (schedule name template,
// hash algorithm, drift sentinel) live in internal/policy.
const (
	// DefaultScheduleGroup is the EventBridge schedule group cronix
	// places schedules in when Options.ScheduleGroup is empty. AWS
	// auto-creates a group named "default" in every region; operators
	// who want a dedicated group must create it out-of-band.
	DefaultScheduleGroup = "default"

	// DefaultTimezone is the schedule expression timezone cronix sets
	// when a job leaves Timezone unset. EventBridge requires an explicit
	// timezone; UTC is the conventional default.
	DefaultTimezone = "UTC"

	// descriptionPrefix marks every cronix-owned EventBridge Schedule.
	// Full Description format:
	//
	//   cronix-managed app=<app> job=<job> idx=<idx> hash=<hex>
	//
	// Schedules whose Description does not start with this prefix are
	// foreign and never touched.
	descriptionPrefix = "cronix-managed"

	// nameMaxLen is AWS's documented max for a Schedule's Name field.
	// Validate() rejects job/app combinations that would overflow.
	nameMaxLen = 64
)
