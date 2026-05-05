---
title: Go SDK
description: github.com/awbx/cronix/go/pkg/cronsdk — HMAC verification
---

`cronsdk` is the Go-side SDK for cronix. It verifies HMAC-SHA256 signatures on incoming triggers and exposes the `X-Cron-*` header constants so handlers can read the run id, attempt counter, and fire times the backend supplies.

## Install

```bash
go get github.com/awbx/cronix/go/pkg/cronsdk
```

## Scope

The v1 Go SDK ships **HMAC verification only**. Manifest declaration, registration, and dispatch are deferred — the [TypeScript SDK](/cronix/sdk/typescript/) is the reference for declaring jobs.

If your service is in Go and needs to register schedules today:

1. Author the [manifest](/cronix/concepts/manifest/) JSON directly (or generate it from a Go struct).
2. Serve it from `/.well-known/cron-manifest`, signed with the same HMAC secret.
3. Use this package on the trigger side to verify the signed POSTs the backend sends.

## VerifyOptions

```go
type VerifyOptions struct {
    Secrets        []string
    Method         string
    Path           string
    Body           []byte
    Header         string
    Now            int64 // unix seconds; 0 = time.Now()
    MaxSkewSeconds int   // 0 = 300
}
```

| Field | Type | Default | Notes |
|---|---|---|---|
| `Secrets` | `[]string` | required, ≥ 1 | Accepted secrets, in preference order. The verifier returns the index of the one that matched. |
| `Method` | `string` | required | Request method (`GET`, `POST`, …). Case-insensitive. |
| `Path` | `string` | required | Request path **with query string** (`r.URL.RequestURI()`), exactly as signed by the backend. |
| `Body` | `[]byte` | required | Verbatim body bytes. Empty body is `[]byte{}`, not nil. |
| `Header` | `string` | required | The `X-Cron-Signature` value, e.g. `t=1736000000,v1=<hex>`. |
| `Now` | `int64` | `time.Now().Unix()` | Unix seconds. Override for tests. |
| `MaxSkewSeconds` | `int` | `300` | Replay window. Requests whose `t=` is outside `[now - skew, now + skew]` are rejected. |

## Verify(opts)

```go
func Verify(o VerifyOptions) (Result, error)
```

Returns `Result{SecretIndex: i}` on success, or one of the [sentinel errors](#sentinel-errors) on failure. Comparison is constant-time.

```go
res, err := cronsdk.Verify(cronsdk.VerifyOptions{
    Secrets: []string{currentSecret, previousSecret},
    Method:  r.Method,
    Path:    r.URL.RequestURI(),
    Body:    body,
    Header:  r.Header.Get(cronsdk.HeaderSignature),
})
if err != nil {
    http.Error(w, err.Error(), http.StatusUnauthorized)
    return
}
log.Printf("matched secret index=%d", res.SecretIndex)
```

## VerifyHTTP(r, body, secrets)

```go
func VerifyHTTP(r *http.Request, body []byte, secrets []string) (Result, error)
```

Convenience wrapper. Pulls `X-Cron-Signature` from `r.Header`, builds the path string from `r.URL.RequestURI()`, and delegates to `Verify`.

**Important:** you must read the body bytes **before** calling `VerifyHTTP`. `http.Request.Body` is read-once — `io.ReadAll` it (or use a raw-body middleware) and pass the bytes in.

```go
body, err := io.ReadAll(r.Body)
if err != nil {
    http.Error(w, err.Error(), http.StatusBadRequest)
    return
}
defer r.Body.Close()

if _, err := cronsdk.VerifyHTTP(r, body, secrets); err != nil {
    http.Error(w, err.Error(), http.StatusUnauthorized)
    return
}
```

## Sentinel errors

```go
var (
    ErrMalformedHeader   = auth.ErrMalformedHeader
    ErrStaleTimestamp    = auth.ErrStaleTimestamp
    ErrSignatureMismatch = auth.ErrSignatureMismatch
)
```

Use `errors.Is` to discriminate:

```go
_, err := cronsdk.VerifyHTTP(r, body, secrets)
switch {
case err == nil:
    // proceed
case errors.Is(err, cronsdk.ErrMalformedHeader):
    http.Error(w, "bad signature header", http.StatusBadRequest)
case errors.Is(err, cronsdk.ErrStaleTimestamp):
    http.Error(w, "stale signature", http.StatusUnauthorized)
case errors.Is(err, cronsdk.ErrSignatureMismatch):
    http.Error(w, "bad signature", http.StatusUnauthorized)
default:
    http.Error(w, err.Error(), http.StatusInternalServerError)
}
```

## Header constants

Mirror the values in [`headers.ts`](/cronix/sdk/typescript/#header-constants):

| Constant | Wire name | Purpose |
|---|---|---|
| `HeaderSignature` | `X-Cron-Signature` | The HMAC header (`t=…,v1=…`). |
| `HeaderRunID` | `X-Cron-Run-Id` | Unique id for this fire. Use as a dedup key. |
| `HeaderScheduleName` | `X-Cron-Schedule-Name` | Job name. |
| `HeaderFireTime` | `X-Cron-Fire-Time` | Scheduled fire time, unix seconds. |
| `HeaderFireTimeActual` | `X-Cron-Fire-Time-Actual` | Actual dispatch time, unix seconds. |
| `HeaderAttempt` | `X-Cron-Attempt` | 1-based attempt counter; `>1` = retry. |
| `HeaderPreviousSuccessTime` | `X-Cron-Previous-Success-Time` | Last success, unix seconds. May be empty. |

## Worked example

A `net/http` handler that verifies a trigger, dedupes on run-id, and processes the work:

```go
package main

import (
    "errors"
    "io"
    "log"
    "net/http"
    "os"

    "github.com/awbx/cronix/go/pkg/cronsdk"
)

var seen = newRunIDCache(10_000) // your LRU / Redis / etc.

func handleReconcile(w http.ResponseWriter, r *http.Request) {
    body, err := io.ReadAll(r.Body)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    defer r.Body.Close()

    secrets := []string{
        os.Getenv("CRON_SECRET_V2"),
        os.Getenv("CRON_SECRET_V1"),
    }
    if _, err := cronsdk.VerifyHTTP(r, body, secrets); err != nil {
        switch {
        case errors.Is(err, cronsdk.ErrStaleTimestamp), errors.Is(err, cronsdk.ErrSignatureMismatch):
            http.Error(w, err.Error(), http.StatusUnauthorized)
        default:
            http.Error(w, err.Error(), http.StatusBadRequest)
        }
        return
    }

    runID := r.Header.Get(cronsdk.HeaderRunID)
    if runID == "" {
        http.Error(w, "missing run id", http.StatusBadRequest)
        return
    }
    if !seen.Add(runID) {
        // Already processed this run — ack idempotently.
        w.WriteHeader(http.StatusOK)
        return
    }

    log.Printf("reconcile run=%s attempt=%s", runID, r.Header.Get(cronsdk.HeaderAttempt))
    if err := reconcilePayments(r.Context()); err != nil {
        seen.Forget(runID) // let a retry try again
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    w.WriteHeader(http.StatusOK)
}

func main() {
    http.HandleFunc("/api/v1/scheduled/reconcile-payments", handleReconcile)
    log.Fatal(http.ListenAndServe(":3000", nil))
}
```

### Why dedupe on run-id

Backends retry on transient failure. If the second attempt arrives while the first is still committing, you want to no-op the duplicate rather than double-charge. The `X-Cron-Run-Id` header is identical across retries of the same fire — a small bounded cache (in-memory LRU, Redis SETNX with TTL, etc.) is enough.

## Conformance

Both `cronsdk` and `@awbx/cronix-sdk` re-export cronix's reference HMAC verifier. They pass every case in the canonical signature test vectors byte-for-byte, so a manifest signed by either SDK verifies on either side.

## See also

- [Manifest reference](/cronix/concepts/manifest/) — the JSON shape your Go service should serve at `/.well-known/cron-manifest`.
- [TypeScript SDK](/cronix/sdk/typescript/) — full job declaration and dispatch.
- [`cronix apply`](/cronix/cli/apply/) — push the manifest to a backend.
