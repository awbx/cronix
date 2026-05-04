// Package headers defines the X-Cron-* HTTP header names cronix uses on
// signed manifest fetches and trigger requests. Keep in sync with
// ts/packages/sdk/src/core/headers.ts and DECISIONS.md D-016 / D-021.
package headers

const (
	Signature           = "X-Cron-Signature"
	RunID               = "X-Cron-Run-Id"
	ScheduleName        = "X-Cron-Schedule-Name"
	FireTime            = "X-Cron-Fire-Time"
	FireTimeActual      = "X-Cron-Fire-Time-Actual"
	Attempt             = "X-Cron-Attempt"
	PreviousSuccessTime = "X-Cron-Previous-Success-Time"
)
