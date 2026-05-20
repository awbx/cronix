package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/awbx/cronix/go/internal/locks/flock"
	"github.com/awbx/cronix/go/internal/trigger"
)

func newTriggerCmd() *cobra.Command {
	var (
		specDir string
		lockDir string
		otelOn  bool
		backend string
	)
	cmd := &cobra.Command{
		Use:   "trigger <app>.<job>",
		Short: "Per-fire executor invoked by the host scheduler",
		Long: `cronix trigger is invoked by the host scheduler (crontab, systemd-timer,
Kubernetes CronJob) at every fire. It loads the job spec from the operator-
managed spec directory, resolves the configured secrets, acquires the
concurrency lock per the job's policy, signs the HTTP request with HMAC-
SHA256, sends it with the configured timeout, and retries on transient
failure with exponential backoff.

Exit codes:
  0   success (any 2xx)
  1   app rejected (4xx) — does not retry
  2   retries exhausted (5xx, network, timeout)
  3   internal error (panic, bad spec, unresolved secrets)
  4   lock contended (Forbid; transient)
  75  same as 4 (POSIX EX_TEMPFAIL)

OpenTelemetry:
  When --otel is set and OTEL_EXPORTER_OTLP_ENDPOINT is configured, the
  shim emits a trace per fire matching the shape locked in spec/
  DECISIONS.md (D-037). Configuration uses the standard OTel env vars
  (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS,
  OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES). If the endpoint env var
  is unset, --otel is a no-op.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.SplitN(args[0], ".", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("trigger: argument must be `<app>.<job>`, got %q", args[0])
			}
			app, jobName := parts[0], parts[1]

			lockBackend, err := flock.New(lockDir)
			if err != nil {
				return fmt.Errorf("trigger: lock backend: %w", err)
			}

			handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
			logger := slog.New(handler)

			ctx := context.Background()
			var tracer trace.Tracer
			if otelOn {
				tp, shutdown, terr := installOTel(ctx)
				if terr != nil {
					logger.Error("trigger: otel init failed; running without traces",
						slog.String("err", terr.Error()))
				} else if tp != nil {
					tracer = tp.Tracer(trigger.TracerName)
					// Flush any pending spans on exit. 5s is a generous bound
					// for a fire that just completed; longer than that and
					// you'd rather lose the trace than block exit.
					defer func() {
						sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						_ = shutdown(sctx)
					}()
				}
			}

			res := trigger.Run(ctx, trigger.Options{
				App:     app,
				JobName: jobName,
				SpecDir: specDir,
				Lock:    lockBackend,
				Logger:  logger,
				Tracer:  tracer,
				Backend: backend,
			})
			os.Exit(res.ExitCode)
			return nil
		},
	}
	cmd.Flags().StringVar(&specDir, "spec-dir", "", "directory containing <app>.<job>.json spec files (default: $CRONIX_JOB_SPEC_DIR or /etc/cronix/jobs)")
	cmd.Flags().StringVar(&lockDir, "lock-dir", "", "directory for flock files (default: /var/lock/cronix)")
	cmd.Flags().BoolVar(&otelOn, "otel", false, "emit OpenTelemetry traces per D-037; configure exporter via OTEL_EXPORTER_OTLP_ENDPOINT etc.")
	cmd.Flags().StringVar(&backend, "backend", "", "host scheduler name for the cronix.backend trace attribute (crontab/systemd-timer/kubernetes/aws-scheduler/vercel)")
	return cmd
}

// installOTel wires the OTLP/HTTP exporter from OTEL_EXPORTER_OTLP_*
// env vars and returns a flush function the CLI invokes on exit. When
// the endpoint env var is unset, returns (nil, no-op, nil) — the
// --otel flag becomes a no-op, exactly as documented in D-037.
func installOTel(ctx context.Context) (*sdktrace.TracerProvider, func(context.Context) error, error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return nil, func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("otlp exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	// Register the W3C TraceContext propagator. injectTraceContext in
	// the shim uses it directly anyway, but setting the global makes
	// any other library cronix imports honor it too.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetTracerProvider(tp)
	return tp, tp.Shutdown, nil
}
