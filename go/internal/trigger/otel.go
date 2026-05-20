package trigger

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TracerName is the instrumentation name used when no Tracer is passed
// in Options. Callers wiring their own TracerProvider should pass the
// same name to keep traces collated.
const TracerName = "github.com/awbx/cronix/go/internal/trigger"

// Span and event names. Locked by D-037 — any change here is a spec
// change.
const (
	spanFire    = "cronix.trigger.fire"
	spanAttempt = "cronix.trigger.attempt"
	spanLock    = "cronix.trigger.lock"

	eventLockHostAcquired = "cronix.lock.acquired"
	eventSignCompleted    = "cronix.sign.completed"
	eventSecretsResolved  = "cronix.secrets.resolved"
)

// Attribute keys. Locked by D-037 — see spec/DECISIONS.md.
const (
	attrApp                = attribute.Key("cronix.app")
	attrJob                = attribute.Key("cronix.job")
	attrRunID              = attribute.Key("cronix.run_id")
	attrSchedule           = attribute.Key("cronix.schedule")
	attrIntendedFireTime   = attribute.Key("cronix.intended_fire_time")
	attrActualFireTime     = attribute.Key("cronix.actual_fire_time")
	attrBackend            = attribute.Key("cronix.backend")
	attrTimeoutSeconds     = attribute.Key("cronix.timeout_seconds")
	attrConcurrencyPolicy  = attribute.Key("cronix.concurrency_policy")
	attrConcurrencyScope   = attribute.Key("cronix.concurrency_scope")
	attrMaxAttempts        = attribute.Key("cronix.max_attempts")
	attrOutcome            = attribute.Key("cronix.outcome")
	attrAttemptsMade       = attribute.Key("cronix.attempts_made")
	attrAttempt            = attribute.Key("cronix.attempt")
	attrRetryReason        = attribute.Key("cronix.retry_reason")
	attrBackoffSeconds     = attribute.Key("cronix.backoff_seconds")
	attrLockBackend        = attribute.Key("cronix.lock.backend")
	attrLockScope          = attribute.Key("cronix.lock.scope")
	attrLockKey            = attribute.Key("cronix.lock.key")
	attrLockOutcome        = attribute.Key("cronix.lock.outcome")
	attrLockTTLSeconds     = attribute.Key("cronix.lock.ttl_seconds")
	attrLockDurationMillis = attribute.Key("cronix.lock.duration_ms")
	attrSecretsCount       = attribute.Key("cronix.secrets.count")

	// HTTP semconv (v1.27.0) — used on attempt spans for the auto-rendered
	// waterfall in any OTel UI. Defined here as constants to avoid an
	// import-time dependency on the full semconv package for every caller.
	attrHTTPMethod     = attribute.Key("http.request.method")
	attrHTTPURL        = attribute.Key("url.full")
	attrHTTPStatusCode = attribute.Key("http.response.status_code")
)

// Outcome values. Locked by D-037.
const (
	OutcomeSuccess           = "success"
	OutcomeAppRejected       = "app_rejected"
	OutcomeRetriesExhausted  = "retries_exhausted"
	OutcomeLockContended     = "lock_contended"
	OutcomeInternalError     = "internal_error"
)

// resolveTracer returns the Options-supplied tracer, or a Tracer from
// the global TracerProvider keyed by TracerName. If OTel is fully
// unconfigured at the global level (no SDK installed), the global
// provider returns a no-op tracer — every Start() returns a non-recording
// span, attribute setters become no-ops, and runtime overhead is zero.
func resolveTracer(t trace.Tracer) trace.Tracer {
	if t != nil {
		return t
	}
	return otel.Tracer(TracerName)
}

// injectTraceContext writes the W3C traceparent (and tracestate if any)
// headers into the outgoing HTTP request, so downstream apps with their
// own OTel SDK chain off cronix.trigger.attempt.
//
// Uses the TraceContext propagator directly (not the global) so adopters
// don't have to remember to call otel.SetTextMapPropagator during SDK
// init. The traceparent header is mandated by D-037; making it
// unconditional avoids a subtle misconfiguration footgun. Adopters who
// need a non-W3C propagator can add it via otel.SetTextMapPropagator —
// this helper composes additively, not exclusively.
func injectTraceContext(ctx context.Context, req *http.Request) {
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(req.Header))
	// Also honor any additional propagators the operator configured globally
	// (Baggage, B3, Jaeger, etc.) — composing with the default.
	if p := otel.GetTextMapPropagator(); p != nil {
		p.Inject(ctx, propagation.HeaderCarrier(req.Header))
	}
}

// classifyRetryReason maps an attempt outcome (status, err) to the
// cronix.retry_reason attribute value documented in D-037.
func classifyRetryReason(status int, err error) string {
	if err != nil {
		if isTimeoutErr(err) {
			return "timeout"
		}
		return "network"
	}
	if status >= 500 {
		return "5xx"
	}
	return ""
}

func isTimeoutErr(err error) bool {
	// net/http surfaces context-cancel as one of two error families;
	// either is "timeout" for our purposes.
	type timeoutError interface{ Timeout() bool }
	for e := err; e != nil; {
		if t, ok := e.(timeoutError); ok && t.Timeout() {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// setSpanError marks a span as ERROR with the given outcome string,
// to match the D-037 status convention.
func setSpanError(s trace.Span, outcome string) {
	s.SetStatus(codes.Error, outcome)
}

// setSpanOK marks a span as OK. The OTel convention is that OK is set
// explicitly only when overriding a previously-set error; for normal
// success we leave Unset (which UIs render as OK). We still expose the
// helper so the success path is symmetric.
func setSpanOK(s trace.Span) {
	s.SetStatus(codes.Ok, "")
}

// timeAttr converts a time.Time to an RFC3339 string attribute. Zero
// time becomes the zero string ("") so attribute filters can match
// "fired before clock available".
func timeAttr(key attribute.Key, t time.Time) attribute.KeyValue {
	if t.IsZero() {
		return key.String("")
	}
	return key.String(t.UTC().Format(time.RFC3339Nano))
}
