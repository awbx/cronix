package trigger

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/awbx/cronix/go/internal/manifest"
)

// newRecorder returns a tracer + the span recorder it writes to, so
// tests can assert against the recorded span tree without depending on
// the global TracerProvider.
func newRecorder(t *testing.T) (trace.Tracer, *tracetest.SpanRecorder) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tp.Tracer("test"), rec
}

// attrMap collapses a span's attributes into a map keyed by string for
// easier assertion.
func attrMap(s sdktrace.ReadOnlySpan) map[string]string {
	m := make(map[string]string, len(s.Attributes()))
	for _, kv := range s.Attributes() {
		m[string(kv.Key)] = kv.Value.Emit()
	}
	return m
}

// findSpan returns the first span whose name matches.
func findSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("span %q not found among recorded spans (got %d total)", name, len(spans))
	return nil
}

func TestOTel_SuccessShape(t *testing.T) {
	setupSecret(t)
	tracer, rec := newRecorder(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Header.Get("Traceparent") == "" {
			t.Errorf("traceparent header was not injected")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	dir, _ := writeSpec(t, u, manifest.NormalizedRetries{MaxAttempts: 1, MinSeconds: 1, MaxSeconds: 1}, "Forbid", 10)

	res := Run(context.Background(), Options{
		App:     "billing",
		JobName: "ping",
		SpecDir: dir,
		Lock:    makeLock(t),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:  tracer,
		Backend: "crontab",
	})
	if res.ExitCode != ExitOK {
		t.Fatalf("exit code: got %d, want %d", res.ExitCode, ExitOK)
	}

	spans := rec.Ended()
	root := findSpan(t, spans, spanFire)
	attempt := findSpan(t, spans, spanAttempt)

	rootAttrs := attrMap(root)
	if rootAttrs["cronix.app"] != "billing" {
		t.Errorf("cronix.app: got %q, want %q", rootAttrs["cronix.app"], "billing")
	}
	if rootAttrs["cronix.job"] != "ping" {
		t.Errorf("cronix.job: got %q, want %q", rootAttrs["cronix.job"], "ping")
	}
	if rootAttrs["cronix.backend"] != "crontab" {
		t.Errorf("cronix.backend: got %q, want %q", rootAttrs["cronix.backend"], "crontab")
	}
	if rootAttrs["cronix.outcome"] != OutcomeSuccess {
		t.Errorf("cronix.outcome: got %q, want %q", rootAttrs["cronix.outcome"], OutcomeSuccess)
	}
	if rootAttrs["cronix.attempts_made"] != "1" {
		t.Errorf("cronix.attempts_made: got %q, want 1", rootAttrs["cronix.attempts_made"])
	}
	if rootAttrs["cronix.run_id"] == "" {
		t.Errorf("cronix.run_id was empty on root span")
	}
	// Run-ID stability check: it must match the Result and the structured-log field.
	if rootAttrs["cronix.run_id"] != res.RunID {
		t.Errorf("cronix.run_id (%q) != Result.RunID (%q)", rootAttrs["cronix.run_id"], res.RunID)
	}
	// Host-scope lock acquisition is an event on the root span, not a child span.
	if len(findEvents(root, eventLockHostAcquired)) != 1 {
		t.Errorf("expected exactly one %s event on root span", eventLockHostAcquired)
	}
	// No cronix.trigger.lock child span when scope is host.
	for _, s := range spans {
		if s.Name() == spanLock {
			t.Errorf("unexpected %s child span — host scope should use an event, not a span", spanLock)
		}
	}

	attemptAttrs := attrMap(attempt)
	if attemptAttrs["cronix.attempt"] != "1" {
		t.Errorf("cronix.attempt: got %q, want 1", attemptAttrs["cronix.attempt"])
	}
	if attemptAttrs["http.request.method"] != "POST" {
		t.Errorf("http.request.method: got %q", attemptAttrs["http.request.method"])
	}
	if attemptAttrs["http.response.status_code"] != "200" {
		t.Errorf("http.response.status_code: got %q", attemptAttrs["http.response.status_code"])
	}
}

func TestOTel_RetryThenSuccess(t *testing.T) {
	setupSecret(t)
	tracer, rec := newRecorder(t)

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	dir, _ := writeSpec(t, u, manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 0, MaxSeconds: 1}, "Forbid", 10)

	res := Run(context.Background(), Options{
		App:     "billing",
		JobName: "ping",
		SpecDir: dir,
		Lock:    makeLock(t),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:  tracer,
		Backend: "kubernetes",
	})
	if res.ExitCode != ExitOK || res.Attempts != 2 {
		t.Fatalf("got exit=%d attempts=%d; want exit=0 attempts=2", res.ExitCode, res.Attempts)
	}

	spans := rec.Ended()
	var attempts []sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == spanAttempt {
			attempts = append(attempts, s)
		}
	}
	if len(attempts) != 2 {
		t.Fatalf("got %d attempt spans, want 2", len(attempts))
	}

	// First attempt should be ERROR with cronix.retry_reason=5xx
	firstAttrs := attrMap(attempts[0])
	if firstAttrs["cronix.attempt"] != "1" {
		t.Errorf("attempt[0].cronix.attempt: got %q, want 1", firstAttrs["cronix.attempt"])
	}
	if firstAttrs["cronix.retry_reason"] != "5xx" {
		t.Errorf("attempt[0].cronix.retry_reason: got %q, want 5xx", firstAttrs["cronix.retry_reason"])
	}
	if firstAttrs["http.response.status_code"] != "502" {
		t.Errorf("attempt[0].http.response.status_code: got %q, want 502", firstAttrs["http.response.status_code"])
	}

	// Second attempt should be OK with status 200
	secondAttrs := attrMap(attempts[1])
	if secondAttrs["cronix.attempt"] != "2" {
		t.Errorf("attempt[1].cronix.attempt: got %q, want 2", secondAttrs["cronix.attempt"])
	}
	if secondAttrs["http.response.status_code"] != "200" {
		t.Errorf("attempt[1].http.response.status_code: got %q, want 200", secondAttrs["http.response.status_code"])
	}

	rootAttrs := attrMap(findSpan(t, spans, spanFire))
	if rootAttrs["cronix.outcome"] != OutcomeSuccess {
		t.Errorf("cronix.outcome: got %q, want %q", rootAttrs["cronix.outcome"], OutcomeSuccess)
	}
	if rootAttrs["cronix.attempts_made"] != "2" {
		t.Errorf("cronix.attempts_made: got %q, want 2", rootAttrs["cronix.attempts_made"])
	}
}

func TestOTel_RetriesExhaustedOutcome(t *testing.T) {
	setupSecret(t)
	tracer, rec := newRecorder(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	dir, _ := writeSpec(t, u, manifest.NormalizedRetries{MaxAttempts: 2, MinSeconds: 0, MaxSeconds: 1}, "Forbid", 10)

	res := Run(context.Background(), Options{
		App:     "billing",
		JobName: "ping",
		SpecDir: dir,
		Lock:    makeLock(t),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:  tracer,
		Backend: "aws-scheduler",
	})
	if res.ExitCode != ExitRetriesExhausted {
		t.Fatalf("exit code: got %d, want %d", res.ExitCode, ExitRetriesExhausted)
	}

	rootAttrs := attrMap(findSpan(t, rec.Ended(), spanFire))
	if rootAttrs["cronix.outcome"] != OutcomeRetriesExhausted {
		t.Errorf("cronix.outcome: got %q, want %q", rootAttrs["cronix.outcome"], OutcomeRetriesExhausted)
	}
	if rootAttrs["cronix.attempts_made"] != "2" {
		t.Errorf("cronix.attempts_made: got %q, want 2", rootAttrs["cronix.attempts_made"])
	}
}

func TestOTel_AppRejected4xxOutcome(t *testing.T) {
	setupSecret(t)
	tracer, rec := newRecorder(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	dir, _ := writeSpec(t, u, manifest.NormalizedRetries{MaxAttempts: 3, MinSeconds: 0, MaxSeconds: 1}, "Forbid", 10)

	res := Run(context.Background(), Options{
		App:     "billing",
		JobName: "ping",
		SpecDir: dir,
		Lock:    makeLock(t),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tracer:  tracer,
	})
	if res.ExitCode != ExitAppRejected || res.Attempts != 1 {
		t.Fatalf("got exit=%d attempts=%d; want exit=1 attempts=1", res.ExitCode, res.Attempts)
	}

	rootAttrs := attrMap(findSpan(t, rec.Ended(), spanFire))
	if rootAttrs["cronix.outcome"] != OutcomeAppRejected {
		t.Errorf("cronix.outcome: got %q, want %q", rootAttrs["cronix.outcome"], OutcomeAppRejected)
	}
	// Backend defaults to "unknown" when caller doesn't set it.
	if rootAttrs["cronix.backend"] != "unknown" {
		t.Errorf("cronix.backend: got %q, want %q", rootAttrs["cronix.backend"], "unknown")
	}
}

func TestOTel_NilTracerIsZeroOverhead(t *testing.T) {
	// Sanity: when Tracer is nil and no global SDK is installed, the
	// shim must still run cleanly with no observable side effects.
	setupSecret(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	dir, _ := writeSpec(t, u, manifest.NormalizedRetries{MaxAttempts: 1, MinSeconds: 1, MaxSeconds: 1}, "Forbid", 10)

	res := Run(context.Background(), Options{
		App:     "billing",
		JobName: "ping",
		SpecDir: dir,
		Lock:    makeLock(t),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Tracer: nil — falls back to global no-op
	})
	if res.ExitCode != ExitOK {
		t.Fatalf("exit code: got %d, want %d", res.ExitCode, ExitOK)
	}
}

// findEvents returns events on the span matching the given name.
func findEvents(s sdktrace.ReadOnlySpan, name string) []sdktrace.Event {
	var out []sdktrace.Event
	for _, e := range s.Events() {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}
