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

	spec, err := LoadSpec(opts.SpecDir, opts.App, opts.JobName)
	if err != nil {
		logger.Error("trigger: load spec", slog.String("err", err.Error()))
		return Result{ExitCode: ExitInternal, Err: err}
	}

	secrets, err := ResolveSecrets(spec.SecretRefs)
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

	// Concurrency policy.
	if policy.Concurrency != "Allow" && opts.Lock != nil {
		key := fmt.Sprintf("%s.%s", spec.App, spec.Job.Name)
		// TTL = timeout + headroom so the shim cannot outlive its own lock.
		lockTTL := timeout + 30*time.Second
		handle, err := opts.Lock.Acquire(ctx, key, lockTTL)
		if err != nil {
			if errors.Is(err, locks.ErrContended) {
				if policy.Concurrency == "Replace" {
					// v1: Replace is documented as best-effort host-scope
					// only. We do not currently implement the SIGTERM-the-
					// previous-holder path; behave as Forbid for now and
					// log the intent. Phase 6+ refinement.
					logger.Warn("trigger: lock contended (Replace not implemented in v1)")
				}
				logger.Info("trigger: lock contended", slog.String("scope", policy.ConcurrencyScope))
				return Result{ExitCode: ExitLockContended, RunID: runIDStr}
			}
			logger.Error("trigger: lock acquire", slog.String("err", err.Error()))
			return Result{ExitCode: ExitInternal, Err: err, RunID: runIDStr}
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
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, body, err := fire(ctx, client, secrets, spec, runIDStr, attempt, intendedFire, actualFire, opts.PreviousSuccessTime, timeout)
		lastStatus = status
		lastErr = err

		switch {
		case err != nil:
			logger.Warn("trigger: attempt failed",
				slog.Int("attempt", attempt),
				slog.String("err", err.Error()))
		case status >= 200 && status < 300:
			logger.Info("trigger: success",
				slog.Int("attempt", attempt),
				slog.Int("status", status))
			return Result{ExitCode: ExitOK, RunID: runIDStr, Attempts: attempt, Status: status}
		case status >= 400 && status < 500:
			logger.Error("trigger: app rejected",
				slog.Int("attempt", attempt),
				slog.Int("status", status),
				slog.String("body_excerpt", excerpt(body, 200)))
			return Result{ExitCode: ExitAppRejected, RunID: runIDStr, Attempts: attempt, Status: status}
		default:
			logger.Warn("trigger: server error",
				slog.Int("attempt", attempt),
				slog.Int("status", status),
				slog.String("body_excerpt", excerpt(body, 200)))
		}

		if attempt < maxAttempts {
			backoff := computeBackoff(attempt, minBackoff, maxBackoff)
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
	return Result{ExitCode: ExitRetriesExhausted, RunID: runIDStr, Attempts: maxAttempts, Status: lastStatus, Err: lastErr}
}

// fire issues a single signed HTTP attempt. Returns (status, body bytes, err).
// status is 0 when no response was received.
func fire(
	ctx context.Context,
	client *http.Client,
	secrets []string,
	spec *SpecFile,
	runID string,
	attempt int,
	intendedFire, actualFire, previousSuccess time.Time,
	timeout time.Duration,
) (int, []byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	method := strings.ToUpper(spec.Job.Request.Method)
	if method == "" {
		method = "POST"
	}
	body := []byte(spec.Job.Request.Body)
	url := spec.Job.Request.URL

	req, err := http.NewRequestWithContext(attemptCtx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}

	// Sign with the first secret. Verifier accepts any of the listed
	// secrets per D-019; the shim uses the operator-priority head.
	sig, err := auth.Sign(auth.SignOptions{
		Secret:    secrets[0],
		Method:    method,
		Path:      reqPath(url),
		Body:      body,
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
