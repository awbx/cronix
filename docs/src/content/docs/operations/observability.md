---
title: Observability
description: OpenTelemetry traces emitted by cronix trigger — spec, attributes, propagation.
---

`cronix trigger` emits an OpenTelemetry trace per fire. The trace shape is locked by [D-037](https://github.com/awbx/cronix/blob/main/spec/DECISIONS.md#d-037-opentelemetry-trace-shape-for-cronix-trigger); this page is the operator-facing reference. Wire any OTLP backend (Honeycomb, Tempo, Datadog, Jaeger, an OpenTelemetry Collector) and you get a coherent picture of fire-and-handler behavior without writing any glue.

## Enabling traces

```sh
cronix trigger --otel <app>.<job>
```

The flag opts into OTel emission. Configuration follows the standard
[OTel SDK environment variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/):

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=https://otel.example.com
export OTEL_EXPORTER_OTLP_HEADERS="authorization=Bearer ..."
export OTEL_SERVICE_NAME=cronix-trigger
export OTEL_RESOURCE_ATTRIBUTES="deployment.environment=prod,service.namespace=ops"
```

Defaults are sane: if the endpoint env var is unset, `--otel` becomes a no-op and the shim runs as if the flag weren't there.

## Trace shape

Every fire produces **one root span and N child spans**, where N is the number of HTTP attempts the retry policy ran (1 for success, up to `max_attempts` for retries-exhausted).

```
cronix.trigger.fire                              (root)
├─ cronix.trigger.lock                           (only if concurrency_scope: global)
├─ cronix.trigger.attempt  (attempt 1)
├─ cronix.trigger.attempt  (attempt 2 — if retried)
└─ cronix.trigger.attempt  (attempt N)
```

Host-scope flock acquisitions add a **span event** (`cronix.lock.acquired`) to the root span instead of a child span — they're <1ms and don't warrant the storage overhead.

### `cronix.trigger.fire` — the root span

Covers the full fire, lock acquisition through final HTTP response (or retry exhaustion).

| Attribute | Type | Notes |
|---|---|---|
| `cronix.app` | string | App ID from the job spec |
| `cronix.job` | string | Job name from the job spec |
| `cronix.run_id` | string | UUIDv7, constant across retry attempts (matches the `Cronix-Run-Id` HTTP header) |
| `cronix.schedule` | string | The 5-field cron expression that fired |
| `cronix.intended_fire_time` | RFC3339 | When the host scheduler intended to fire |
| `cronix.actual_fire_time` | RFC3339 | When the shim actually started |
| `cronix.backend` | string | `crontab` / `systemd-timer` / `kubernetes` / `aws-scheduler` / `vercel` |
| `cronix.timeout_seconds` | int | Per-attempt HTTP timeout |
| `cronix.concurrency_policy` | string | `Allow` / `Forbid` / `Replace` |
| `cronix.concurrency_scope` | string | `host` / `global` |
| `cronix.max_attempts` | int | From the retry policy |
| `cronix.outcome` | string | `success` / `app_rejected` / `retries_exhausted` / `lock_contended` / `internal_error` (set on span end) |
| `cronix.attempts_made` | int | Final attempt count |

Status: `OK` if `outcome=success`, otherwise `ERROR`.

### `cronix.trigger.attempt` — one per HTTP attempt

| Attribute | Type | Notes |
|---|---|---|
| `cronix.attempt` | int | 1-indexed |
| `http.request.method` | string | Per [HTTP semconv](https://opentelemetry.io/docs/specs/semconv/http/) |
| `url.full` | string | Per HTTP semconv |
| `http.response.status_code` | int | Once the response arrives |
| `cronix.retry_reason` | string | `5xx` / `network` / `timeout` — set when this attempt was followed by a retry |
| `cronix.backoff_seconds` | float | Sleep before this attempt; 0 for attempt 1 |

Status: `OK` for 2xx, `ERROR` otherwise. 4xx is `ERROR` because it terminates the fire (no retry).

### `cronix.trigger.lock` — only when `concurrency_scope: global`

| Attribute | Type | Notes |
|---|---|---|
| `cronix.lock.backend` | string | `redis` (v1; pluggable in v2) |
| `cronix.lock.scope` | string | always `global` |
| `cronix.lock.key` | string | `cronix:lock:<app>:<job>` |
| `cronix.lock.outcome` | string | `acquired` / `contended` |
| `cronix.lock.ttl_seconds` | int | Set to the job's `timeout_seconds` |

Status: `OK` on `acquired`, `ERROR` on `contended` (propagates to root as `outcome=lock_contended`).

### Span events

Short-lived steps that don't warrant child spans:

| Event | Where | Attributes |
|---|---|---|
| `cronix.lock.acquired` | root span (host scope only) | `cronix.lock.scope=host`, `cronix.lock.duration_ms` |
| `cronix.sign.completed` | each `cronix.trigger.attempt` span | (none — signing is consistently <1ms) |
| `cronix.secrets.resolved` | root span | `cronix.secrets.count` |

## Propagation

Every outbound HTTP attempt carries a W3C `traceparent` header. If your handler is OTel-instrumented, its spans chain naturally off `cronix.trigger.attempt`:

```
cronix.trigger.fire
└─ cronix.trigger.attempt  ← your handler's incoming-request span chains here
   └─ your handler's app spans
      ├─ db.query
      └─ external.api.call
```

The shim does **not** extract a `traceparent` from anywhere — the host scheduler isn't OTel-aware, so there's no inbound trace context to propagate.

## Querying examples

The attribute set is designed so adopters can answer common ops questions with one or two filters.

**"Show me every failed fire for app `billing-service` in the last hour."**

```
{ cronix.app = "billing-service" AND cronix.outcome != "success" }
```

**"Which jobs are getting `lock_contended` consistently?"**

```
{ cronix.outcome = "lock_contended" } | group by cronix.app, cronix.job, count()
```

**"Which retry policies are inadequate?"** (jobs hitting retries-exhausted regularly)

```
{ cronix.outcome = "retries_exhausted" } | group by cronix.app, cronix.job, count()
```

**"Trace this specific fire end-to-end"** (from the structured log's `run_id`)

```
{ cronix.run_id = "<uuid-from-log>" }
```

The `cronix.run_id` is the join key between structured logs and OTel traces. If you're using the same observability platform for both, set up a derived link from log lines containing `run_id=<value>` to the trace search above.

## Cross-language consistency

The attribute set defined here is the contract that **every cronix-compatible SDK** must use when emitting traces. The TypeScript SDK (`@awbx/cronix-sdk`) emits identical attributes from its in-process trigger path; future Rust / Python / Java SDKs do the same. A query filtering on `cronix.app = "billing-service"` finds fires regardless of which SDK / language emitted them.

## Implementation status

Shipped in `cronix trigger --otel` since [v0.11.0](https://github.com/awbx/cronix/releases). Pass `--backend <name>` to populate the `cronix.backend` attribute; the host scheduler invoking the shim should set it (e.g., the Helm chart sets `--backend=kubernetes`, the systemd unit sets `--backend=systemd-timer`).

The TypeScript SDK's in-process trigger path emits the same trace shape — adopters running cronix as a library, not a separate process, get identical attributes and naturally chain into the same OTel pipeline.

## Going deeper

- [D-037](https://github.com/awbx/cronix/blob/main/spec/DECISIONS.md#d-037-opentelemetry-trace-shape-for-cronix-trigger) — the locked spec
- [Trigger lifecycle](/cronix/concepts/trigger-lifecycle/) — what runs at every fire, in order
- [Production runbook §Dashboards](/cronix/operations/runbook/#dashboards) — Prometheus queries derived from these spans (when using an OTel→Prometheus exporter)
- [OpenTelemetry SDK environment variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/) — for the `OTEL_*` configuration vars
