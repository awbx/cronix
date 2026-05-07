# Locked Decisions

Decisions in this file are settled. To revisit one, raise it as an entry in
`OPEN_QUESTIONS.md` first.

---

## D-001: Discovery endpoint
Date: 2026-05-04
Status: Locked

Decision: The manifest is served at `GET /.well-known/cron-manifest`.
Rationale: `.well-known` is the IETF standard location for protocol-level
discovery resources (RFC 8615). It avoids collision with application routes.
Alternatives considered: `/api/cron-manifest`, `/_cron`. Rejected — too
opinionated about an app's URL layout.

---

## D-002: Manifest top-level shape
Date: 2026-05-04
Status: Locked

Decision: `{ version: 1, app: "<id>", jobs: [...] }`.
Rationale: An explicit `version` field decouples on-the-wire schema from
implementation versions. `app` namespaces jobs across multi-tenant deployments.
Alternatives considered: a flat array of jobs. Rejected — leaves no room for
top-level fields and forces awkward versioning.

---

## D-003: Job name format
Date: 2026-05-04
Status: Locked

Decision: `^[a-z][a-z0-9-]{0,62}$` (kebab-case, ≤ 63 chars).
Rationale: Compatible with K8s resource name length (63), systemd unit name
limits, and crontab line readability.
Alternatives considered: snake_case, no length limit. Rejected — would
require name munging at K8s and systemd boundaries.

---

## D-004: Schedule syntax
Date: 2026-05-04
Status: Locked

Decision: 5-field cron expressions plus the shortcuts `@hourly`, `@daily`,
`@weekly`, `@monthly`, `@yearly`, and `@every <duration>`. No seconds in v1.
Rationale: 5-field cron is the lingua franca; every backend speaks it. Sub-
minute resolution is impossible on crontab and creates inconsistent fidelity.
Alternatives considered: 6-field cron with seconds. Deferred — see Limitation 3.

---

## D-005: Multiple schedules per job
Date: 2026-05-04
Status: Locked

Decision: `schedules: [...]` array. Single `schedule` field is sugar for
`schedules: [<value>]`.
Rationale: Real cron jobs often want disjoint windows ("hourly during the
day, every 5 minutes during business hours"). Modeling this as N entries
is cleaner than expressive cron-expression hacks.

---

## D-006: Default timezone
Date: 2026-05-04
Status: Locked

Decision: UTC. Per-job override allowed.
Rationale: UTC is unambiguous. Per-job override supports business-hour
schedules without forcing the whole app into a regional TZ.

---

## D-007: Default HTTP method
Date: 2026-05-04
Status: Locked

Decision: POST.
Rationale: Triggers are state-changing by nature.
Alternatives considered: GET. Rejected — encourages idempotency confusion
("but it was a GET, why did it write?").

---

## D-008: Success criterion
Date: 2026-05-04
Status: Locked

Decision: Any 2xx response.
Rationale: Universal, framework-neutral, no body parsing required.

---

## D-009: Concurrency policies
Date: 2026-05-04
Status: Locked

Decision: `Allow`, `Forbid`, `Replace` (Kubernetes vocabulary). Default `Forbid`.
Rationale: Reuses well-known semantics. `Forbid` default protects naive jobs
from overlap; users must opt into `Allow`.

---

## D-010: Concurrency scope
Date: 2026-05-04
Status: Locked

Decision: `host` (default) or `global`. `global` requires a configured
distributed lock backend.
Rationale: Most users run from a single host or a single CI runner; `host`
scope avoids the need to configure Redis. `global` is opt-in.

---

## D-011: Timeout
Date: 2026-05-04
Status: Locked

Decision: Optional in manifest, default 60s, hard ceiling 600s. Cannot be
disabled.
Rationale: An unbounded request is a leak waiting to happen. The shim must
release its lock; 600s is enough for almost any sane handler.

---

## D-012: Retries default
Date: 2026-05-04
Status: Locked

Decision: 3 attempts within a single fire, exponential backoff 1s → 60s.
Rationale: Within-fire retries handle transient network blips without
re-firing the schedule.

---

## D-013: Retry across fires
Date: 2026-05-04
Status: Locked

Decision: Not supported. Apps must be idempotent.
Rationale: Cross-fire retry would require a state store, which v1 does not
have. Apps already need idempotency for at-least-once delivery.

---

## D-014: Auth
Date: 2026-05-04
Status: Locked

Decision: HMAC-SHA256, Stripe-shaped, mandatory for both manifest and trigger.
Rationale: HMAC-SHA256 is widely understood, simple to implement in any
language, and well-trodden in webhook signing.

---

## D-015: Signed payload
Date: 2026-05-04
Status: Locked

Decision: `<unix_seconds>.<METHOD>.<path>.<body>` (body is empty bytes for GET).
Rationale: Method and path inclusion prevent replay attacks that swap one
endpoint for another. Period delimiter is unambiguous because timestamp is
all-digits and method is uppercase letters.

---

## D-016: Signature header
Date: 2026-05-04
Status: Locked

Decision: `X-Cron-Signature: t=<unix_seconds>,v1=<hex_sha256>`.
Rationale: Mirrors Stripe's shape. `v1=` prefix supports future algorithm
upgrades without changing header name.

---

## D-017: Replay window
Date: 2026-05-04
Status: Locked

Decision: 300s default, receiver-configurable.
Rationale: Tolerates clock skew without leaving a wide replay surface.

---

## D-018: Comparison
Date: 2026-05-04
Status: Locked

Decision: Constant-time required (`crypto/subtle.ConstantTimeCompare` in Go;
manual XOR-bitwise loop in TypeScript Web Crypto).
Rationale: Standard timing-attack mitigation. CI greps for `===`/`bytes.Equal`
on HMAC values.

---

## D-019: Multiple acceptable secrets
Date: 2026-05-04
Status: Locked

Decision: Manifest may reference multiple secrets (`secret_refs: [...]`);
verifier accepts the first match.
Rationale: Enables zero-downtime key rotation.

---

## D-020: Run-id format
Date: 2026-05-04
Status: Locked

Decision: UUIDv7 (time-ordered). Constant across retries within a single fire.
Rationale: Time-orderable run-ids make handler-side dedup and log
correlation easier than v4 randoms.

---

## D-021: Injected headers
Date: 2026-05-04
Status: Locked

Decision: `X-Cron-Run-Id`, `X-Cron-Schedule-Name`, `X-Cron-Fire-Time`
(intended), `X-Cron-Fire-Time-Actual` (when shim ran), `X-Cron-Attempt`,
`X-Cron-Previous-Success-Time`.
Rationale: Each carries information apps want for dedup, logging, and
late-fire detection without polluting the body.

---

## D-022: v1 backends
Date: 2026-05-04
Status: Locked

Decision: `crontab`, `systemd-timer`, `kubernetes`. Others are out of scope
for v1.
Rationale: These three cover the vast majority of self-hosted deployments.
Backend adapter contract is stable; community contributions are possible
after v1.

---

## D-023: v1 lock backends
Date: 2026-05-04
Status: Locked

Decision: `redis` only (for `global` scope). `flock` for `host` scope.
`k8s-lease`, `postgres`, `etcd` deferred.
Rationale: Pluggable interface present from day one; Redis covers most
real-world `global` needs.

---

## D-024: Spec passing to trigger
Date: 2026-05-04
Status: Locked

Decision: Local job-spec file written by `cronix apply` at
`/etc/cronix/jobs/<app>.<job>.json` (bare-metal) or ConfigMap mount (K8s).
Host-scheduler entry only invokes `cronix trigger <app>.<job>`.
Rationale: Keeps host-scheduler entries short and immutable; avoids smuggling
URLs and secrets through crontab/systemd command lines.

---

## D-025: Manifest source
Date: 2026-05-04
Status: Locked

Decision: URL (HTTPS, HMAC-signed) or local file
(`--manifest=file:./manifest.json`).
Rationale: URL flow is the production case. File flow is the CI dry-run case.

---

## D-026: Reconciler ownership tracking
Date: 2026-05-04
Status: Locked

Decision: Per-backend marker:
- crontab: `# cronix:owned app=<app> job=<name> hash=<sha>` comment line
- systemd: naming convention `cronix-<app>-<name>.{timer,service}`
- kubernetes: labels `cronix.dev/managed=true`, `cronix.dev/app=<app>`,
  `cronix.dev/job=<job>`, `cronix.dev/hash=<sha>`
`cronix` never modifies entries it did not create.
Rationale: Co-existence with manually-managed cron entries is required;
operators must trust that `apply` will not touch their hand-rolled lines.

---

## D-027: Idempotency
Date: 2026-05-04
Status: Locked

Decision: `cronix apply` with no manifest changes is a complete no-op
(no host-scheduler reload, no log churn). Hash-based change detection.
Rationale: A reconciler that is loud when nothing changed is a reconciler
nobody runs from CI.

---

## D-028: Synthesis-first principle
Date: 2026-05-04
Status: Locked

Decision: When a policy can be enforced either by the host scheduler natively
or by the trigger shim, prefer the shim. The host scheduler's job is *when to
fire*. The shim's job is *everything that happens at and after the fire*.
Rationale: Synthesis-first keeps backend adapters thin and behavior uniform
across crontab, systemd, and Kubernetes.

---

## D-029: Polyglot monorepo layout
Date: 2026-05-04
Status: Locked

Decision: The repo uses a polyglot top-level: `spec/` (language-neutral),
`ts/` (TypeScript pnpm workspace), `go/` (Go module). Future SDKs in other
languages each get their own top-level directory (`py/`, `rb/`, …). The Go
module path is `github.com/awbx/cronix/go` (not `github.com/awbx/cronix`).
Rationale: Symmetry across languages. An earlier draft placed the Go
module at the repo root for goreleaser / `go install` ergonomics, but that
asymmetry worsens as more languages join. The slightly longer install path
(`go install github.com/awbx/cronix/go/cmd/cronix@latest`) is an acceptable
cost.
Alternatives considered: Go at repo root with TypeScript under `packages/`
(the earlier draft); both languages under a single `src/` tree (rejected —
fights both ecosystems' tooling defaults); Bazel/Pants (rejected — overkill
for v1).

## D-030: SDK extension points are non-normative
Date: 2026-05-06
Status: Locked

Decision: SDKs MAY ship the affordances listed in RFC §SDK Contract /
Extension points (skip-verify, hooks, errorResponse, pluggable logger,
replay-window override, per-job overrides, standalone verify utilities).
None of them participate in the on-the-wire contract. Whether or not an
SDK exposes them, two conformant SDKs MUST agree byte-for-byte on every
manifest and signing case in `spec/manifest-vectors.json` and
`spec/auth-vectors.json`. The conformance vectors do NOT exercise the
extension points.
Rationale: We want SDKs to compete on DX without forking the wire.
Apps that integrate against the standalone verify utilities or rely on
hooks for observability stay portable to any SDK that ships them; apps
that use only the four core operations are guaranteed portable.
Alternatives considered: bake all extension points into the conformance
suite (rejected — locks every implementation into the same DX shape);
exclude them from the spec (rejected — apps end up depending on
undocumented surface, which makes future SDKs harder to ship).

## D-031: skipVerify is a footgun, must be loud
Date: 2026-05-06
Status: Locked

Decision: When an SDK exposes a "skip the HMAC verify" mode, the option
MUST be loud — opt-in, named so it cannot be turned on by accident
(e.g. `skipVerify: true`, not `auth: false`), and the SDK MUST emit a
one-line warning at instance construction when the flag is set. Where
the language permits, the JobContext SHOULD also expose
`ctx.unverified === true` so handlers can branch on it. The wire format
is unchanged: `cronix trigger` still signs outgoing requests; the SDK
simply does not verify them.
Rationale: skipVerify is the right escape hatch for trust-delegated
environments (mTLS, internal Kubernetes service, dev) but is a pure
removal of authentication if used on the public internet. Loudness in
the API surface and runtime trace is the only mitigation we can ship.
Alternatives considered: refuse to ship skipVerify at all (rejected —
apps that need it route around cronix entirely); environment-flag
gated (rejected — env flags drift from the code that pinned them).

## D-032: hooks are fire-and-forget
Date: 2026-05-06
Status: Locked

Decision: Any SDK hook (`onVerifyFailure`, `onTriggerStart`,
`onTriggerSuccess`, `onTriggerError`, `onManifestRequest`) MUST be
called fire-and-forget. Errors thrown inside a hook MUST be caught by
the SDK and routed to the configured logger; they MUST NOT propagate
to the response, MUST NOT short-circuit the verify result, and MUST
NOT cancel the handler.
Rationale: Hooks are observability seams. Letting them influence the
request shape would create a second, undocumented authentication
surface — exactly what cronix's signed-payload contract exists to
prevent. Hooks that need to gate behaviour can return a value, but
the SDK MUST ignore it.
Alternatives considered: allow hooks to short-circuit (rejected —
conflates observability with authorization); make hooks await each
other in sequence (rejected — slow path, easy to introduce ordering
bugs).

## D-033: per-job overrides supersede instance defaults
Date: 2026-05-06
Status: Locked

Decision: When a job definition specifies `skipVerify`, `secret`,
`replayWindowSeconds`, `enabled`, or `tags`, those values supersede
the corresponding instance-level option for that job. Per-job
overrides DO NOT change the on-the-wire wire format — they are
additional declarative metadata in the job entry plus per-job runtime
behaviour the SDK honours locally. Per-job overrides take precedence
in this order: per-job > instance-level > SDK default.
Rationale: A common ask is "let me skip-verify just this one health-
check endpoint" or "let me give the high-stakes job its own secret".
Forcing those into a separate cron instance is workable but worse DX
than a per-job flag.
Alternatives considered: only instance-level overrides (rejected —
forces apps to instantiate multiple crons for trivial differences);
inheritance via env vars (rejected — adds a second declaration surface
that drifts from the manifest).

## D-034: replay window minimum 30 seconds
Date: 2026-05-06
Status: Locked

Decision: The configurable replay window (`replayWindowSeconds`,
applied at instance level or per job) MUST be ≥ 30 seconds. Values
below 30 cause legitimate requests to be rejected under common clock
skew (NTP-synced hosts routinely drift 1–5 seconds; cloud VMs can
drift more). SDKs MUST reject `replayWindowSeconds < 30` at instance
construction or job registration with a clear error.
Rationale: §Authentication's default of 300s reflects industry practice
(Stripe uses 300s, GitHub webhooks 5 minutes). Apps with stricter
freshness requirements can tighten, but a minimum guards against the
common foot-gun of "set it to 1 second for safety," which then breaks
in production.
Alternatives considered: hard-code 300s (rejected — no override is too
restrictive); allow any value ≥ 0 (rejected — trivially exposes the
foot-gun).

## D-035: standalone verify utilities are required
Date: 2026-05-06
Status: Locked

Decision: SDKs SHOULD export their verify primitives as **standalone
functions** alongside the per-instance methods. The minimum set:
`verifyTriggerRequest`, `verifyManifestRequest`, `signRequest`,
`parseSignatureHeader`, `canonicalSignedString`, and a constant-time
byte equality helper. These functions accept the same inputs as the
instance methods and return the same `{ ok, ... }` result shape, but
do not require constructing a cron instance.
Rationale: Several integration patterns benefit from verify-without-
register: testing flows (sign a payload to inject into a fixture); a
custom router that owns `/api/v1/scheduled/*` already and only needs
the verdict; a polyglot service where another framework owns routing
and only the verify primitive is needed.
Alternatives considered: instance-only verify (rejected — forces apps
to declare every job they intend to receive triggers for, even when
they're using a separate routing layer); leak the auth.ts internals
without a typed wrapper (rejected — pushes the request normalization
burden onto the user).
