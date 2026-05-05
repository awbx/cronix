---
title: Trigger lifecycle
description: What happens when the host scheduler fires cronix trigger.
---

`cronix trigger <app>.<job>` is the binary the host scheduler invokes at every fire. The host (`cron(8)`, `systemd-timer`, K8s `CronJob`, EventBridge → Lambda) decides **when** to fire; the trigger shim handles **everything that happens at and after the fire** — secret resolution, signing, locking, the HTTP request, retries, structured logs, the exit code.

This is the synthesis-first principle: backend adapters stay thin, behavior stays uniform across hosts.

## Per-fire lifecycle

```
host scheduler fires `cronix trigger billing-service.settle-invoices`
        │
        ▼
1. Load operator config (~/.cronix/cronix.yaml or $CRONIX_CONFIG)
2. Load job spec  (/etc/cronix/jobs/<app>.<job>.json)
3. Resolve secrets per spec.secret_refs (env: / file: / raw:)
4. Generate run-id (UUIDv7) — constant across all retry attempts
5. Acquire concurrency lock (Forbid → flock or Redis; Allow → skip)
        │
        ▼
6. For attempt = 1..max_attempts:
       a. Build HTTP request with timeout_seconds
       b. Inject X-Cron-* headers
       c. Sign with first resolved secret
       d. Send
       e. On 2xx: success → exit 0
          On 4xx: app rejected → exit 1 (no retry)
          On 5xx / network / timeout: log, sleep backoff, continue
       f. Sleep min_seconds * 2^(attempt-1), capped at max_seconds
        │
        ▼
7. If exhausted: exit 2 (ExitRetriesExhausted)
8. Always: release lock (deferred), emit terminal log line
```

A panic anywhere in this flow is caught by a top-level `recover()`, the lock is released by its deferred handler, and the shim exits `3` (`ExitInternal`).

## Step 1 — Operator config

Resolution order: `--config` flag → `$CRONIX_CONFIG` → `~/.cronix/cronix.yaml` → `/etc/cronix/cronix.yaml`. The schema is loaded with strict unknown-field rejection — typos fail loudly rather than silently being ignored.

## Step 2 — Job spec

The reconciler ([`cronix apply`](/cronix/quickstart/)) writes one `<app>.<job>.json` file per job into `--spec-dir` (default `/etc/cronix/jobs`). The shim reads it at fire time and never touches the manifest source itself.

The spec is the post-defaults `NormalizedJob` plus the app id and the resolved `secret_refs`. Spec dir resolution: explicit `--spec-dir` → `$CRONIX_JOB_SPEC_DIR` → `/etc/cronix/jobs`.

For the AWS EventBridge backend, there is no host filesystem; the `Input` field of the EventBridge schedule carries the spec inline and the Lambda shim reads it from the event.

## Step 3 — Secret resolution

`secret_refs` is an ordered list of `<scheme>:<value>` strings — see [Secrets & rotation](/cronix/concepts/secrets/) for the full reference. Empty resolutions (unset env var, missing file) are skipped with a warning. An empty resulting list is fatal — exit `3` (`ExitInternal`).

## Step 4 — Run-id

A UUIDv7 generated at the top of the fire. It is **constant across all retry attempts** within this fire, and it's the field apps dedupe on for at-least-once delivery. UUIDv7 is time-ordered, so a fleet of run-ids sorts chronologically — useful for log correlation.

## Step 5 — Concurrency lock

Per `policy.concurrency` and `policy.concurrency_scope`:

- `Allow` — skip lock acquisition entirely.
- `Forbid` — try-acquire with `TTL = timeout_seconds + 30s`. On contention, exit `4` (`ExitLockContended`).
- `Replace` — in v1, behaves as `Forbid` and logs the intent. The SIGTERM-the-previous-holder path is deferred.

The lock is released by `defer Release()` — including on panic. See [Concurrency policies](/cronix/concepts/concurrency/) for scope (host/global) and TTL details.

## Step 6 — The attempt loop

Each attempt is one signed HTTP request under a `context.WithTimeout(timeout_seconds)`. The headers the shim injects on every attempt:

| Header | Value | Notes |
|---|---|---|
| `X-Cron-Signature` | `t=<unix>,v1=<hex>` | HMAC-SHA256 over `<unix>.<METHOD>.<path>.<body>`. Signed with the first resolved secret. See [Authentication](/cronix/concepts/auth/). |
| `X-Cron-Run-Id` | UUIDv7 string | Constant across all attempts within this fire. Dedupe on this. |
| `X-Cron-Schedule-Name` | the job's `name` | Identifies which job in the manifest fired. |
| `X-Cron-Fire-Time` | unix seconds | The intended fire-time the host scheduler aimed for. |
| `X-Cron-Fire-Time-Actual` | unix seconds | When the shim actually started. Lets apps detect host-scheduler lag. |
| `X-Cron-Attempt` | `1`..`max_attempts` | Increments per attempt within this fire. |
| `X-Cron-Previous-Success-Time` | unix seconds | When known. Lets apps detect missed fires. |

Plus any `request.headers` declared in the manifest (e.g. `Accept: application/json`).

[Retries & timeouts](/cronix/concepts/retries/) covers the response → outcome mapping, the backoff schedule, and the at-least-once delivery contract in detail.

## Step 7-8 — Terminal outcome

The shim always emits one terminal log line carrying the final status. The lock is released by `defer Release()` — even on panic.

## Exit code map

| Code | Name | Meaning |
|---|---|---|
| `0` | `ExitOK` | Success (any 2xx response). |
| `1` | `ExitAppRejected` | 4xx response. Do not retry. |
| `2` | `ExitRetriesExhausted` | Retries exhausted on 5xx / network / timeout. |
| `3` | `ExitInternal` | Panic, bad spec, unresolved secrets, config error. |
| `4` | `ExitLockContended` | `Forbid` policy + lock held. Transient. |
| `75` | `ExitTempfail` | POSIX `EX_TEMPFAIL`. Same meaning as `4`. Some host schedulers (e.g. `cron(8)` honoring `MAILTO` thresholds) special-case `75` — operators are free to use either. |

The reconciler-side CLI ([`apply`](/cronix/quickstart/), [`drift`](/cronix/concepts/drift/), etc.) has a separate exit-code map; the two are kept disjoint where overlap would be confusing.

## Where the logs go

The shim emits structured logs to stdout (slog-JSON via the Go standard library) and errors to stderr. Every line carries `app`, `job`, `run_id` so a single fire is grep-able end-to-end.

| Host | Where logs land |
|---|---|
| `crontab` | syslog, plus `MAILTO=` if configured. |
| `systemd-timer` | journald — `journalctl -u cronix-<app>-<job>-<idx>`. The shim emits structured fields when `INVOCATION_ID` is set. |
| `kubernetes` | Pod logs of the CronJob's Job. K8s `Event` records are also posted on terminal outcomes when `KUBERNETES_SERVICE_HOST` is set. |
| `aws-scheduler` | CloudWatch Logs of the cronix-trigger Lambda. |

Apps logging at the receiver side should include the inbound `X-Cron-Run-Id` so a single fire can be traced end-to-end across both sides.

## Worked log line

A successful fire emits something like:

```json
{
  "time": "2026-05-05T03:00:01.234Z",
  "level": "INFO",
  "msg": "trigger: success",
  "app": "billing-service",
  "job": "settle-invoices",
  "run_id": "01928f7e-1234-7abc-8def-1234567890ab",
  "attempt": 1,
  "status": 200
}
```

A 5xx-retried-then-succeeded fire emits one WARN per failed attempt followed by the same INFO success line — same `run_id` throughout.

## See also

- [Authentication](/cronix/concepts/auth/) — the signed payload and header format.
- [Secrets & rotation](/cronix/concepts/secrets/) — what `secret_refs` resolves to.
- [Concurrency policies](/cronix/concepts/concurrency/) — the lock acquired in step 5.
- [Retries & timeouts](/cronix/concepts/retries/) — the loop in step 6.
- [Backends overview](/cronix/backends/overview/) — which host scheduler invokes the shim and how.
