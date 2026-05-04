// Package trigger implements the per-fire executor invoked by the host
// scheduler: load spec, acquire lock, sign HMAC, send HTTP, retry with
// backoff, structured-log the outcome.
//
// Phase 5a populates this package.
package trigger
