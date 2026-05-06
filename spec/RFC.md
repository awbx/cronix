# RFC: cronix — Cron Jobs as Code

## Status

**Stable for v1 (release candidate).** The on-the-wire contract — manifest
shape, header format, signed payload, conformance vectors — is frozen.
Code paths backing the contract are implemented except where flagged in
the Backend Fidelity Matrix (live `client-go` integration for the
kubernetes backend and `systemctl`/`journalctl` shell-out for the
systemd-timer backend remain follow-up work; rendering and `Validate`
ship now). The protocol is the product; a v1.0.0-rc.1 tag is the gate
to v1.0.0.

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
from the canonical Zod schema in `@awbx/cronix-sdk` by `ts/scripts/gen-schema.mjs`.
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

Every manifest implementation (the reference TS in `@awbx/cronix-sdk`, the
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

A cronix SDK is a small, framework-agnostic library that helps an app
declare cron jobs in code, build the manifest, and verify signed
incoming requests. It does **not** ship per-framework adapters. Wiring
the SDK into Express, Fastify, Hono, or any other HTTP framework is the
user's job — and a 5-line job at that.

This section is the language-neutral behavioral contract. The reference
implementation is `@awbx/cronix-sdk` (TypeScript, Web Standards). Future SDKs
in any language MUST satisfy the same contract and MUST pass
`spec/manifest-vectors.json` and `spec/auth-vectors.json`.

### Surface

Every SDK exposes four operations on a per-app instance:

| Operation | Synchronous? | Purpose |
|---|---|---|
| `register(definition)` | yes | Add a job to the registry. Throws on duplicate name or invalid definition. |
| `manifest()` | yes | Build the `NormalizedManifest` from the registry. Idempotent. |
| `verify(request)` | async | Verify a signed incoming request. On success returns either a `manifest` outcome or a `trigger` outcome (with a `JobContext` and a `run()` closure that invokes the registered handler). |
| `(implicit) run` | async | Run the handler for a verified trigger context. Languages with first-class closures expose this as `run()` on the trigger outcome; languages without may expose a separate `dispatch(ctx)` function. |

The instance MUST NOT expose anything else as public API. In particular,
SDKs do not own routing, framework integration, response shaping,
authentication-key fetching, retry, or scheduling — all of those are
either the host framework's responsibility or `cronix trigger`'s.

### `register(definition)`

A `JobDefinition` carries:

- `name` — required, kebab-case, matches D-003 regex.
- `schedule` xor `schedules` — exactly one is required.
- `timezone`, `method`, `headers`, `body` — optional, plumb through to
  the manifest.
- `urlOverride` — optional escape hatch when the conventional URL
  (`<baseUrl>/api/v1/scheduled/<name>`) does not fit.
- `policy`, `auth.secret_refs` — optional; merged into the manifest.
- `handler` — the function to call when a verified trigger arrives.

`register` MUST validate the produced single-job manifest by running
`parseManifest` on it. Validation failures throw at register time so
misuse is loud during boot, not on first fire.

### `manifest()`

Returns a `NormalizedManifest`: every optional field defaulted, jobs
sorted by name, headers sorted alphabetically. The output is exactly
what `applyDefaults(parseManifest(rawInput))` produces. SDKs are
responsible for serializing the manifest to JSON when responding to a
GET on `/.well-known/cron-manifest` — typically via the host framework's
JSON helper. (Bytes-on-the-wire don't need to be canonical for the
manifest endpoint; the *receiver* — `cronix apply` — re-canonicalizes
for change detection.)

### `verify(request)`

Inputs are Web-Standards shape:

```ts
{
  kind: 'manifest' | 'trigger',
  method: string,
  path: string,
  body: Uint8Array,
  headers: Record<string, string | string[] | undefined>,
  now?: number,         // unix seconds; default = current time
  maxSkewSeconds?: number, // default 300 per D-017
}
```

The function:

1. Reads `X-Cron-Signature` from headers (case-insensitive). Missing →
   `MissingSignature` (HTTP 401).
2. Resolves the configured secrets (the user passes a string, an array,
   or a function returning either).
3. Calls the language's HMAC verifier per the §Authentication rules.
   Failures surface the underlying error code:
   - `MalformedHeader` → 401
   - `StaleTimestamp` → 401
   - `SignatureMismatch` → 401
4. Validates protocol expectations against the verified request:
   - For `kind: 'manifest'`: method MUST be `GET` (`BadMethod` → 400)
     and path MUST be exactly `/.well-known/cron-manifest` (`BadPath`
     → 404).
   - For `kind: 'trigger'`: path MUST start with
     `/api/v1/scheduled/`, the rest MUST be a registered job name
     (`BadPath` / `UnknownJob` → 404).
5. On the trigger success path, builds a `JobContext` from the
   `X-Cron-*` headers (run-id, attempt, fire times) and binds a
   no-arg `run()` closure that invokes the registered handler.

### Error contract

All `verify` failures return a structured value, not an exception:

```
{ ok: false, status: number, code: string, message: string }
```

The `code` is one of: `MissingSignature`, `MalformedHeader`,
`StaleTimestamp`, `SignatureMismatch`, `BadMethod`, `BadPath`,
`UnknownJob`. SDKs MAY add error codes for language-specific concerns
(e.g. body too large) but MUST keep the canonical seven.

### Wiring patterns (informational)

The reference TypeScript SDK ships three example apps demonstrating the
wiring on Node + Express, Node + Fastify, and Node-or-Bun-or-Workers +
Hono. Each example mounts two routes — one for the manifest endpoint,
one for the trigger endpoint — totalling roughly 30 lines of glue. The
parameterized integration test suite at
`ts/packages/sdk/test/integration.test.ts` exercises all three under
the same set of scenarios (signed manifest, missing signature, signed
trigger dispatch, tampered body, unknown job).

### Conformance

Any SDK in any language passes:

1. `spec/manifest-vectors.json` against its `parseManifest` /
   `applyDefaults` / `canonicalize` implementations (per §The Manifest).
2. `spec/auth-vectors.json` against its `sign` / `verify` (per
   §Authentication).
3. The behavioral test set from §SDK Contract: the same scenarios
   exercised by the reference TS integration suite, applicable to any
   HTTP framework.

## Reconciliation Model

`cronix apply` is the reconciler. It reads a manifest (URL or local file
per D-025), reads the host scheduler's current state via the configured
backend, computes a diff, and brings the backend into agreement with the
manifest. There is no daemon; reconciliation is a single-shot operation
typically invoked from CI.

### State, identity, ownership

cronix tracks ownership per backend (D-026). An entry is "owned" when:

- crontab: a comment line `# cronix:owned app=<app> job=<name> hash=<sha> idx=<n>` is
  attached to the entry by `apply` at create time.
- systemd-timer: the `.timer`/`.service` files are named
  `cronix-<app>-<name>-<idx>.timer` / `.service` (and contain matching
  `app/job/hash` annotations in `[Unit] X-Cronix-*=` keys).
- kubernetes: `CronJob` resources carry the labels
  `cronix.dev/managed=true`, `cronix.dev/app=<app>`,
  `cronix.dev/job=<name>`, `cronix.dev/hash=<sha>`, `cronix.dev/index=<n>`.

`cronix` MUST NOT modify entries it does not own. This is the
co-existence guarantee operators rely on when running cronix alongside
hand-rolled cron entries.

### Identity

The reconciler keys ownership on the tuple `(app, job, index)`:

- **app** — from the manifest's top-level `app` field.
- **job** — the job's `name`.
- **index** — for multi-schedule jobs, the index in `schedules[]`. Single-
  schedule jobs use `index=0`.

The **hash** is `sha256(canonicalize(SubManifestForOneScheduleEntry))`
truncated to 16 hex chars. It is *not* part of the identity tuple — it is
the change-detection signal (D-027).

### State table

For each desired (app, job, index) tuple in the manifest and each managed
entry currently on the backend:

| Desired | Installed | Hash matches | Action |
|---|---|---|---|
| ✓ | ✗ | — | **Create** |
| ✓ | ✓ | ✓ | **Skip** (idempotent — no host-scheduler reload, no log churn) |
| ✓ | ✓ | ✗ | **Update** |
| ✗ | ✓ | — | **Delete** |
| ✗ | ✗ | — | (impossible by construction) |

`Apply` executes the plan in the order **deletes → updates → creates**.
This frees up backend names and resources before allocating new ones —
important when a job is renamed (the old entry must be deleted before
the new entry can be created without a name collision).

### Idempotency

`apply` with a manifest equal to what is already installed MUST be a
complete no-op. Specifically:

- No file writes that change content.
- No `systemctl daemon-reload`.
- No K8s API mutation calls.
- No log lines emitted at level INFO or higher (debug logs may still
  describe the no-op for diagnostic purposes).

Operators run `apply` from CI on every deploy. A noisy idempotent path
makes that workflow expensive and gets the reconciler removed from the
pipeline. CI integration relies on the no-op contract.

### Concurrency safety

Concurrent `apply` invocations against the same host are uncommon (CI
serializes naturally) but MUST NOT corrupt state. Backends serialize
writes:

- crontab: `apply` acquires `/var/lock/cronix/apply.lock` via flock for
  the duration of the apply.
- systemd-timer: same flock.
- kubernetes: relies on K8s optimistic concurrency (`resourceVersion`).
  Per-CronJob `Update` calls retry on conflict.

Cross-host concurrent applies are out of scope for v1 (Limitation 6).

### Drift

`cronix drift` reports entries whose installed `hash` no longer
matches what the current manifest would produce. Drift MAY arise from:

- An operator hand-edited an owned entry. cronix flags but does not auto-
  correct; `apply` will rewrite it on the next run, which is the operator's
  acknowledgement of the drift.
- A new manifest version changed defaults — `apply` will re-create owned
  entries with the new hash.

### Backend interface

The Go `Backend` interface (`go/internal/backends/backend.go`) is the
language-neutral contract for any host-scheduler adapter. v1 ships
four backends; the contract is stable from v1 onward so community
contributions for additional backends become possible.

### Locking primitives

The trigger shim uses the `Lock` interface
(`go/internal/locks/lock.go`) to enforce per-job `concurrency` and
`concurrency_scope` (D-009 / D-010). Two implementations ship in v1:

- **flock** (`go/internal/locks/flock`): OS file locks, default for
  `concurrency_scope: host`. Crashed shims do not leak the lock —
  the kernel releases the file lock on process exit.
- **redis** (`go/internal/locks/redis`): SET-NX-EX with Lua-fenced
  refresh and release, for `concurrency_scope: global`. Fenced
  release prevents a stale Refresh/Release from a previous holder
  from stomping on a current holder. Tested under `miniredis` for
  determinism.

### Operator config

`cronix.yaml` (`go/internal/config`) lists manifest sources, secret
references, lock backends, and per-job defaults. Resolution order is
`--config` → `$CRONIX_CONFIG` → `~/.cronix/cronix.yaml` →
`/etc/cronix/cronix.yaml`. The schema is loaded with strict
`KnownFields(true)` so typos in field names fail loudly rather than
silently being ignored.

## Backend Adapter Contract

A backend adapter is a translator: it takes language-neutral
`NormalizedJob` values and renders them into the host scheduler's native
artifacts (a crontab line block, a systemd `.timer`/`.service` pair, a
Kubernetes `CronJob` resource).

The Go interface is defined in `go/internal/backends/backend.go`:

```go
type Backend interface {
    Name() string
    List(ctx context.Context) ([]ManagedEntry, error)
    Create(ctx context.Context, app string, job NormalizedJob) error
    Update(ctx context.Context, app string, job NormalizedJob) error
    Delete(ctx context.Context, app, jobName string) error
    Validate(job NormalizedJob) ValidationResult
    History(ctx context.Context, opts HistoryOpts) ([]HistoryEntry, error)
    Ensure(ctx context.Context) error
}
```

Contract requirements:

- **Ownership**: every artifact `Create` produces MUST carry an
  unforgeable cronix marker (D-026). `List` MUST return only owned
  artifacts. `Delete` MUST refuse to delete artifacts cronix did not
  create.
- **Multi-schedule**: a job with N schedules produces N owned entries
  with distinct `index` values 0..N-1. `Update` and `Delete` operate
  per (app, job) — the reconciler always updates or deletes all
  schedules of a job atomically.
- **Idempotency**: `List` is read-only. `Update` of an unchanged job
  MAY rewrite files but MUST NOT cause user-visible side effects (no
  `systemctl daemon-reload` when content is byte-identical, no K8s
  resource version bump, no log lines at INFO+).
- **Validation**: `Validate` returns `OK: false` with explanatory
  issues when the backend cannot faithfully express the job (e.g.
  crontab cannot honor per-job timezone, systemd-timer's OnCalendar
  cannot express some rare cron forms). The reconciler aborts the
  apply when validation fails.

The interface is stable from v1 onward. Community contributions for new
backends become possible after v1 ships.

## Backend Fidelity Matrix

| Capability | crontab | systemd-timer | kubernetes |
|---|---|---|---|
| 5-field cron | ✓ native | ✓ via `OnCalendar=` translation | ✓ native |
| Shortcuts (`@hourly`, …) | ✓ via translation | ✓ native (`OnCalendar=hourly`) | ✓ via translation |
| `@every <N>{m\|h}` | ✓ when N evenly divides 60/24 | ✓ via `OnCalendar=*-*-* */N` | ✗ rejected at Validate |
| Sub-minute (`@every <Ns>`) | ✗ | ✗ in v1 (deferred to v1.1) | ✗ |
| Per-job timezone | ✗ (system TZ only — flagged) | ✓ if systemd ≥ 240 | ✓ via `spec.timeZone` |
| Concurrency policy enforcement | by shim | by shim (+ `RuntimeMaxSec=` kills runaway) | by shim (+ `concurrencyPolicy: Forbid` belt-and-suspenders) |
| Native retry / backoff | none — done by shim | none — done by shim | `backoffLimit: 0` defers entirely to shim |
| Run history source | syslog / `MAILTO=` | `journalctl -u cronix-...` | K8s Events + Pod logs |

The "by shim" pattern is the synthesis-first principle (D-028): the host
scheduler decides *when to fire*; the shim handles *everything that
happens at and after the fire*. This keeps backend adapters thin and
behavior uniform across hosts.

## Trigger Shim Behavior

`cronix trigger <app>.<name>` is the binary the host scheduler invokes
at every fire. Its full per-fire lifecycle:

1. **Load operator config** from `--config` / `$CRONIX_CONFIG` /
   `~/.cronix/cronix.yaml` / `/etc/cronix/cronix.yaml`.
2. **Load job spec** from `<spec-dir>/<app>.<name>.json`, where
   `spec-dir` defaults to `$CRONIX_JOB_SPEC_DIR` or `/etc/cronix/jobs`.
   The spec is the post-defaults `NormalizedJob` plus the app id and
   the resolved `secret_refs`. The reconciler writes this file at
   apply time.
3. **Resolve secrets** per the `secret_refs` in the spec. Schemes:
   `env:NAME`, `file:/path`, `raw:literal`. Empty resolutions are
   skipped (with a warning log); an empty resulting list is fatal
   (`ExitInternal`).
4. **Generate run-id**: a UUIDv7. Constant across all retry attempts
   within this fire (D-020).
5. **Acquire concurrency lock** per `policy.concurrency` and
   `policy.concurrency_scope` (D-009/D-010):
   - `Allow`: skip lock acquisition entirely.
   - `Forbid`: try-acquire with TTL = `timeout_seconds + 30s`. On
     contention, exit `ExitLockContended` (4) with a structured log.
   - `Replace`: in v1, behaves as `Forbid` and logs the intent
     (the SIGTERM-the-previous-holder path is deferred to a follow-up version).
6. **Per-attempt loop** (1..`policy.retries.max_attempts`):
   1. Build the HTTP request with `policy.timeout_seconds` enforced
      via `context.WithTimeout`.
   2. Inject `X-Cron-*` headers: `Run-Id`, `Schedule-Name`,
      `Fire-Time` (intended), `Fire-Time-Actual`, `Attempt`,
      `Previous-Success-Time` (when known).
   3. Sign per the §Authentication rules using the first resolved secret
      (verifier accepts any of the listed secrets per D-019).
   4. Send. On 2xx: success → `ExitOK` (0). On 4xx: app rejected →
      `ExitAppRejected` (1), do not retry. On 5xx / network /
      timeout: log and continue.
   5. Sleep with exponential backoff
      `min_seconds * 2^(attempt-1)`, capped at `max_seconds`.
7. **Exhausted retries** → `ExitRetriesExhausted` (2).
8. **Always release the lock** on exit (`defer Release()`).
9. **Panic recovery**: a top-level `recover()` logs the panic, releases
   the lock via the deferred Release, and exits `ExitInternal` (3).

### Exit code map

| Code | Name | Meaning |
|---|---|---|
| 0 | `ExitOK` | success (any 2xx) |
| 1 | `ExitAppRejected` | 4xx — do not retry |
| 2 | `ExitRetriesExhausted` | retries exhausted on 5xx/network/timeout |
| 3 | `ExitInternal` | panic, bad spec, unresolved secrets, config error |
| 4 | `ExitLockContended` | `Forbid` policy + lock held; transient |
| 75 | `ExitTempfail` | POSIX `EX_TEMPFAIL`, same meaning as 4 |

Some host schedulers special-case `75` (e.g. `cron(8)` honoring
`MAILTO` thresholds), so `4` and `75` both map to "transient
contention" and operators are free to use either.

### Observability

The shim emits structured logs to stdout (JSON via stdlib `log/slog`
with `JSONHandler`) and errors to stderr. Every log line carries
`app`, `job`, `run_id`. v1 wires these additional emitters:

- Under K8s (`KUBERNETES_SERVICE_HOST` env set), terminal outcomes
  also post K8s `Event` records.
- Under systemd (`INVOCATION_ID` env set), structured fields are
  emitted via journald.

Apps logging at the receiver side should include the inbound
`X-Cron-Run-Id` so a single fire can be traced end-to-end.

## CLI

`cronix` is a single binary with subcommands. The reconciler-side surface
is operator-friendly: idempotent `apply`, dry-run `plan`, drift detection,
read-only `list` and `validate`. The trigger shim is the same binary's
`trigger` subcommand.

### Subcommands (v1)

| Command | Purpose |
|---|---|
| `cronix init` | Interactive scaffold for a new operator config. |
| `cronix validate <source>` | Lint a manifest from a file path or signed HTTPS URL. No side effects. |
| `cronix plan` (alias `diff`) | Show what `apply` would change. Equivalent to `apply --dry-run`. |
| `cronix apply` | Reconcile a manifest against the host scheduler. Writes per-job spec files for the trigger shim. |
| `cronix list` | List cronix-owned entries currently installed in the backend. |
| `cronix show <app>.<name>` | Detailed view of one owned entry (state, schedule, ownership marker). |
| `cronix drift` | Report entries whose installed state diverges from the manifest. With `--exit-on-drift`, exits 5 when drift is detected. |
| `cronix global-status` | Cross-backend snapshot — owned / in-sync / drifted counts per backend. |
| `cronix prune` | Remove all owned entries (with confirmation; `--yes` to skip). |
| `cronix history <app>.<name>` | Aggregated backend-native run records (journalctl / kubectl / CloudWatch). |
| `cronix trigger <app>.<job>` | Per-fire executor invoked by the host scheduler. |
| `cronix version` | Version, build, and target platform info. |
| `cronix completion <bash\|zsh\|fish\|powershell>` | Emit a shell completion script. |

### Cross-cutting behavior

- **Output formats**: `--output table` (default for TTY) or `--output json`
  (stable shape — CI integration relies on it).
- **Color**: auto-detected from TTY; `NO_COLOR` env honored.
- **Manifest source**: file path (`./manifest.json` or `/abs/path`),
  `file://`, `https://`, or `http://localhost`/`127.0.0.1` for dev.
  HTTPS sources require `--secret-ref` (one or more) for the signed GET.
- **Backend selection**: `--backend crontab|systemd-timer|kubernetes`.
  In v1 `crontab` and `aws-scheduler` support the full reconciliation
  cycle; `systemd-timer` and `kubernetes` are render-only (live
  reconciliation deferred to a follow-up).
- **Trigger spec writeout**: `cronix apply --spec-dir /etc/cronix/jobs`
  writes one `<app>.<job>.json` per job into the shim's spec directory.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | generic error (validation failure, fetch failure, etc.) |
| 2 | usage / config error (cobra surface) |
| 3 | (reserved for backend unreachable) |
| 4 | (reserved for auth failure on manifest fetch) |
| 5 | drift detected (only when `drift --exit-on-drift` is set) |

The trigger shim has its own exit-code map (see §Trigger Shim Behavior
in this RFC); the two are kept disjoint where overlap would be confusing.

### Quickstart

```bash
# 1. Validate a local manifest.
cronix validate ./examples/hand-rolled/manifest.json

# 2. Show what apply would change.
cronix plan --manifest ./manifest.json --backend crontab \
  --crontab-path /etc/crontab --trigger-bin /usr/local/bin/cronix \
  --secret-ref env:CRON_SECRET

# 3. Reconcile.
cronix apply --manifest ./manifest.json --backend crontab \
  --crontab-path /etc/crontab --trigger-bin /usr/local/bin/cronix \
  --spec-dir /etc/cronix/jobs --secret-ref env:CRON_SECRET

# 4. Inspect.
cronix list --backend crontab --crontab-path /etc/crontab

# 5. Drift check from CI.
cronix drift --manifest ./manifest.json --backend crontab \
  --crontab-path /etc/crontab --exit-on-drift
```

## Deployment

### Bare-metal — crontab

1. Install the cronix binary at a stable absolute path.
   ```bash
   go install github.com/awbx/cronix/go/cmd/cronix@latest
   sudo cp "$(go env GOPATH)/bin/cronix" /usr/local/bin/cronix
   sudo mkdir -p /etc/cronix/jobs /var/lock/cronix
   ```
2. Set the secrets the reconciler and the app share — typically as
   environment variables under your secret manager.
3. From CI on every deploy:
   ```bash
   cronix apply \
     --manifest https://billing.internal/.well-known/cron-manifest \
     --backend crontab \
     --crontab-path /etc/crontab \
     --trigger-bin /usr/local/bin/cronix \
     --spec-dir /etc/cronix/jobs \
     --secret-ref env:CRON_SECRET_V2 --secret-ref env:CRON_SECRET_V1
   ```

   `apply` is idempotent — no host-scheduler reload, no log churn when
   nothing has changed (D-027).

See `docs/src/content/docs/backends/crontab.md` for the per-backend setup guide.

### Bare-metal — systemd-timer (v1)

The `systemd-timer` backend ships with `Validate` and unit-file
rendering in v1; live reconciliation is deferred to a follow-up
version. Operators using systemd today can render the unit pair via the
SDK and apply with `systemctl daemon-reload && systemctl enable --now`.

See `docs/src/content/docs/backends/systemd.md`.

### Docker

The cronix image at `ghcr.io/awbx/cronix:<version>` is `FROM
gcr.io/distroless/static-debian12:nonroot`, multi-arch
(amd64 + arm64), under 30 MB. Built and pushed by the GoReleaser
release workflow.

```bash
docker run --rm ghcr.io/awbx/cronix:latest version
```

### Kubernetes

The `kubernetes` backend ships with `Validate` and CronJob+ConfigMap
YAML rendering in v1; live `client-go` reconciliation is deferred to a
follow-up version. The pre-alpha Helm chart at
`deploy/helm/cronix/` provisions the cronix image, ServiceAccount,
RBAC, and an in-cluster `cronix apply` CronJob that reconciles a
named manifest URL on a schedule.

See `docs/src/content/docs/backends/kubernetes.md`.

### Distribution channels

| Channel | Status |
|---|---|
| `go install github.com/awbx/cronix/go/cmd/cronix@latest` | works once the repo is published |
| GitHub Releases (Linux/macOS/Windows tarballs+zip, signed checksums) | wired via GoReleaser; first tag pending |
| Docker image (`ghcr.io/awbx/cronix:<version>`) | wired via GoReleaser; pushed on tag |
| `npm install @awbx/cronix-sdk` | wired via Changesets; first publish pending |
| Homebrew tap | follow-up |
| deb / rpm | follow-up |

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
  `systemd-analyze calendar`.
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

### v1.0.0-rc.1 — 2026-05

The on-the-wire contract is frozen. Every protocol surface is specified
in this document and exercised by the conformance vectors at
`spec/manifest-vectors.json` (29 cases) and `spec/auth-vectors.json` (35
cases). The TypeScript SDK (`@awbx/cronix-sdk`) and the Go reference
(`internal/manifest`, `internal/auth`) pass both vector suites byte-for-
byte; CI gates against any regression.

**What v1 covers:**

- **Manifest** — JSON shape, JSON Schema (`spec/manifest.schema.json`),
  normalization rules, schedule syntax (POSIX cron + `@hourly` /
  `@daily` / `@weekly` aliases + IANA timezone).
- **Authentication** — HMAC-SHA256 over `t.METHOD.path.body`,
  `cronix-signature: t=…,v1=…` header, ±5 min replay window,
  constant-time verification, multi-secret rotation. CI greps for
  loose-comparison adjacent to HMAC values in both languages.
- **SDK contract** — `createCron(...)` exposing `register` / `manifest`
  / `verify`. Framework-agnostic core with thin adapters for Hono,
  Express, Fastify, Koa, and Nest.
- **Reconciliation model** — ownership tracking per backend (D-026),
  state table, idempotency contract (D-027), drift semantics, and the
  delete-then-update-then-create apply ordering.
- **Backends** — `crontab` (full lifecycle, owned-block markers),
  `aws-scheduler` (full lifecycle via EventBridge Scheduler),
  `systemd-timer` (`Validate` + unit-file render in v1; live
  reconciliation deferred), `kubernetes` (`Validate` + CronJob YAML
  render in v1; live `client-go` reconciliation deferred).
- **Trigger shim** — per-fire lifecycle: spec load, secret resolve,
  lock acquire (`flock` or `redis`), signed HTTP with timeout /
  retries / backoff, panic recovery, structured JSON logs, dedicated
  exit-code map.
- **CLI** — `init`, `validate`, `plan` / `diff`, `apply`, `list`,
  `show`, `drift`, `global-status`, `prune`, `history`, `trigger`,
  `version`, `completion`. Stable `-o json` output for CI integration.
- **Distribution** — Homebrew tap, deb / rpm / apk packages, Docker
  image, npm SDK, pre-alpha Helm chart at `deploy/helm/cronix/`.

**Deferred to follow-up versions:** live `client-go` integration for
the `kubernetes` backend, live `systemctl` / `journalctl` shell-out for
the `systemd-timer` backend, persistent run-history for `cronix
history`, the SIGTERM-the-previous-holder path for the `Replace`
concurrency policy, and graduating the Helm chart from pre-alpha.

## Open Questions

See `OPEN_QUESTIONS.md`.
