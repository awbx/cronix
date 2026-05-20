package trigger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/awbx/cronix/go/internal/auth"
	"github.com/awbx/cronix/go/internal/headers"
	"github.com/awbx/cronix/go/internal/locks"
)

// Exit code map. Documented in RFC §Trigger Shim Behavior. Stable across
// versions — host schedulers may special-case 75 (EX_TEMPFAIL).
const (
	ExitOK               = 0
	ExitAppRejected      = 1  // 4xx response — do not retry
	ExitRetriesExhausted = 2  // 5xx/network/timeout, no remaining attempts
	ExitInternal         = 3  // panic, config load failure, spec parse error
	ExitLockContended    = 4  // policy=Forbid + lock held; transient
	ExitTempfail         = 75 // POSIX EX_TEMPFAIL — same meaning as 4
)

// Options configures a single Run.
type Options struct {
	// App is the manifest app id; spec is loaded for (App, JobName).
	App string
	// JobName is the job within the manifest app.
	JobName string
	// SpecDir overrides the spec file directory. Empty → env/default.
	SpecDir string
	// Lock is the configured lock backend (flock or redis).
	Lock locks.Lock
	// HTTPClient is injectable for tests; nil → http.DefaultClient with
	// no timeout (the timeout is enforced via context.WithTimeout).
	HTTPClient *http.Client
	// Logger is the structured logger. Nil → slog.Default().
	Logger *slog.Logger
	// Now is injected for tests. Nil → time.Now.
	Now func() time.Time
	// PreviousSuccessTime is forwarded to the X-Cron-Previous-Success-Time
	// header. Apps use it to detect missed fires. The reconciler is
	// responsible for tracking it (out of scope for this phase — pass zero).
	PreviousSuccessTime time.Time
	// IntendedFireTime is forwarded to the X-Cron-Fire-Time header. When
	// the host scheduler does not provide it, the shim uses Now.
	IntendedFireTime time.Time
	// Spec, when set, takes precedence over SpecDir and skips on-disk
	// loading. Used by the AWS Lambda shim where the EventBridge event
	// itself carries the spec — there is no host filesystem to read.
	Spec *SpecFile
	// SecretResolver overrides the default env/file/raw resolver. The
	// AWS Lambda shim uses this to resolve `ssm:` and `secretsmanager:`
	// references against AWS APIs without polluting the trigger package
	// with cloud SDK imports.
	SecretResolver func(refs []string) ([]string, error)
	// Tracer emits per-fire OTel traces per the shape locked in D-037.
	// When nil, the shim falls back to the global TracerProvider —
	// if no SDK is installed at the global level (the default), every
	// Start() returns a no-op span and runtime overhead is effectively zero.
	Tracer trace.Tracer
	// Backend is the host scheduler that invoked this shim, recorded as
	// the cronix.backend trace attribute. Empty string is rendered as
	// "unknown" on the span. Behaviorally a no-op when Tracer is nil.
	Backend string
}

// Result describes the outcome of a single fire.
type Result struct {
	ExitCode int
	RunID    string
	Attempts int
	Status   int // last HTTP status; 0 if no response received
	Err      error
}

// Run executes one fire. Caller should `os.Exit(result.ExitCode)`.
//
// On any panic the shim logs and exits with ExitInternal — the lock is
// still released because of the deferred Release.
func Run(ctx context.Context, opts Options) (res Result) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{}
	}

	defer func() {
		if r := recover(); r != nil {
			logger.Error("trigger: panic", slog.Any("panic", r))
			res = Result{ExitCode: ExitInternal, Err: fmt.Errorf("panic: %v", r)}
		}
	}()

	spec := opts.Spec
	if spec == nil {
		var err error
		spec, err = LoadSpec(opts.SpecDir, opts.App, opts.JobName)
		if err != nil {
			logger.Error("trigger: load spec", slog.String("err", err.Error()))
			return Result{ExitCode: ExitInternal, Err: err}
		}
	}

	resolve := opts.SecretResolver
	if resolve == nil {
		resolve = ResolveSecrets
	}
	secrets, err := resolve(spec.SecretRefs)
	if err != nil {
		logger.Error("trigger: resolve secrets", slog.String("err", err.Error()))
		return Result{ExitCode: ExitInternal, Err: err}
	}

	runID, err := uuid.NewV7()
	if err != nil {
		logger.Error("trigger: gen run-id", slog.String("err", err.Error()))
		return Result{ExitCode: ExitInternal, Err: err}
	}
	runIDStr := runID.String()

	intendedFire := opts.IntendedFireTime
	if intendedFire.IsZero() {
		intendedFire = now()
	}
	actualFire := now()

	logger = logger.With(
		slog.String("app", spec.App),
		slog.String("job", spec.Job.Name),
		slog.String("run_id", runIDStr),
	)

	policy := spec.Job.Policy
	timeout := time.Duration(policy.TimeoutSeconds) * time.Second

	// D-037 root span. resolveTracer falls back to the global tracer,
	// which is a no-op tracer when no SDK is installed — zero overhead
	// for every existing call site that doesn't opt into --otel.
	tracer := resolveTracer(opts.Tracer)
	backend := opts.Backend
	if backend == "" {
		backend = "unknown"
	}
	ctx, span := tracer.Start(ctx, spanFire,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attrApp.String(spec.App),
			attrJob.String(spec.Job.Name),
			attrRunID.String(runIDStr),
			attrSchedule.String(strings.Join(spec.Job.Schedules, ", ")),
			timeAttr(attrIntendedFireTime, intendedFire),
			timeAttr(attrActualFireTime, actualFire),
			attrBackend.String(backend),
			attrTimeoutSeconds.Int(policy.TimeoutSeconds),
			attrConcurrencyPolicy.String(policy.Concurrency),
			attrConcurrencyScope.String(policy.ConcurrencyScope),
			attrMaxAttempts.Int(max(policy.Retries.MaxAttempts, 1)),
		),
	)
	// Outcome attribute is finalized in the deferred closer below.
	var (
		finalOutcome  = OutcomeInternalError
		finalAttempts int
	)
	defer func() {
		span.SetAttributes(
			attrOutcome.String(finalOutcome),
			attrAttemptsMade.Int(finalAttempts),
		)
		if finalOutcome != OutcomeSuccess {
			setSpanError(span, finalOutcome)
		}
		span.End()
	}()
	span.AddEvent(eventSecretsResolved, trace.WithAttributes(
		attrSecretsCount.Int(len(secrets)),
	))

	// Concurrency policy.
	if policy.Concurrency != "Allow" && opts.Lock != nil {
		key := fmt.Sprintf("%s.%s", spec.App, spec.Job.Name)
		// TTL = timeout + headroom so the shim cannot outlive its own lock.
		lockTTL := timeout + 30*time.Second
		var handle locks.Handle
		var err error
		// Global scope gets a child span (D-037); host scope gets an event
		// at the end of acquisition. The split is because flock is <1ms
		// and a span per acquisition wastes storage in any aggregator.
		isGlobalScope := policy.ConcurrencyScope == "global"
		lockStart := now()
		var lockSpan trace.Span
		lockCtx := ctx
		if isGlobalScope {
			lockCtx, lockSpan = tracer.Start(ctx, spanLock,
				trace.WithSpanKind(trace.SpanKindInternal),
				trace.WithAttributes(
					attrLockBackend.String("redis"),
					attrLockScope.String("global"),
					attrLockKey.String("cronix:lock:"+key),
					attrLockTTLSeconds.Int(int(lockTTL.Seconds())),
				),
			)
		}
		if policy.Concurrency == "Replace" {
			// SIGTERM the previous host-local holder, wait up to half the
			// timeout for it to exit, then re-acquire. Non-local holders
			// or holders that refuse to exit surface as ErrContended.
			waitForExit := timeout / 2
			if waitForExit < 5*time.Second {
				waitForExit = 5 * time.Second
			}
			handle, err = locks.AcquireOrReplace(lockCtx, opts.Lock, key, lockTTL, waitForExit)
		} else {
			handle, err = opts.Lock.Acquire(lockCtx, key, lockTTL)
		}
		if err != nil {
			if errors.Is(err, locks.ErrContended) {
				if policy.Concurrency == "Replace" {
					logger.Warn("trigger: replace gave up — holder non-local or refused SIGTERM",
						slog.String("scope", policy.ConcurrencyScope))
				} else {
					logger.Info("trigger: lock contended", slog.String("scope", policy.ConcurrencyScope))
				}
				if lockSpan != nil {
					lockSpan.SetAttributes(attrLockOutcome.String("contended"))
					setSpanError(lockSpan, "contended")
					lockSpan.End()
				}
				finalOutcome = OutcomeLockContended
				return Result{ExitCode: ExitLockContended, RunID: runIDStr}
			}
			logger.Error("trigger: lock acquire", slog.String("err", err.Error()))
			if lockSpan != nil {
				lockSpan.RecordError(err)
				setSpanError(lockSpan, "acquire_failed")
				lockSpan.End()
			}
			return Result{ExitCode: ExitInternal, Err: err, RunID: runIDStr}
		}
		if lockSpan != nil {
			lockSpan.SetAttributes(attrLockOutcome.String("acquired"))
			lockSpan.End()
		} else {
			// Host-scope flock: emit an event on the root span with the
			// duration; cheaper than a child span and still surfaces the
			// information in any UI that renders events on the timeline.
			span.AddEvent(eventLockHostAcquired, trace.WithAttributes(
				attrLockScope.String("host"),
				attrLockDurationMillis.Int(int(now().Sub(lockStart).Milliseconds())),
			))
		}
		if policy.Concurrency == "Replace" {
			logger.Info("trigger: replaced previous holder", slog.String("scope", policy.ConcurrencyScope))
		}
		defer func() {
			if rerr := handle.Release(); rerr != nil {
				logger.Warn("trigger: lock release", slog.String("err", rerr.Error()))
			}
		}()
	}

	maxAttempts := max(policy.Retries.MaxAttempts, 1)
	minBackoff := time.Duration(policy.Retries.MinSeconds) * time.Second
	maxBackoff := time.Duration(policy.Retries.MaxSeconds) * time.Second

	var lastStatus int
	var lastErr error
	var backoff time.Duration
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, body, err := fire(ctx, tracer, client, secrets, spec, runIDStr, attempt, backoff, intendedFire, actualFire, opts.PreviousSuccessTime, timeout)
		lastStatus = status
		lastErr = err
		finalAttempts = attempt

		switch {
		case err != nil:
			logger.Warn("trigger: attempt failed",
				slog.Int("attempt", attempt),
				slog.String("err", err.Error()))
		case status >= 200 && status < 300:
			logger.Info("trigger: success",
				slog.Int("attempt", attempt),
				slog.Int("status", status))
			finalOutcome = OutcomeSuccess
			return Result{ExitCode: ExitOK, RunID: runIDStr, Attempts: attempt, Status: status}
		case status >= 400 && status < 500:
			logger.Error("trigger: app rejected",
				slog.Int("attempt", attempt),
				slog.Int("status", status),
				slog.String("body_excerpt", excerpt(body, 200)))
			finalOutcome = OutcomeAppRejected
			return Result{ExitCode: ExitAppRejected, RunID: runIDStr, Attempts: attempt, Status: status}
		default:
			logger.Warn("trigger: server error",
				slog.Int("attempt", attempt),
				slog.Int("status", status),
				slog.String("body_excerpt", excerpt(body, 200)))
		}

		if attempt < maxAttempts {
			backoff = computeBackoff(attempt, minBackoff, maxBackoff)
			select {
			case <-ctx.Done():
				return Result{ExitCode: ExitInternal, RunID: runIDStr, Attempts: attempt, Status: lastStatus, Err: ctx.Err()}
			case <-time.After(backoff):
			}
		}
	}
	logger.Error("trigger: retries exhausted",
		slog.Int("attempts", maxAttempts),
		slog.Int("last_status", lastStatus))
	finalOutcome = OutcomeRetriesExhausted
	return Result{ExitCode: ExitRetriesExhausted, RunID: runIDStr, Attempts: maxAttempts, Status: lastStatus, Err: lastErr}
}

// fire issues a single signed HTTP attempt. Returns (status, body bytes, err).
// status is 0 when no response was received.
func fire(
	ctx context.Context,
	tracer trace.Tracer,
	client *http.Client,
	secrets []string,
	spec *SpecFile,
	runID string,
	attempt int,
	backoff time.Duration,
	intendedFire, actualFire, previousSuccess time.Time,
	timeout time.Duration,
) (status int, body []byte, retErr error) {
	method := strings.ToUpper(spec.Job.Request.Method)
	if method == "" {
		method = "POST"
	}
	url := spec.Job.Request.URL

	// D-037 child span — one per HTTP attempt. The url.full and
	// http.request.method attributes follow OTel HTTP semconv v1.27.
	attemptCtx, attemptSpan := tracer.Start(ctx, spanAttempt,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attrAttempt.Int(attempt),
			attrHTTPMethod.String(method),
			attrHTTPURL.String(url),
			attrBackoffSeconds.Float64(backoff.Seconds()),
		),
	)
	defer func() {
		if retErr != nil {
			attemptSpan.RecordError(retErr)
			attemptSpan.SetAttributes(attrRetryReason.String(classifyRetryReason(status, retErr)))
			setSpanError(attemptSpan, "request_error")
		} else {
			attemptSpan.SetAttributes(attrHTTPStatusCode.Int(status))
			switch {
			case status >= 200 && status < 300:
				// span stays Unset — OTel UIs render as OK
			case status >= 400 && status < 500:
				setSpanError(attemptSpan, "app_rejected")
			default:
				attemptSpan.SetAttributes(attrRetryReason.String(classifyRetryReason(status, nil)))
				setSpanError(attemptSpan, "server_error")
			}
		}
		attemptSpan.End()
	}()

	reqCtx, cancel := context.WithTimeout(attemptCtx, timeout)
	defer cancel()

	reqBody := []byte(spec.Job.Request.Body)
	req, err := http.NewRequestWithContext(reqCtx, method, url, bytes.NewReader(reqBody))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}

	// Sign with the first secret. Verifier accepts any of the listed
	// secrets per D-019; the shim uses the operator-priority head.
	sig, err := auth.Sign(auth.SignOptions{
		Secret:    secrets[0],
		Method:    method,
		Path:      reqPath(url),
		Body:      reqBody,
		Timestamp: actualFire.Unix(),
	})
	if err != nil {
		return 0, nil, fmt.Errorf("sign: %w", err)
	}
	req.Header.Set(headers.Signature, sig.Header)
	req.Header.Set(headers.RunID, runID)
	req.Header.Set(headers.ScheduleName, spec.Job.Name)
	req.Header.Set(headers.FireTime, fmt.Sprintf("%d", intendedFire.Unix()))
	req.Header.Set(headers.FireTimeActual, fmt.Sprintf("%d", actualFire.Unix()))
	req.Header.Set(headers.Attempt, fmt.Sprintf("%d", attempt))
	if !previousSuccess.IsZero() {
		req.Header.Set(headers.PreviousSuccessTime, fmt.Sprintf("%d", previousSuccess.Unix()))
	}
	for k, v := range spec.Job.Request.Headers {
		req.Header.Set(k, v)
	}
	attemptSpan.AddEvent(eventSignCompleted)

	// Inject W3C traceparent so downstream handlers chain off this span.
	injectTraceContext(attemptCtx, req)

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, respBody, nil
}

func reqPath(rawURL string) string {
	// Use the raw URL string parser to recover the path-and-query exactly
	// as the receiver will see it. Avoid a full url.Parse round-trip
	// because that re-encodes some characters.
	if i := strings.Index(rawURL, "://"); i >= 0 {
		rest := rawURL[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return rest[j:]
		}
		return "/"
	}
	return rawURL
}

func computeBackoff(attempt int, minBackoff, maxBackoff time.Duration) time.Duration {
	// 2^(attempt-1) * minBackoff, capped at maxBackoff.
	d := minBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	if d < minBackoff {
		return minBackoff
	}
	return d
}

func excerpt(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// Stderr writes a message to stderr. Convenience for the binary entrypoint.
func Stderr(msg string) {
	_, _ = fmt.Fprintln(os.Stderr, msg)
}
