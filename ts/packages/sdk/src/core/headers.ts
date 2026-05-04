/**
 * X-Cron-* HTTP header names. These are the wire-format header constants
 * for trigger requests and signed manifest fetches. Keep in sync with
 * go/internal/headers/headers.go and DECISIONS.md D-016 / D-021.
 */
export const HeaderSignature = "X-Cron-Signature";
export const HeaderRunId = "X-Cron-Run-Id";
export const HeaderScheduleName = "X-Cron-Schedule-Name";
export const HeaderFireTime = "X-Cron-Fire-Time";
export const HeaderFireTimeActual = "X-Cron-Fire-Time-Actual";
export const HeaderAttempt = "X-Cron-Attempt";
export const HeaderPreviousSuccessTime = "X-Cron-Previous-Success-Time";
