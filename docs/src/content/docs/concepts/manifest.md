---
title: Manifest format
description: The JSON shape your app serves at /.well-known/cron-manifest.
---

A cronix manifest is a JSON document an application serves at `GET /.well-known/cron-manifest`. It declares every scheduled job the app expects to be triggered — the schedule, the URL, the policy, the auth. The reconciler ([`cronix apply`](/cronix/quickstart/)) reads this manifest and brings the host scheduler into agreement with it.

The manifest is the source of truth. Schedules live next to handlers in the same repo, the same review, the same deploy.

## Top-level shape

```json
{
  "version": 1,
  "app": "billing-service",
  "jobs": [ /* one or more job objects */ ]
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `version` | `1` | yes | Schema version. Locked at `1` for v1. |
| `app` | string | yes | App id. Matches `^[a-z][a-z0-9-]{0,62}$`. |
| `jobs` | array | yes | 1..256 job objects. Job names must be unique within the manifest. |

## Per-job fields

| Field | Type | Default | Notes |
|---|---|---|---|
| `name` | string | — | Required. Matches `^[a-z][a-z0-9-]{0,62}$`. |
| `schedule` | string | — | Mutually exclusive with `schedules`. Exactly one of the two must be set. |
| `schedules` | string[] | — | 1..64 schedule expressions. Order is preserved (and is the firing order). |
| `timezone` | IANA name | `UTC` | Per-job timezone override. |
| `request` | object | — | Required. See below. |
| `policy` | object | (defaults applied) | See [Concurrency policies](/cronix/concepts/concurrency/) and [Retries & timeouts](/cronix/concepts/retries/). |
| `auth` | object | (none) | `{ secret_refs: string[] }` — see [Secrets & rotation](/cronix/concepts/secrets/). |

### `request`

| Field | Type | Default | Notes |
|---|---|---|---|
| `method` | enum | `POST` | One of `GET`, `POST`, `PUT`, `PATCH`, `DELETE`. |
| `url` | string | — | Required. Must be `http://` or `https://`. |
| `headers` | map<string,string> | `{}` | Static headers added to every fire. Sorted alphabetically in the canonical normalization. |
| `body` | string | `""` | Request body sent on every fire. Apps that need per-fire bodies must derive them server-side from the run-id (see [Trigger lifecycle](/cronix/concepts/trigger-lifecycle/)). |

### `policy`

| Field | Type | Default | Notes |
|---|---|---|---|
| `concurrency` | `Allow` / `Forbid` / `Replace` | `Forbid` | See [Concurrency policies](/cronix/concepts/concurrency/). |
| `concurrency_scope` | `host` / `global` | `host` | `global` requires a configured Redis lock backend. |
| `timeout_seconds` | int 1..600 | `60` | Hard ceiling on a single attempt. |
| `retries.max_attempts` | int 1..10 | `3` | Within a single fire — retries do NOT cross fires. |
| `retries.min_seconds` | int ≥ 0 | `1` | Initial backoff. |
| `retries.max_seconds` | int ≥ 1 | `60` | Backoff cap. |

### `auth`

| Field | Type | Notes |
|---|---|---|
| `secret_refs` | string[] | 1..8 opaque identifiers (`env:NAME`, `file:/path`, `raw:literal`). The bytes themselves never appear in the manifest — see [Secrets & rotation](/cronix/concepts/secrets/). |

## Schedule syntax

cronix accepts three forms:

| Form | Example | Notes |
|---|---|---|
| 5-field cron | `*/15 * * * *` | `<min> <hour> <dom> <mon> <dow>`. Day-of-week 0–6 with `0` = Sunday. |
| Shortcut | `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly` | `@daily` == `@midnight`; `@yearly` == `@annually`. |
| Interval | `@every 5m`, `@every 2h` | Units `s`, `m`, `h`. The resulting interval must be ≥ 60 seconds — `@every 30s` is rejected at validation time. |

No 6-field cron (no seconds field) in v1.

## Constraints at a glance

| Limit | Value | Where it bites |
|---|---|---|
| `app` regex | `^[a-z][a-z0-9-]{0,62}$` | Top-level `app`. |
| Job `name` regex | `^[a-z][a-z0-9-]{0,62}$` | Per-job `name`. Same regex as `app`. |
| Jobs per manifest | 1..256 | `jobs` array length. |
| Schedules per job | 1..64 | `schedules` array length. |
| Secret refs per job | 1..8 | `auth.secret_refs` length. |
| `timeout_seconds` | 1..600 | Per-job. |
| `retries.max_attempts` | 1..10 | Per-job. |

Job names must be **unique within the manifest**. Duplicate names fail validation with a single error pointing at both occurrences.

## Worked examples

### Minimal job — single schedule, all defaults

```json
{
  "version": 1,
  "app": "billing-service",
  "jobs": [
    {
      "name": "reconcile-payments",
      "schedule": "*/15 * * * *",
      "request": {
        "url": "https://billing.internal/api/v1/scheduled/reconcile-payments"
      }
    }
  ]
}
```

After defaults are applied: method `POST`, body `""`, no headers, concurrency `Forbid`, scope `host`, timeout 60s, retries 3 with 1s..60s backoff, no auth.

### Full job — multi-schedule, custom policy, rotated secrets

```json
{
  "version": 1,
  "app": "billing-service",
  "jobs": [
    {
      "name": "settle-invoices",
      "schedules": ["0 2 * * *", "0 14 * * 1-5"],
      "timezone": "Europe/Paris",
      "request": {
        "method": "POST",
        "url": "https://billing.internal/api/v1/scheduled/settle-invoices",
        "headers": { "Accept": "application/json" },
        "body": ""
      },
      "policy": {
        "concurrency": "Forbid",
        "concurrency_scope": "host",
        "timeout_seconds": 120,
        "retries": { "max_attempts": 5, "min_seconds": 2, "max_seconds": 30 }
      },
      "auth": {
        "secret_refs": ["BILLING_CRON_V2", "BILLING_CRON_V1"]
      }
    }
  ]
}
```

This job fires twice on weekdays (02:00 and 14:00 Paris time) and once on weekends (02:00). Each fire gets up to 5 attempts with 2s..30s backoff. Verifiers accept either of the two listed secrets — see [Secrets & rotation](/cronix/concepts/secrets/) for the rollout pattern.

## Normalization

The reconciler always works on the post-defaults `NormalizedManifest`:

- `jobs` are sorted by `name` ascending.
- A single `schedule` is rewritten as `schedules: [<value>]`.
- `headers` keys are emitted in alphabetical order.
- All other fields preserve their input order — notably the `schedules` array, which preserves the user's intended firing order.
- Every optional field is filled with its default.

`canonicalize(normalized)` returns a byte-exact JSON serialization. The TypeScript and Go implementations agree byte-for-byte for every conformance vector. The hash that powers [drift detection](/cronix/concepts/drift/) is computed from this canonical form.

## Signed manifest fetches

When the reconciler fetches a manifest over HTTPS, the GET is HMAC-signed with the same scheme as triggers — see [Authentication](/cronix/concepts/auth/). Local file manifests skip signing (no transport, no replay window). HTTPS manifest sources require at least one `--secret-ref` on the CLI.

## Conformance

Every manifest implementation — the reference TypeScript SDK, the Go reference in `internal/manifest`, any future SDK in any language — must pass `spec/manifest-vectors.json`. The vectors are the authoritative correctness contract. If your manifest validates in the SDK but is rejected by `cronix validate`, file a bug.
