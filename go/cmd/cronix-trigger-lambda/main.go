// cronix-trigger-lambda is the AWS Lambda companion to the AWS
// EventBridge Scheduler backend.
//
// EventBridge invokes this Lambda once per fire with a SpecFile JSON
// payload as the event. The Lambda:
//
//  1. Unmarshals the SpecFile.
//  2. Resolves secret refs (env: / file: / raw: locally; ssm: and
//     secretsmanager: against AWS APIs).
//  3. Reuses the existing trigger.Run shim to sign the canonical
//     request and POST to the application URL — exactly the same code
//     path the on-host crontab/systemd/k8s shim uses, so wire format
//     stays byte-identical across backends.
//
// Build (Lambda provided.al2 runtime, arm64):
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
//	  go build -tags lambda.norpc -ldflags="-s -w" -o bootstrap \
//	  ./cmd/cronix-trigger-lambda
//	zip cronix-trigger.zip bootstrap
//
// Deploy: see deploy/aws/cronix-trigger-lambda/README.md.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/awbx/cronix/go/internal/trigger"
)

// handle is the Lambda entrypoint.
//
// EventBridge Scheduler delivers the Schedule's `Input` as the raw event
// body. cronix's AWS backend serializes a trigger.SpecFile into Input,
// so we unmarshal directly.
func handle(ctx context.Context, raw json.RawMessage) (Response, error) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var spec trigger.SpecFile
	if err := json.Unmarshal(raw, &spec); err != nil {
		logger.Error("trigger: parse event", slog.String("err", err.Error()))
		return Response{ExitCode: trigger.ExitInternal, Err: err.Error()}, err
	}
	if spec.App == "" || spec.Job.Name == "" {
		err := fmt.Errorf("trigger: event missing app/job (got %+v)", spec)
		logger.Error("trigger: invalid event", slog.String("err", err.Error()))
		return Response{ExitCode: trigger.ExitInternal, Err: err.Error()}, err
	}

	resolver := newResolver(ctx)

	res := trigger.Run(ctx, trigger.Options{
		App:            spec.App,
		JobName:        spec.Job.Name,
		Spec:           &spec,
		SecretResolver: resolver.Resolve,
		HTTPClient:     &http.Client{},
		Logger:         logger,
	})
	resp := Response{
		ExitCode: res.ExitCode,
		RunID:    res.RunID,
		Status:   res.Status,
		Attempts: res.Attempts,
	}
	if res.Err != nil {
		resp.Err = res.Err.Error()
	}

	// We do not propagate non-zero exits as Lambda errors. EventBridge
	// retries on Lambda errors, but the cronix shim has already done its
	// own retry policy — surfacing more retries from EventBridge would
	// double-fire. Always return nil; the structured log is the audit
	// trail.
	return resp, nil
}

// Response is what the Lambda returns. CloudWatch Logs picks this up via
// the standard Lambda response logging.
type Response struct {
	ExitCode int    `json:"exit_code"`
	RunID    string `json:"run_id,omitempty"`
	Status   int    `json:"status,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
	Err      string `json:"err,omitempty"`
}

func main() {
	// Sanity-check ENV-driven options at start so misconfiguration shows
	// up in CloudWatch immediately rather than on first fire.
	if region := strings.TrimSpace(os.Getenv("AWS_REGION")); region == "" {
		fmt.Fprintln(os.Stderr, "cronix-trigger-lambda: AWS_REGION not set; falling back to SDK chain")
	}
	lambda.Start(handle)
}
