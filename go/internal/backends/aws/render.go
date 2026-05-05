package aws

import (
	"encoding/json"
	"fmt"

	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/trigger"
)

// buildTargetInput is the JSON payload EventBridge passes to the Lambda
// shim. The full SpecFile is embedded inline so the Lambda is
// self-contained — no separate spec store (S3, SSM Parameter Store)
// required for v1.
//
// AWS's documented Schedule Input limit is 256 KiB. Typical bodies fit
// comfortably; oversize bodies are a future concern (offload to S3 and
// pass an s3:// URI inline instead).
func buildTargetInput(app string, job manifest.NormalizedJob, idx int, secretRefs []string) (string, error) {
	spec := trigger.SpecFile{
		App:           app,
		Job:           job,
		SecretRefs:    secretRefs,
		ScheduleIndex: idx,
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal spec: %w", err)
	}
	return string(raw), nil
}

// buildDescription produces the schedule description that doubles as
// our ownership marker. Format:
//
//	cronix-managed app=<app> job=<job> idx=<idx> hash=<hex>
//
// parseDescription is the inverse.
func buildDescription(app, jobName string, idx int, hash string) string {
	return fmt.Sprintf("%s app=%s job=%s idx=%d hash=%s", descriptionPrefix, app, jobName, idx, hash)
}

func timezone(job manifest.NormalizedJob) string {
	if job.Timezone == "" {
		return DefaultTimezone
	}
	return job.Timezone
}
