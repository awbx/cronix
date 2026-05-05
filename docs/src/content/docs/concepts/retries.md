---
title: Retries & timeouts
description: Per-fire retry behavior and request deadlines.
---

Every fire is a single intent — "run this job for fire-time T". The trigger shim turns that intent into 1..N HTTP attempts against your handler. This page explains the timeout, the retry budget, the backoff schedule, and how response status maps to outcomes.

Retries are scoped to **one fire**. They never span across fires. If a fire exhausts its retries at 14:00:00, the next attempt is the next scheduled fire — not a retry of the 14:00 one.

## Settings at a glance

| Setting | Default | Min | Max | Notes |
|---|---|---|---|---|
| `policy.timeout_seconds` | `60` | `1` | `600` | Hard ceiling on a single attempt. |
| `policy.retries.max_attempts` | `3` | `1` | `10` | Total attempts per fire (initial attempt + retries). |
| `policy.retries.min_seconds` | `1` | `0` | — | Initial backoff. |
| `policy.retries.max_seconds` | `60` | `1` | — | Backoff cap. |

`min_seconds` must be ≤ `max_seconds`. Validation rejects manifests that violate this.

## Timeout — what it does

Each attempt runs under a Go `context.WithTimeout(timeout_seconds)`. When the deadline passes:

- The shim closes the HTTP connection.
- The attempt counts as a failure (network/timeout class — see below).
- Retries continue if attempts remain.

Important: closing the connection does **not** cancel the work the app started. Apps that want true timeout must check `ctx.Done()` (or equivalent) on their side. The shim provides the deadline; the app provides the cooperation.

Timeout interacts with the [concurrency lock TTL](/cronix/concepts/concurrency/): `lock_ttl = timeout_seconds + 30s`. Increasing `timeout_seconds` automatically widens the lock window. There is no separate lock-TTL knob.

## Backoff schedule

Backoff between attempts is exponential, capped:

```
backoff(attempt) = min(min_seconds * 2^(attempt-1), max_seconds)
```

With the defaults (`min=1s`, `max=60s`):

| Between attempt | Sleep |
|---|---|
| 1 → 2 | 1s |
| 2 → 3 | 2s |
| 3 → 4 | 4s |
| 4 → 5 | 8s |
| 5 → 6 | 16s |
| 6 → 7 | 32s |
| 7 → 8 | 60s (cap) |
| 8 → 9 | 60s (cap) |
| 9 → 10 | 60s (cap) |

There is no jitter in v1. If you need jitter to spread thundering-herd load, your handler can sleep a small random amount before doing real work — but most jobs don't need it.

## Outcome mapping

The HTTP response (or lack of one) decides whether the attempt is a success, a hard failure, or a retryable failure:

| Response | Class | What the shim does | Exit code (terminal) |
|---|---|---|---|
| 2xx | success | Stop. Emit `event=success` with `attempt`, `status`. | `0` (`ExitOK`) |
| 4xx | app rejected | Stop. **Do not retry.** Emit `event=app-rejected` with `attempt`, `status`, `body_excerpt`. | `1` (`ExitAppRejected`) |
| 5xx | server error | Log and continue to the next attempt (or exit if exhausted). | `2` (`ExitRetriesExhausted`) when no attempts remain |
| Network error | transient | Same as 5xx. | `2` (`ExitRetriesExhausted`) when no attempts remain |
| Timeout | transient | Same as 5xx. | `2` (`ExitRetriesExhausted`) when no attempts remain |

### Why 4xx never retries

4xx is the app saying "this request is wrong — don't retry me." The shim treats every 4xx (400 Bad Request, 401 Unauthorized, 404 Not Found, 422 Unprocessable, …) as an app-level rejection and exits `1` immediately. The next fire is the next opportunity to try a different request.

If your app wants the shim to retry, return 5xx (or close the connection) — not 4xx.

### Why 5xx / network / timeout always retries

Transient failures are exactly what retries are for. cronix burns the retry budget on them. Once the budget is exhausted, it exits `2` so the host scheduler (and operator dashboards) can see "this fire could not get through".

## At-least-once delivery

Combining retries with at-least-once host-scheduler semantics means a job **can** fire twice for the same intended fire-time:

- The shim retries within a fire — multiple HTTP attempts can land if the first one's response was lost in flight.
- The host scheduler can retry the entire fire (e.g. K8s `CronJob` with `backoffLimit > 0`, though cronix sets it to `0` to defer to the shim).
- Network partitions, host reboots, and clock drift can all cause duplicate fires.

**Apps must be idempotent.** The `X-Cron-Run-Id` header is constant across all retries within one fire — apps dedupe on it. The run-id is a UUIDv7, so it's also time-ordered for observability. See [Trigger lifecycle](/cronix/concepts/trigger-lifecycle/) for the headers the shim injects on every attempt.

## Worked example

```json
{
  "version": 1,
  "app": "analytics",
  "jobs": [
    {
      "name": "nightly-rollup",
      "schedule": "0 3 * * *",
      "request": {
        "url": "https://analytics.internal/api/v1/scheduled/nightly-rollup"
      },
      "policy": {
        "timeout_seconds": 300,
        "retries": { "max_attempts": 5, "min_seconds": 5, "max_seconds": 120 }
      }
    }
  ]
}
```

A single fire can take up to:

```
5 attempts × 300s timeout
+ 4 backoff sleeps (5s + 10s + 20s + 40s)
= 1500s + 75s = 1575s ≈ 26 minutes
```

The lock is held for `300 + 30 = 330` seconds *per attempt*. (The lock is released after each attempt's connection closes, then re-acquired for the next attempt — so a long retry sequence does not hold the lock for the entire 26 minutes.)

## Operator-side observability

Every attempt emits one structured log line at INFO or WARN. The terminal outcome line carries the run-id, the attempt count, and the last status. To trace a single fire end-to-end, grep your logs for the `run_id`:

```bash
journalctl -u cronix-billing-service-settle-invoices-0 \
  | grep '"run_id":"01928f7e-...'
```

For K8s, check the Pod logs of the `cronix-billing-service-settle-invoices-0` CronJob.

## See also

- [Trigger lifecycle](/cronix/concepts/trigger-lifecycle/) — the per-fire flow that wraps the retry loop.
- [Concurrency policies](/cronix/concepts/concurrency/) — how the lock TTL is derived from `timeout_seconds`.
- [Manifest format](/cronix/concepts/manifest/) — the `policy.retries` schema.
