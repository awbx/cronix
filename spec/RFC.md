# RFC: cronix — Cron Jobs as Code

## Status

**Draft.** Pre-alpha; v1 in active construction. The implementation phases that
build out this RFC are tracked in `/PLAN.md`.

## Summary

`cronix` puts the schedule next to the handler. An application is the source
of truth for its own scheduled work via a manifest endpoint at
`GET /.well-known/cron-manifest`. The `cronix` reconciler reads that manifest
and installs, updates, or removes entries in the host's native scheduler
(`crontab`, `systemd-timer`, Kubernetes `CronJob`). The host scheduler does
the firing. A small Go binary, `cronix trigger`, runs at every fire and
handles HMAC signing, concurrency locking, timeouts, and retries on the
application's behalf.

The protocol is the product. The reconciler and SDK are reference
implementations.

## Motivation

Today the schedule for a job lives somewhere different from the code that
handles it — a UI, an EventBridge rule, a hand-edited crontab, a separate
YAML repo. Three problems follow:

1. **Drift is invisible.** The handler in code can change without anyone
   updating the schedule, or vice versa. Reviewers see one diff and miss the
   other. Changes are coordinated via process, not tooling.
2. **Onboarding is folklore.** New engineers ask which cron runs where, and
   the answer lives in someone's head, a confluence page, or a long-dead
   ticket. The application can't tell you what it expects to be triggered.
3. **Migration is rewriting.** Moving from a hand-edited crontab to systemd
   timers, or to Kubernetes, requires re-encoding every schedule by hand.
   The schedule isn't portable because it doesn't live with the code.

`cronix` collapses these by making the application declare its schedules in
the same repository, the same review, the same deploy as the handler.
Reconciliation against `crontab`, `systemd-timer`, or Kubernetes is a
mechanical translation of that declaration.

## Goals and Non-goals

### Goals (v1)

- A single small declaration on the app side that names every scheduled
  endpoint and its schedule, timezone, policy, and authentication.
- Reconciliation against the host's native scheduler — no custom daemon, no
  state store, no central coordinator.
- Strong authentication on every fire and every manifest fetch (HMAC).
- Friendly behavior for operators: idempotent `apply`, dry-run `diff`,
  drift detection, never-touch-unmanaged guarantee.
- Cross-implementation correctness via shared conformance vectors.

### Non-goals (v1)

- Long-running scheduler daemon
- Persistent state store of any kind
- Run history database
- Workflow orchestration / job chaining / DAGs
- Built-in web UI
- Plugin system with dynamic loading (backends are compiled in)
- One-shot `run-at` (specific timestamp) jobs
- CRD-based K8s deployment (Helm chart is enough)

## Limitations

These are intrinsic to the design, not bugs. Apps choosing `cronix` accept
them.

### Intrinsic — these will not change in v2 either

1. **App must be reachable at fire time.** No queueing, no buffering. If the
   app is offline, the fire fails and is logged; the next scheduled run is
   the only retry.
2. **At-least-once delivery.** Network errors and host-scheduler quirks mean
   a job *can* fire twice for the same intended fire-time. The run-id
   (UUIDv7, constant across retry attempts) plus app-side dedup is the
   answer. The shim provides the primitive; apps provide the discipline.
3. **1-minute resolution floor.** Cron's 5-field syntax cannot express
   sub-minute intervals. `@every 30s` is rejected by the validator.
4. **No fan-out.** One scheduled job fires one HTTP request. Per-tenant or
   per-region work is the handler's responsibility.
5. **Timeout cancels the connection, not the app's work.** The shim closes
   the connection at the timeout. Apps that want true timeout must check
   `ctx.Done()` (or equivalent) on their side.

### v1 scope choices — addressed in later versions

6. No HA reconciler. `cronix apply` is a single-host operation. Concurrent
   applies are *safe* (file locks, K8s optimistic concurrency) but not
   load-balanced. Operators run from CI; CI serializes naturally.
7. No central history database. History is read on-demand from
   backend-native sources (journald, K8s Events, Pod logs).
8. Limited backend coverage: crontab, systemd-timer, kubernetes only.
9. Lock backends in v1 are Redis-only for `global` scope.
10. TypeScript SDK is the only "full" SDK in v1. Go SDK is
    signature-verification-only. Conformance vectors make porting mechanical.

### When `cronix` is the wrong tool

If you need at-most-once delivery, sub-minute scheduling, queueing for
offline apps, fan-out parameterization, workflow chaining, or DAG
orchestration — use Temporal, BullMQ, Sidekiq, Airflow, or a workflow
engine. Not this.

## Terminology

- **Manifest** — the JSON document an application serves at
  `GET /.well-known/cron-manifest` declaring its scheduled jobs.
- **Job** — one entry in the manifest's `jobs` array. A job has a name, one
  or more schedules, an HTTP request descriptor, and a policy.
- **Schedule** — a 5-field cron expression or one of the documented
  shortcuts (`@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`,
  `@every <duration>`).
- **Backend** — a host scheduler adapter: `crontab`, `systemd-timer`, or
  `kubernetes`.
- **Reconciler** — `cronix apply`, the Go process that reads a manifest and
  brings the backend into agreement with it.
- **Trigger shim** — `cronix trigger <app>.<job>`, the Go process the host
  scheduler invokes at fire time. It signs the HTTP request, acquires a
  concurrency lock, applies the timeout, retries on transient failure, and
  emits structured logs.
- **Run-id** — UUIDv7 generated by the shim, constant across retries within
  a single fire. Apps dedupe on it.
- **Fire** — a single attempt by the host scheduler to invoke a job. A fire
  comprises 1..N HTTP attempts (governed by the retry policy).
- **Owned entry** — a host-scheduler entry created by `cronix apply` and
  marked as managed (D-026). `cronix` never modifies entries it does not
  own.

## The Manifest

A manifest is a JSON document with the top-level shape:

```json
{
  "version": 1,
  "app": "<app-id>",
  "jobs": [ /* one or more job objects */ ]
}
```

The wire-shape JSON Schema is at `spec/manifest.schema.json`, generated
from the canonical Zod schema in `@cronix/sdk` by `ts/scripts/gen-schema.mjs`.
CI fails on drift between the Zod schema and the committed JSON Schema.

### Top-level fields

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | `1` | ✓ | Schema version. Locked at `1` for v1 (D-002). |
| `app` | string | ✓ | App id. Matches `^[a-z][a-z0-9-]{0,62}$`. |
| `jobs` | array | ✓ | 1..256 job objects. Job names must be unique within the manifest. |

### Job fields

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string | — | Required. Matches `^[a-z][a-z0-9-]{0,62}$` (D-003). |
| `schedule` | string | — | One of `schedule` and `schedules` (mutually exclusive) is required. |
| `schedules` | string[] | — | 1..64 schedule expressions. |
| `timezone` | IANA name | `UTC` | Per-job timezone override (D-006). |
| `request` | object | — | Required. See below. |
| `policy` | object | (defaults applied) | Optional. See below. |
| `auth` | object | (none) | Optional. `{ secret_refs: string[] }` per D-019. |

### `request`

| Field | Type | Default | Description |
|---|---|---|---|
| `method` | enum | `POST` | One of `GET`, `POST`, `PUT`, `PATCH`, `DELETE` (D-007). |
| `url` | string | — | Required. Must be `http://` or `https://`. |
| `headers` | map<string,string> | `{}` | Static headers added to every fire. Sorted alphabetically in the canonical normalization. |
| `body` | string | `""` | Request body sent on every fire. Apps that need per-fire bodies must derive them server-side from the run-id. |

### `policy`

| Field | Type | Default | Description |
|---|---|---|---|
| `concurrency` | `Allow`/`Forbid`/`Replace` | `Forbid` | D-009. |
| `concurrency_scope` | `host`/`global` | `host` | D-010. `global` requires a configured Redis lock backend. |
| `timeout_seconds` | int 1..600 | `60` | D-011. Hard ceiling 600. |
| `retries.max_attempts` | int 1..10 | `3` | Within a single fire (D-012). |
| `retries.min_seconds` | int ≥ 0 | `1` | Initial backoff. |
| `retries.max_seconds` | int ≥ 1 | `60` | Backoff cap. |

### Schedule syntax (D-004)

- 5-field cron: `<min> <hour> <dom> <mon> <dow>`. Standard ranges, lists,
  steps. Day-of-week 0–6 with `0` = Sunday.
- Shortcuts: `@hourly`, `@daily` (== `@midnight`), `@weekly`, `@monthly`,
  `@yearly` (== `@annually`).
- `@every <N>{s|m|h}` where the resulting interval is ≥ 60 seconds. (Sub-
  minute is rejected — see Limitation 3.)
- No 6-field cron (no seconds field) in v1.

### Normalization

`applyDefaults(parseManifest(input))` produces a `NormalizedManifest`
where every optional field is filled with its default and the result is
deterministically ordered:

- `jobs` are sorted by `name` ascending.
- A single `schedule` is rewritten as `schedules: [<value>]`.
- `headers` keys are emitted in alphabetical order.
- All other fields preserve their input order (notably: `schedules` array
  preserves the user's intended firing order).

`canonicalize(normalized)` returns a byte-exact JSON serialization. The
TypeScript and Go implementations both implement `canonicalize`/`Canonicalize`
and CI asserts byte equality across implementations for every conformance
vector. Apps and reconcilers should use the canonical form whenever a
hash-stable representation is needed (D-027 — change detection).

### Worked examples

#### Minimal job (single schedule, all defaults)

```json
{
  "version": 1,
  "app": "billing-service",
  "jobs": [
    {
      "name": "reconcile-payments",
      "schedule": "*/15 * * * *",
      "request": { "url": "https://billing.internal/api/v1/scheduled/reconcile-payments" }
    }
  ]
}
```

#### Multi-schedule job with policy and rotated secrets

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
        "url": "https://billing.internal/api/v1/scheduled/settle-invoices",
        "headers": { "Accept": "application/json" }
      },
      "policy": {
        "concurrency": "Forbid",
        "timeout_seconds": 120,
        "retries": { "max_attempts": 5, "min_seconds": 2, "max_seconds": 30 }
      },
      "auth": { "secret_refs": ["BILLING_CRON_V2", "BILLING_CRON_V1"] }
    }
  ]
}
```

### Conformance

Every manifest implementation (the reference TS in `@cronix/sdk`, the
reference Go in `internal/manifest`, and any future SDK in any language)
**must** pass `spec/manifest-vectors.json`. The vectors are the
authoritative correctness contract. Adding or modifying a vector is a spec
change requiring an RFC update.

## Authentication

cronix authenticates **both** manifest fetches and trigger requests with the
same HMAC-SHA256 scheme (D-014). The construction is Stripe-shaped: a single
header carries a timestamp and one or more signature versions, the receiver
recomputes the signature, and the comparison is constant-time.

### Threat model

- **Attacker A1** can passively observe network traffic. Defeated by HTTPS
  (which cronix mandates for manifest URLs and trigger URLs alike).
- **Attacker A2** can replay captured signed requests. Defeated by the
  timestamp + replay-window check (D-017).
- **Attacker A3** can intercept and alter requests in flight (a misissued
  TLS cert, a compromised intermediary). Defeated by the HMAC over
  `<ts>.<METHOD>.<path>.<body>` — any change to method, path, body, or
  timestamp invalidates the signature.
- **Attacker A4** can read application logs. Mitigated by the requirement
  that secrets are never logged. Apps and SDKs MUST `redact()` before
  emitting log lines that include any signed-payload component.
- **Attacker A5** is a former operator whose access to *one* secret has
  been revoked. Defeated by D-019 — multi-secret rotation lets the new
  secret roll out before the old one is removed.

cronix does **not** protect against:

- A compromised app server (the receiver inherently trusts itself).
- An attacker who has stolen a current secret. Detect by monitoring secret
  use; rotate via the multi-secret mechanism.
- Side channels in the HMAC implementation other than timing-of-comparison
  (which we do mitigate). Cryptographic primitives are stdlib.

### Signed-payload construction (D-015)

The byte sequence input to HMAC is:

```
<unix_seconds_decimal>.<METHOD_UPPERCASE>.<PATH>.<BODY_BYTES>
```

- `<unix_seconds_decimal>` is the integer seconds since the Unix epoch,
  rendered in base-10 with no leading zeros, no signs, no fractional part.
- `<METHOD_UPPERCASE>` is the HTTP method uppercased (`POST`, `GET`, …).
  Implementations uppercase the input before signing; the verifier does
  the same so that mixed-case inputs sign and verify symmetrically.
- `<PATH>` is the URL path-and-query as-sent. cronix does not normalize
  paths beyond what the URL parsing layer of the HTTP client/server
  performs; both sides should agree on percent-encoding rules.
- `<BODY_BYTES>` is the request body verbatim. For methods conventionally
  without a body (e.g. `GET`), it is zero bytes.
- The three `.` characters are literal dots. They are unambiguous because
  the timestamp is all digits and the method is uppercase letters; no
  legal value of either field contains `.`.

### Header format (D-016)

```
X-Cron-Signature: t=<unix_seconds>,v1=<lowercase_hex_sha256>
```

- `t` is the timestamp from the canonicalization. It MUST equal the
  timestamp the signer used; mutating it on the wire breaks the signature.
- `v1` is the lowercase hexadecimal HMAC-SHA256 of the canonical payload,
  exactly 64 characters.
- Comma-separated; segment order is not significant; unknown segments are
  ignored (forward-compat). At minimum, the verifier MUST find a `t=`
  segment and a `v1=` segment.
- The `v1=` prefix reserves space for future algorithm upgrades (`v2=`,
  …) without changing the header name.

### Replay window (D-017)

Verifiers reject signatures whose timestamp is more than `maxSkewSeconds`
away from the current time, in either direction. Default is 300 seconds.
Operators may tighten the window per-route. Receivers MUST use a
monotonic-or-NTP-synced clock; cronix assumes ≤ 60s of uncorrected drift
between sender and receiver in production.

### Comparison (D-018)

Implementations MUST use a constant-time comparison primitive on the
HMAC bytes:

- Go: `crypto/subtle.ConstantTimeCompare`.
- TypeScript: a manual XOR loop over equal-length `Uint8Array`s. Do not
  assume `crypto.timingSafeEqual` is available across runtimes.

CI greps for loose comparison adjacent to HMAC values in both languages
(`===`/`!==` near `hmac|signature|sig|mac` in TS, `bytes.Equal` near the
same in Go) and fails on a hit.

### Multiple acceptable secrets (D-019)

The verifier accepts a list of secrets and reports the index of the first
one that produces a matching signature. This enables zero-downtime
rotation:

1. Add the new secret as the highest-priority entry; signers continue
   using the old secret.
2. Roll out the new secret to signers; verifiers accept either.
3. Once all signers have switched, remove the old secret.

Apps reference secrets by `secret_refs` in the manifest, an array of
opaque identifiers (env-var names, Vault paths, K8s Secret keys); the
operator-side configuration resolves each reference to its raw bytes.
The bytes themselves never appear in the manifest.

### Worked example

Inputs:

- `secret`: `whsec_test_primary_aaaaaaaaaaaaaaaaaaaaaaaaaaa`
- `method`: `POST`
- `path`: `/api/v1/scheduled/reconcile-payments`
- `body`: `{"runId":"abc","attempt":1}`
- `timestamp`: `1730000002`

Canonical payload (literal bytes):

```
1730000002.POST./api/v1/scheduled/reconcile-payments.{"runId":"abc","attempt":1}
```

The HMAC-SHA256 hex of that payload with the secret yields the `v1=` value.
The full header is:

```
X-Cron-Signature: t=1730000002,v1=<64 lowercase hex chars>
```

The exact bytes are committed in `spec/auth-vectors.json` under
`verify-ok/post-json-body` (and in the `sign-emits/post-json-body`
counterpart). Both the TypeScript and Go reference implementations
produce the same header.

### Conformance

`spec/auth-vectors.json` is the authoritative correctness contract for any
implementation. The current vector set (35 cases) covers:

- happy path: empty body; GET with no body; POST with JSON body; UTF-8
  body with emoji; 1 MiB body; body with embedded NUL bytes; path with
  percent-encoding; lowercase-method input that is uppercased before
  signing; rotation case where the second secret matches.
- malformed header: empty header; missing `t`; missing `v1`; wrong
  algorithm tag (`v2=`); `v1` of wrong length; `v1` not hex; segment
  with no `=`; non-integer timestamp.
- replay: timestamp 1s past the default window; timestamp 1s before the
  default window; tighter custom window correctly enforced.
- tamper: signature byte flipped; body altered; method altered; path
  altered; wrong secret; multiple secrets none of which match.

Adding a vector is a spec change requiring an RFC update.

## SDK Contract

*(Phase 3 will populate this.)*

## Reconciliation Model

*(Phase 4 will populate this.)*

## Backend Adapter Contract

*(Phase 5 will populate this.)*

## Backend Fidelity Matrix

*(Phase 5 will populate this.)*

## Trigger Shim Behavior

*(Phase 5 will populate this.)*

## CLI

*(Phase 6 will populate this.)*

## Deployment

*(Phase 7 will populate this.)*

## Alternatives Considered

*(Refined throughout.)*

## Prior Art

- **Stripe webhook signing.** The signature header shape
  `t=<unix>,v1=<hex>` (D-016) is borrowed directly from Stripe's webhook
  scheme, which has been battle-tested in production for a decade.
- **Kubernetes `CronJob.concurrencyPolicy`.** The `Allow`/`Forbid`/`Replace`
  vocabulary (D-009) is taken from K8s; cronix uses the same names with
  the same semantics so engineers familiar with K8s do not need to learn
  a new policy vocabulary.
- **systemd `OnCalendar=`.** The systemd-timer backend translates 5-field
  cron expressions into systemd's calendar event syntax via
  `systemd-analyze calendar` (Phase 5c).
- **Vercel/Cloudflare Cron.** Both products allow declaring cron jobs in
  application config. cronix's manifest-served-by-the-app pattern is in
  the same family; the difference is reconciliation against a host-managed
  scheduler rather than a vendor-hosted one.
- **HashiCorp Nomad periodic jobs.** Periodic Nomad jobs declare schedules
  inside the job spec; cronix carries a similar split-of-concerns (declare
  in app, dispatch via host scheduler).
- **Stripe-style "config-as-code" tools** (e.g., Terraform, Crossplane).
  cronix borrows the idempotent-`apply`-from-CI pattern but does not need
  state files because the host scheduler itself is the state of record;
  ownership markers (D-026) replace state.

## Changelog

- **2026-05-04 — Phase 2.** HMAC-SHA256 sign/verify implemented in
  `@cronix/sdk` (Web Crypto API) and `internal/auth` (`crypto/hmac` +
  `crypto/subtle`). 35 conformance vectors at `spec/auth-vectors.json`
  cover happy path, malformed headers, replay window, tampered fields,
  and multi-secret rotation. CI greps for loose-comparison adjacent to
  HMAC values in both languages and runs the TS conformance suite under
  Bun in addition to Node 20+22. RFC §Authentication populated with
  threat model, signed-payload construction, header format, replay
  window, comparison rules, and rotation guidance.
- **2026-05-04 — Phase 1.** Manifest specification, Zod schema in
  `@cronix/sdk` and Go mirror in `internal/manifest`, conformance vectors
  at `spec/manifest-vectors.json` (29 cases), generated JSON Schema at
  `spec/manifest.schema.json`. Both implementations pass all vectors and
  agree byte-for-byte on canonicalized output.
- **2026-05-04 — Phase 0.** Repository scaffolding only. No product code.
  Locked decisions D-001 through D-029 captured in DECISIONS.md. Repo
  layout deviates from `PLAN.md` §6: a polyglot top-level (`spec/`,
  `ts/`, `go/`) replaces the original Go-at-root + TS-in-`packages/`
  design. The deviation is recorded as D-029.

## Open Questions

See `OPEN_QUESTIONS.md`.
