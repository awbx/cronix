# cronctl — Implementation Plan (v2)

> **Audience:** Claude Code, executing this plan in a fresh repository.
> **Project:** `cronctl` — *cron jobs as code*. Apps declare scheduled work in their own code; `cronctl` reconciles those declarations against the host's native scheduler.
> **TypeScript reference repo:** `~/open-sources/fhir-dsl` — mirror its monorepo layout, tsconfig structure, package.json shape, build scripts, lint/format setup, and changeset workflow. **Read it first before scaffolding the TS side.**
> **Go reference repo:** none specified — default to widely-accepted Go conventions (cobra, slog, goreleaser, `cmd/`+`internal/` layout).

---

## 1. The pitch

**Cron jobs as code.** Today, the schedule for a job lives somewhere different from the code that handles it — in a UI, an EventBridge rule, a hand-edited crontab, a separate YAML repo. Changes require coordinating two places. Drift is invisible. Reviewers see the handler change but miss the schedule change.

`cronctl` puts the schedule next to the handler. The application is the source of truth for its own schedules via a manifest endpoint. `cronctl apply` reconciles that manifest against whatever scheduler the host provides — `crontab`, `systemd-timer`, Kubernetes — installing, updating, or removing entries as needed. The host's native scheduler does the firing. A small Go binary (`cronctl trigger`) handles HMAC signing, concurrency locks, timeouts, and retries on every fire.

The protocol is the product. The reconciler and SDK are reference implementations.

---

## 2. Architecture

```
                 ┌─────────────────────────────────────┐
                 │  app                                │
                 │  GET /.well-known/cron-manifest     │
                 │  POST /api/v1/scheduled/<name>      │
                 └──────────┬──────────────────────────┘
                            │
        (1) reads manifest  │
        ┌───────────────────┘
        │
        ▼
  ┌──────────────┐
  │ cronctl apply│   (2) installs/updates/deletes entries
  │ (Go binary)  ├─────────────────────────────────┐
  └──────────────┘                                 │
                                                   ▼
                                       ┌────────────────────────┐
                                       │  host scheduler        │
                                       │  (crontab / systemd /  │
                                       │   kubernetes CronJob)  │
                                       └────────┬───────────────┘
                                                │ invokes on schedule
                                                ▼
                                       ┌────────────────────────┐
                                       │ cronctl trigger        │
                                       │ <app>.<name>           │
                                       │  • reads job spec      │
                                       │  • acquires lock       │
                                       │  • signs HMAC          │
                                       │  • fires HTTP request  │
                                       │  • applies timeout     │
                                       │  • retries on 5xx/net  │
                                       └────────┬───────────────┘
                                                │ POST + signature
                                                ▼
                                       ┌────────────────────────┐
                                       │  app handler           │
                                       │  (verifies signature,  │
                                       │   dedupes by run-id)   │
                                       └────────────────────────┘
```

### Component summary

| Component | Language | Distribution | Role |
|---|---|---|---|
| `cronctl` (single binary, subcommands) | Go | static binary + Docker image | Reconciler (`apply`, `diff`, `list`, `prune`, `drift`, `history`, `validate`, `init`) and trigger executor (`trigger`) |
| `@cronctl/sdk` | TypeScript | npm | App-side SDK: register jobs, serve manifest, verify signatures |
| `cronsdk` (Go) | Go | go module | Minimal Go SDK: signature verification only, repackaged from core code |
| `packages/spec` | language-neutral | git only | RFC, JSON Schema, conformance vectors |

### Locked design decisions

These are settled. Do not relitigate without raising as an OPEN_QUESTION first.

| # | Decision | Value |
|---|---|---|
| D-001 | Discovery endpoint | `GET /.well-known/cron-manifest` |
| D-002 | Manifest top-level shape | `{ version: 1, app: "<id>", jobs: [...] }` |
| D-003 | Job name format | `^[a-z][a-z0-9-]{0,62}$` (kebab-case) |
| D-004 | Schedule syntax | 5-field cron + shortcuts (`@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`, `@every <duration>`). No seconds in v1. |
| D-005 | Multiple schedules per job | Supported via `schedules: [...]` array. Single `schedule` field is sugar for `schedules: [<value>]`. |
| D-006 | Default timezone | UTC. Per-job override allowed. |
| D-007 | Default HTTP method | POST |
| D-008 | Success criterion | Any 2xx |
| D-009 | Concurrency policies | `Allow` / `Forbid` / `Replace` (K8s vocabulary). Default `Forbid`. |
| D-010 | Concurrency scope | `host` (default) or `global`. `global` requires a configured distributed lock backend. |
| D-011 | Timeout | Optional in manifest, default 60s, hard ceiling 600s. Cannot be disabled. |
| D-012 | Retries default | 3 attempts within a single fire, exponential backoff 1s → 60s. |
| D-013 | Retry across fires | Not supported. Apps must be idempotent. |
| D-014 | Auth | HMAC-SHA256, Stripe-shaped, mandatory for both manifest and trigger. |
| D-015 | Signed payload | `<unix_seconds>.<METHOD>.<path>.<body>` (body is empty bytes for GET) |
| D-016 | Signature header | `X-Cron-Signature: t=<unix_seconds>,v1=<hex_sha256>` |
| D-017 | Replay window | 300s default, receiver-configurable |
| D-018 | Comparison | Constant-time required (`crypto/subtle.ConstantTimeCompare` in Go, `crypto.timingSafeEqual` equivalent in TS) |
| D-019 | Multiple acceptable secrets | Manifest may reference multiple secrets (`secret_refs: [...]`); verifier accepts the first match. Enables zero-downtime rotation. |
| D-020 | Run-id format | UUIDv7 (time-ordered). Constant across retries within a single fire. |
| D-021 | Injected headers | `X-Cron-Run-Id`, `X-Cron-Schedule-Name`, `X-Cron-Fire-Time` (intended), `X-Cron-Fire-Time-Actual` (when shim ran), `X-Cron-Attempt`, `X-Cron-Previous-Success-Time` |
| D-022 | v1 backends | `crontab`, `systemd-timer`, `kubernetes`. Others are out of scope for v1. |
| D-023 | v1 lock backends | `redis` (only). Pluggable interface; `k8s-lease`, `postgres`, `etcd` deferred. |
| D-024 | Spec-passing to trigger | Local job spec file written by `apply` at `/etc/cronctl/jobs/<app>.<job>.json` (bare-metal) or ConfigMap mount (K8s). Host scheduler entry only invokes `cronctl trigger <app>.<job>`. |
| D-025 | Manifest source | URL (HTTPS, HMAC-signed) or local file (`--manifest=file:./manifest.json`). |
| D-026 | Reconciler ownership tracking | Per-backend marker — comment line for crontab, naming convention for systemd, K8s labels (`cronctl.dev/managed=true`, `cronctl.dev/app=...`, `cronctl.dev/job=...`, `cronctl.dev/hash=...`). `cronctl` never modifies entries it did not create. |
| D-027 | Idempotency | `cronctl apply` with no manifest changes is a complete no-op (no host-scheduler reload, no log churn). Hash-based change detection. |
| D-028 | Synthesis-first principle | When a policy can be enforced either by the host scheduler natively or by the trigger shim, prefer the shim. The host scheduler's job is *when to fire*. The shim's job is *everything that happens at and after the fire*. |

---

## 3. Limitations

These are documented in the RFC under a `Limitations` section. They are not bugs.

### Intrinsic (will not change in v2 either)

1. **App must be reachable at fire time.** No queueing, no buffering. If the app is offline, the fire fails and is logged; the next scheduled run is the only retry.
2. **At-least-once delivery.** Network errors and host-scheduler quirks mean a job *can* fire twice for the same intended fire-time. Run-id (UUIDv7, constant across retry attempts) plus app-side dedup is the answer. The shim provides the primitive; apps provide the discipline.
3. **1-minute resolution floor.** Cron's 5-field syntax can't express sub-minute. Sub-minute scheduling is out of scope for v1. systemd-timer-only sub-minute support may come in v1.1.
4. **No fan-out.** One scheduled job fires one HTTP request. Per-tenant or per-region work is the handler's responsibility.
5. **Timeout cancels the connection, not the app's work.** The shim closes the connection at the timeout. Apps that want true timeout must check `ctx.Done()` (or equivalent) on their side.

### v1 scope choices (addressed in later versions)

6. **No HA reconciler.** `cronctl apply` is a single-host operation. Concurrent applies are *safe* (file locks, K8s optimistic concurrency) but not load-balanced. Operators run from CI; CI serializes naturally.
7. **No central history database.** History is read on-demand from backend-native sources (journald, K8s Events, Pod logs). `cronctl history` aggregates them; no separate store.
8. **Limited backend coverage in v1.** crontab, systemd-timer, kubernetes only. launchd, Windows, Docker (no native cron), AWS EventBridge, GCP Cloud Scheduler, Cloudflare Cron, Vercel Cron all deferred. Backend adapter contract is stable; community contributions become possible after v1.
9. **Lock backends in v1 are Redis-only.** Pluggable interface present; K8s Lease, Postgres, etcd are fast-follows.
10. **TypeScript SDK is the only "full" SDK in v1.** Go SDK is signature-verification-only (repackaged from core). Python/full Go SDK in v2. Conformance vectors make porting mechanical.

### When `cronctl` is the wrong tool

If you need: at-most-once delivery, sub-minute scheduling, queueing for offline apps, fan-out parameterization, workflow chaining, or DAG orchestration — use Temporal, BullMQ, Sidekiq, Airflow, or a workflow engine. Not this.

---

## 4. Non-goals (v1)

- Long-running scheduler daemon
- Persistent state store of any kind
- Run history database
- Workflow orchestration / job chaining / DAGs
- Built-in web UI
- Plugin system with dynamic loading (backends are compiled in)
- One-shot `run-at` (specific timestamp) jobs
- CRD-based K8s deployment (Helm chart is enough)

---

## 5. Tech stack

### TypeScript side

Mirror `~/open-sources/fhir-dsl` for everything not specified here. **Read its root config files before writing your own.**

- Node.js >= 20 LTS
- TypeScript, strict mode, `noUncheckedIndexedAccess: true`
- pnpm workspaces
- Build: matches fhir-dsl (likely `tsup` or `tsc`)
- Validation: `zod`. TypeScript types via `z.infer`. Single source of truth.
- JSON Schema: generated from Zod via `zod-to-json-schema`, committed at `packages/spec/manifest.schema.json`. CI checks for drift.
- Testing: `vitest`, co-located `*.test.ts`, `@vitest/coverage-v8`. 90%+ on auth/manifest, 80%+ elsewhere.
- Lint/format: matches fhir-dsl (likely Biome).
- Versioning: `changesets`.
- Commits: Conventional Commits.
- HTTP server (SDK): framework-agnostic core, adapters for Express, Fastify, Hono via subpath exports.
- Web Standards: SDK core uses Fetch API (`Request`, `Response`, `Uint8Array`, Web Crypto API) — runs unchanged on Node, Bun, Deno, Workers, Edge. No `node:*` imports in core. **Adapters** can use framework/runtime types.

### Go side

- Go 1.22+
- Module path: `github.com/<owner>/cronctl` (pick this once, never change)
- Layout: `cmd/cronctl/` for the binary entrypoint, `internal/` for everything not exported, `pkg/cronsdk/` for the public Go SDK
- CLI: `github.com/spf13/cobra`
- Config: `github.com/knadh/koanf` (lighter than Viper; YAML + env + flags)
- Logging: stdlib `log/slog`. JSON in non-TTY, pretty-printed in TTY (`tint` or roll your own).
- HTTP: stdlib `net/http` with explicit timeouts. Do not pull in third-party HTTP clients.
- UUID: `github.com/google/uuid` (v7 support).
- YAML: `gopkg.in/yaml.v3`.
- K8s: `k8s.io/client-go`.
- Redis: `github.com/redis/go-redis/v9`.
- Testing: stdlib `testing` + `github.com/stretchr/testify/require` for assertions. Fuzzing via `testing.F`.
- Build/release: `goreleaser` for cross-platform binaries, Docker images (multi-arch), Homebrew tap, deb/rpm packages, GitHub Releases.
- File locks: `github.com/gofrs/flock` (cross-platform).
- Cron parsing: `github.com/robfig/cron/v3`.
- Systemd: `github.com/coreos/go-systemd/v22` (journal output, unit file generation).

---

## 6. Repository layout

```
cronctl/
├── packages/                              # pnpm workspace (TS)
│   ├── spec/                              # Language-neutral spec
│   │   ├── RFC.md                         # The authoritative spec
│   │   ├── DECISIONS.md                   # Numbered locked decisions
│   │   ├── OPEN_QUESTIONS.md              # In-flight design questions
│   │   ├── manifest.schema.json           # JSON Schema (generated from Zod)
│   │   ├── manifest-vectors.json          # Manifest parsing conformance vectors
│   │   └── auth-vectors.json              # HMAC conformance test vectors
│   └── sdk/                               # @cronctl/sdk
│       └── src/
│           ├── core/                      # Web Standards core (no Node-isms)
│           │   ├── manifest.ts            # Zod schema, parser, normalizer
│           │   ├── auth.ts                # HMAC sign/verify (Web Crypto)
│           │   ├── headers.ts
│           │   ├── registry.ts            # cron.register(), buildManifest()
│           │   ├── handler.ts             # Framework-agnostic dispatcher
│           │   └── index.ts
│           ├── express/                   # Express adapter (subpath: @cronctl/sdk/express)
│           ├── fastify/                   # Fastify adapter
│           ├── hono/                      # Hono adapter
│           └── index.ts
├── go.mod                                 # Go module at repo root
├── go.sum
├── cmd/
│   └── cronctl/
│       └── main.go                        # Single binary entrypoint, dispatches subcommands
├── internal/                              # Go internal packages (not importable)
│   ├── manifest/                          # Parser, validator, normalizer (mirrors TS)
│   ├── auth/                              # HMAC sign/verify + replay window
│   ├── headers/                           # X-Cron-* constants
│   ├── backends/
│   │   ├── backend.go                     # Backend interface
│   │   ├── crontab/
│   │   ├── systemd/
│   │   └── kubernetes/
│   ├── locks/
│   │   ├── lock.go                        # Lock interface
│   │   ├── flock/                         # Local file lock (default for `host` scope)
│   │   └── redis/                         # Distributed lock (for `global` scope)
│   ├── trigger/                           # The per-fire executor
│   ├── reconcile/                         # Diff + apply orchestration
│   ├── history/                           # Backend-native log aggregation
│   ├── observability/                     # K8s Events, journald structured logging
│   ├── config/                            # Operator config schema, loader
│   └── cli/
│       └── commands/                      # One file per subcommand
├── pkg/
│   └── cronsdk/                           # Public Go SDK (signature verification)
│       ├── verify.go
│       └── doc.go
├── examples/
│   ├── express-app/                       # TS + Express
│   ├── fastify-app/                       # TS + Fastify
│   ├── hono-app/                          # TS + Hono (Bun + Cloudflare Workers ready)
│   ├── go-app/                            # Go app using cronsdk for verification
│   └── hand-rolled/                       # Pure HTTP, no SDK — proves the protocol
├── deploy/
│   ├── docker/
│   │   └── Dockerfile                     # Multi-arch image for K8s CronJob containers
│   └── helm/
│       └── cronctl/                       # Alpha Helm chart
├── .changeset/
├── .github/workflows/                     # CI: TS test, Go test, conformance, lint, release
├── .goreleaser.yaml
├── pnpm-workspace.yaml
├── package.json
├── tsconfig.base.json
├── biome.json (or eslint/prettier matching fhir-dsl)
└── README.md
```

The Go module lives at the repo root (not under `go/`) because `goreleaser`, `go install`, and Go tooling all work most smoothly that way. The TypeScript packages cohabit fine — pnpm doesn't care about `cmd/`, `internal/`, or `pkg/` directories.

---

## 7. RFC discipline

The RFC at `packages/spec/RFC.md` is the **authoritative document**. Code follows the RFC; the RFC does not document the code.

### Workflow per phase

1. Begin the phase by reading `RFC.md`, `DECISIONS.md`, `OPEN_QUESTIONS.md` start-to-end.
2. Implement the phase's deliverables.
3. **Before declaring the phase complete, update the RFC** with sections this phase introduces or clarifies. Move resolved questions out of OPEN_QUESTIONS into DECISIONS or the RFC body.
4. Add an entry to the RFC's `## Changelog` section: phase number, date, one-paragraph summary.
5. Run all tests, all linters, all type checks (TS and Go). They must pass.
6. Stop. Do not begin the next phase until prompted.

### RFC structure (build out across phases)

```
# RFC: cronctl — Cron Jobs as Code

## Status
## Summary                              (Phase 1)
## Motivation                            (Phase 1)
## Goals and Non-goals                   (Phase 1)
## Limitations                           (Phase 1)
## Terminology                           (Phase 1)
## The Manifest                          (Phase 1, refined later)
## Authentication                        (Phase 2)
## SDK Contract                          (Phase 3)
## Reconciliation Model                  (Phase 4)
## Backend Adapter Contract              (Phase 5)
## Backend Fidelity Matrix               (Phase 5)
## Trigger Shim Behavior                 (Phase 5)
## CLI                                   (Phase 6)
## Deployment                            (Phase 7)
## Alternatives Considered               (refined throughout)
## Prior Art                             (Phase 1)
## Changelog
## Open Questions
```

### DECISIONS.md format

```
## D-NNN: <short title>
Date: YYYY-MM-DD
Status: Locked

Decision: <what was decided>
Rationale: <why>
Alternatives considered: <briefly>
```

Pre-populate DECISIONS.md with **D-001 through D-028** from §2 above as part of Phase 0.

### OPEN_QUESTIONS.md format

```
## Q-NNN: <question>
Raised: YYYY-MM-DD
Phase: <where it surfaced>

Context: ...
Options:
  a) ...
  b) ...
Currently leaning: ...
```

When resolved, move to DECISIONS.md with the same number prefix (Q-007 → D-007).

---

## 8. Phases

Each phase: **Scope**, **Tasks**, **Acceptance**, **RFC updates**, **Anti-goals**.

### Phase 0 — Foundation

**Scope:** Repository scaffolding for both TS and Go. No product code.

**Tasks:**
- Read `~/open-sources/fhir-dsl` thoroughly. Note tsconfig, build, scripts, lint, changesets, CI.
- Initialize the repo with the layout in §6.
- TS side: `pnpm-workspace.yaml`, root `package.json`, `tsconfig.base.json`, lint/format config, vitest config, changesets — matching fhir-dsl.
- Go side: `go.mod` at repo root with module path `github.com/<owner>/cronctl`. `cmd/cronctl/main.go` with cobra root command and `version` subcommand only (placeholder). `golangci-lint` config. `goreleaser.yaml` with at least Linux amd64+arm64, macOS amd64+arm64, and Docker image targets.
- CI: GitHub Actions running TS typecheck/lint/test, Go vet/lint/test, schema-drift check (placeholder). Match fhir-dsl's CI patterns where applicable.
- Empty stubs for every package and `internal/` directory listed in §6.
- `RFC.md`, `DECISIONS.md` (pre-populated with D-001 through D-028 from §2), `OPEN_QUESTIONS.md` (empty).
- Root `README.md` with the §1 pitch and a "Pre-alpha — under active development" status banner.

**Acceptance:**
- `pnpm install && pnpm -r build && pnpm -r test && pnpm -r lint` clean.
- `go build ./... && go test ./... && go vet ./... && golangci-lint run` clean.
- `goreleaser build --snapshot --clean` produces binaries for all configured platforms.
- CI green on initial commit.

**RFC updates:**
- Set `Status: Draft`.
- Populate `Summary` with a one-paragraph version of §1.
- Pre-populate DECISIONS.md (D-001 through D-028).
- Add `## Changelog` entry: "Phase 0 — repository scaffolding."

**Anti-goals:**
- Do not write product code. Stubs only.
- Do not invent conventions; copy fhir-dsl on the TS side, follow standard Go layout on the Go side.

---

### Phase 1 — Spec and manifest types

**Scope:** Write the manifest specification. Implement parser/validator in TypeScript (Zod) and Go (mirror). No HMAC, no reconciler, no SDK shipping.

**Tasks:**

*TypeScript:*
- `packages/sdk/src/core/manifest.ts`: Zod schema for the manifest. Cover top level (`version`, `app`, `jobs`) and per-job (`name`, `schedule | schedules`, `timezone?`, `request: { method?, url, headers?, body? }`, `policy?: { concurrency?, concurrency_scope?, timeout_seconds?, retries? }`, `auth?: { secret_refs?: string[] }`).
- Validate cron expressions inside `.refine()` using `cron-parser`.
- Enforce job-name regex and per-app uniqueness.
- `parseManifest(input: unknown): Result<Manifest, ManifestError>` returning a discriminated-union result. No exceptions for validation failures.
- `applyDefaults(manifest: Manifest): NormalizedManifest` — fills every optional field with its documented default.
- Generate `manifest.schema.json` from Zod via `zod-to-json-schema`. Commit.

*Go:*
- `internal/manifest/`: parse + normalize the same shape. Use `encoding/json` + struct tags + an explicit `Validate()` method. Do **not** use `kin-openapi` or other JSON Schema runtime validators — write idiomatic Go validation.
- Conformance: a shared file `packages/spec/manifest-vectors.json` with valid + invalid manifests and expected normalization output. **Both** TS and Go parsers must agree on every vector.

*Spec:*
- Write the RFC sections listed below.

**Acceptance:**
- 100% of validation rules covered by unit tests (TS and Go).
- TS and Go parsers produce **byte-identical normalized output** for every vector in `manifest-vectors.json`. CI test enforces this.
- `manifest.schema.json` matches the Zod schema (CI drift check).
- A hand-written sample at `examples/hand-rolled/manifest.json` parses and normalizes successfully under both implementations.

**RFC updates:**
- Write `Summary`, `Motivation`, `Goals and Non-goals`, `Limitations`, `Terminology`, `Prior Art`, `The Manifest` sections. The Manifest section must include a complete field reference, defaults table, and worked examples (minimal job, multi-schedule job, fully-specified job).
- Reference `manifest-vectors.json` as the conformance suite.
- Add Changelog entry.

**Anti-goals:**
- No HMAC code yet.
- No SDK runtime (`register()`, manifest serving) yet — that's Phase 3.
- No HTTP code anywhere.

---

### Phase 2 — HMAC authentication (TS + Go)

**Scope:** Self-contained signing and verification in both languages. Both languages pass the same `auth-vectors.json`.

**Tasks:**

*Shared:*
- Author `packages/spec/auth-vectors.json` with **at least 30** test cases covering: happy path (GET no body, POST with JSON body, POST with empty body), expired timestamp, future timestamp outside skew, tampered body, tampered path, tampered method, malformed header, missing header, wrong algorithm tag (`v2=`), unicode body, body with embedded NULs, very large body (>1MB), multiple acceptable secrets where first matches, where second matches, where none match.

*TypeScript (`packages/sdk/src/core/auth.ts`):*
- `sign({ secret, method, path, body, timestamp? }): { header: string }` — produces `t=<ts>,v1=<hex>`.
- `verify({ secrets, method, path, body, header, now?, maxSkewSeconds? }): Result<void, VerifyError>` with discriminated errors (`MalformedHeader`, `StaleTimestamp`, `SignatureMismatch`).
- Web Crypto API only (`crypto.subtle.importKey`, `crypto.subtle.sign`). No `node:crypto`.
- Constant-time comparison: implement manually with bitwise XOR over equal-length buffers; do not assume `crypto.timingSafeEqual` is available.

*Go (`internal/auth/`):*
- `Sign(opts SignOptions) (header string, err error)`.
- `Verify(opts VerifyOptions) error` with sentinel errors (`ErrMalformedHeader`, `ErrStaleTimestamp`, `ErrSignatureMismatch`).
- Use `crypto/hmac`, `crypto/sha256`, `crypto/subtle.ConstantTimeCompare`.

*Headers:*
- Constants for `X-Cron-Signature`, `X-Cron-Run-Id`, `X-Cron-Schedule-Name`, `X-Cron-Fire-Time`, `X-Cron-Fire-Time-Actual`, `X-Cron-Attempt`, `X-Cron-Previous-Success-Time` in both TS and Go.

**Acceptance:**
- Every `auth-vectors.json` case passes in both TS and Go.
- Mutation testing: deliberately mutating any signed field causes verification to fail in both implementations.
- Lint check: no string `===` / `==` comparisons of HMAC values anywhere. Greppable rule in CI.
- TS works on Node 20+ AND on Bun (runtime test in CI).

**RFC updates:**
- Write the full `Authentication` section: threat model, signed-payload byte-exact construction, header format, replay window, multiple-secret rotation, implementation requirements (constant-time, NTP, key-storage guidance).
- Worked example showing the canonical string for one trigger and one manifest fetch.
- Reference `auth-vectors.json` as the conformance suite for any future SDK port.
- Add Changelog entry.

**Anti-goals:**
- HMAC-SHA256 only. No algorithm negotiation.
- Header format `t=<unix_seconds>,v1=<hex>`. No alternatives.
- No key rotation logic in code (that's an operator concern); the spec just supports multiple acceptable secrets.

---

### Phase 3 — TypeScript SDK

**Scope:** App-side SDK. Register jobs in code, serve the manifest, verify incoming triggers. Web Standards core, framework-specific adapters.

**Tasks:**

*Core (`packages/sdk/src/core/`):*
- `createCron({ app: string, secret: string | string[] | () => string | string[] })` returning:
  - `register(definition: JobDefinition): void`
  - `manifest(): NormalizedManifest`
  - `verifyRequest(req: { method, path, headers, body }): Result<JobContext, VerifyError>` — framework-agnostic, takes Web-Standard-shape inputs.
  - `dispatch(ctx: JobContext): Promise<HandlerResult>` — runs the registered handler.
- The core must work in any runtime that supports the Fetch API. No `node:*` imports.

*Adapters:*
- `packages/sdk/src/express/`: `cron.express()` returns an Express router mounting `GET /.well-known/cron-manifest` and `POST /api/v1/scheduled/:name`. Verifies signatures, returns 401 on failure, 200 on handler success, 5xx on handler error.
- `packages/sdk/src/fastify/`: same surface, idiomatic Fastify plugin.
- `packages/sdk/src/hono/`: same surface as Hono middleware. Works on Node, Bun, Workers.

*Examples:*
- `examples/express-app/`, `examples/fastify-app/`, `examples/hono-app/` — minimal apps with two registered jobs each.
- `examples/hand-rolled/` — no SDK, returns a static manifest from a small Hono server. Demonstrates the protocol is framework-free.

*Tests:*
- Unit tests for the core (registry, manifest builder, dispatcher).
- Integration tests booting each adapter, fetching manifest, posting valid + invalid signed triggers. Same test suite parameterized over adapters.

**Acceptance:**
- Every adapter passes the parameterized integration suite.
- SDK-built manifests round-trip through `parseManifest` cleanly.
- Examples run (`pnpm --filter <example> dev`) and respond to a curl-ed signed manifest fetch.
- Hono example runs unchanged on Node, Bun, and `wrangler dev` (Workers).

**RFC updates:**
- Write the `SDK Contract` section: the language-neutral behavioral contract any SDK in any language must implement (registry → manifest → verify → dispatch). Specify return shapes, error categories, dispatch lifecycle.
- Note that SDKs in other languages should pass `auth-vectors.json` and `manifest-vectors.json`; that is the bar.
- Add Changelog entry.

**Anti-goals:**
- Do not put secrets in the manifest.
- Do not lock the core to one framework.
- Do not auto-detect frameworks.

---

### Phase 4 — Go core libraries

**Scope:** Go-side libraries used by both the reconciler and the trigger shim. No CLI surface yet, no backend adapters yet.

**Tasks:**
- `internal/manifest/`: already done in Phase 1. Confirm it's still aligned with the spec.
- `internal/auth/`: already done in Phase 2.
- `internal/headers/`: already done in Phase 2.
- `internal/backends/backend.go`: define the `Backend` interface:
  ```go
  type Backend interface {
      Name() string
      List(ctx context.Context) ([]ManagedEntry, error)
      Create(ctx context.Context, job NormalizedJob) error
      Update(ctx context.Context, entry ManagedEntry, job NormalizedJob) error
      Delete(ctx context.Context, entry ManagedEntry) error
      Validate(job NormalizedJob) ValidationResult
      History(ctx context.Context, opts HistoryOpts) ([]HistoryEntry, error)
      Ensure(ctx context.Context) error
  }
  ```
- `internal/locks/lock.go`: define the `Lock` interface:
  ```go
  type Lock interface {
      Acquire(ctx context.Context, key string, ttl time.Duration) (Handle, error)
  }
  type Handle interface {
      Release() error
      Refresh(ctx context.Context) error
  }
  ```
- `internal/locks/flock/`: file-based lock implementation (default for `concurrency_scope: host`).
- `internal/locks/redis/`: Redis-based implementation using `SET NX EX` + Lua refresh script (for `concurrency_scope: global`).
- `internal/config/`: operator-config schema (YAML), validated via explicit `Validate()`. Schema includes: manifest sources, secret references, lock backend choice + connection info, log level, default policies.

**Acceptance:**
- All interfaces compile and have test doubles.
- `flock` lock passes a contention test (10 goroutines fight, exactly one wins, others wait or fail-fast based on options).
- Redis lock passes the same test against a real Redis (testcontainers in CI).
- Config loading round-trips a sample `cronctl.yaml` correctly. Invalid configs fail with helpful messages.

**RFC updates:**
- Write the `Reconciliation Model` section: state table (manifest × backend → action), ownership rules, hash-based change detection, never-touch-unmanaged guarantee.
- Add Changelog entry.

**Anti-goals:**
- No backend implementations yet. Just the interface.
- No CLI yet.

---

### Phase 5 — Backend adapters and the trigger shim

**Scope:** Implement the three v1 backends. Implement `cronctl trigger`. Wire up reconciliation.

This is the largest phase. Split internally into 5a–5e for sanity, but ship as one phase.

#### 5a — Trigger shim (`internal/trigger/`)

The shim is the synthesis-first runtime. Single entrypoint:

```
cronctl trigger <app>.<name> [--config /etc/cronctl/cronctl.yaml]
```

Behavior on every invocation:
1. Load operator config from `--config` or `/etc/cronctl/cronctl.yaml` or `~/.cronctl/cronctl.yaml`.
2. Read job spec from `/etc/cronctl/jobs/<app>.<name>.json` (or `$CRONCTL_JOB_SPEC_DIR/<app>.<name>.json` — set by K8s env).
3. Resolve the secret(s) from `secret_refs` (env, file, K8s mounted Secret).
4. Acquire concurrency lock per `policy.concurrency` and `policy.concurrency_scope`:
   - `Allow`: skip lock acquisition.
   - `Forbid`: try to acquire; on contention, exit `75` (EX_TEMPFAIL) with structured log.
   - `Replace`: acquire, sending SIGTERM to previous holder if any. (For `host` scope only; `global` Replace is a future enhancement, validated as unsupported in v1.)
5. Generate run-id (UUIDv7). This run-id is constant across all retry attempts within this fire.
6. For each attempt (1 to `policy.retries.max_attempts`):
   - Build the HTTP request, including all `X-Cron-*` injected headers.
   - Sign per Phase 2 auth.
   - Send with `policy.timeout_seconds` enforced via `context.WithTimeout`. The timeout covers connection + headers + full body read.
   - On 2xx: success. Exit 0.
   - On 4xx: app rejected. Do not retry. Exit 1 with structured log.
   - On 5xx, network error, or timeout: log attempt result, sleep with exponential backoff (`min_seconds * 2^(attempt-1)`, capped at `max_seconds`), continue.
7. After exhausting retries: exit 2 with structured log.
8. Always release the lock on exit (defer).
9. Log every attempt to stdout as JSON. Errors to stderr.
10. If running in K8s context (env var `KUBERNETES_SERVICE_HOST` set), also post a K8s Event for terminal outcomes.
11. If running under systemd (env var `INVOCATION_ID` set), emit journald structured fields.
12. Panic recovery via `defer recover()` at the top — log panic, release lock, exit 3.

Exit code map:
- 0: success
- 1: app rejected (4xx)
- 2: retries exhausted
- 3: internal error (panic, config load failure)
- 4: lock contention with `Forbid` (transient, scheduler should not alarm)
- 75 (EX_TEMPFAIL): same as 4; some host schedulers special-case 75.

#### 5b — `crontab` backend (`internal/backends/crontab/`)

- `List`: parse the crontab file (system or user). Find lines marked with `# cronctl:owned app=<app> job=<name> hash=<sha>`. Return as `ManagedEntry`s.
- `Create`/`Update`: rewrite the crontab atomically. Acquire flock on the crontab file during edit.
- `Delete`: rewrite without the entry.
- `Validate`: ensure the schedule is expressible in 5-field cron (no sub-minute, no shortcuts that crontab doesn't support — translate `@daily` to `0 0 * * *`, etc.).
- `History`: read `MAILTO` output if configured; otherwise scrape `/var/log/syslog` or `journalctl -u cron`.
- `Ensure`: confirm the crontab file exists and is writable.

Crontab line format:
```
*/5 * * * * /usr/local/bin/cronctl trigger billing.users-check
# cronctl:owned app=billing job=users-check hash=abc123def
```

Multiple schedules per job become multiple lines, each with the same `app/job` ownership comment but distinct hashes including the schedule index.

#### 5c — `systemd-timer` backend (`internal/backends/systemd/`)

- Unit files in `/etc/systemd/system/cronctl-<app>-<name>.timer` and `cronctl-<app>-<name>.service`.
- `List`: glob `/etc/systemd/system/cronctl-*.timer`, parse, return.
- `Create`: write `.timer` and `.service` files, run `systemctl daemon-reload`, `systemctl enable --now cronctl-<app>-<name>.timer`.
- `Update`: rewrite files, `daemon-reload`, `restart`.
- `Delete`: `systemctl disable --now`, remove files, `daemon-reload`.
- `Validate`: schedule maps to `OnCalendar=` syntax. Use `systemd-analyze calendar` if available to validate.
- `History`: `journalctl -u cronctl-<app>-<name>.service --output=json --since=<since>`.
- `Ensure`: confirm systemd is the init system.

Service file template:
```ini
[Unit]
Description=cronctl: <app>.<name>
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/cronctl trigger <app>.<name>
RuntimeMaxSec=<policy.timeout_seconds + 30>
LoadCredential=cron-secret:<resolved path>
```

Timer file template:
```ini
[Unit]
Description=cronctl timer: <app>.<name>
PartOf=cronctl-<app>-<name>.service

[Timer]
OnCalendar=<schedule translated to OnCalendar>
Unit=cronctl-<app>-<name>.service
Persistent=true

[Install]
WantedBy=timers.target
```

#### 5d — `kubernetes` backend (`internal/backends/kubernetes/`)

- Use `client-go` with in-cluster or out-of-cluster config detection.
- Resources created per job: `CronJob`, plus a `ConfigMap` with the job spec (mounted into the Pod), plus optional `Secret` reference (operator pre-creates).
- `List`: list `CronJob`s with label selector `cronctl.dev/managed=true`, group by `cronctl.dev/app` + `cronctl.dev/job`.
- `Create`/`Update`/`Delete`: standard `client-go` operations with optimistic concurrency (uses `resourceVersion`).
- `Validate`: K8s name length (63 chars), schedule syntax (K8s uses standard cron), `concurrencyPolicy` mapping if not using shim-only.
- `History`: list Pods owned by Jobs owned by the CronJob, read their logs and Events.
- `Ensure`: ping the API server.

CronJob template (per schedule — multi-schedule jobs produce N CronJobs):
```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: cronctl-<app>-<job>-<schedule-index>
  labels:
    cronctl.dev/managed: "true"
    cronctl.dev/app: <app>
    cronctl.dev/job: <job>
    cronctl.dev/hash: <sha>
spec:
  schedule: <cron>
  timeZone: <tz>
  concurrencyPolicy: Forbid     # belt-and-suspenders; shim is authoritative
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 3
  jobTemplate:
    spec:
      backoffLimit: 0           # shim handles retries
      template:
        spec:
          restartPolicy: Never
          containers:
          - name: cronctl
            image: ghcr.io/<owner>/cronctl:<version>
            args: ["trigger", "<app>.<job>"]
            env:
            - name: CRONCTL_JOB_SPEC_DIR
              value: /etc/cronctl/jobs
            - name: CRONCTL_RUN_FROM_KUBERNETES
              value: "true"
            volumeMounts:
            - name: job-spec
              mountPath: /etc/cronctl/jobs
              readOnly: true
            - name: secret
              mountPath: /etc/cronctl/secrets
              readOnly: true
          volumes:
          - name: job-spec
            configMap:
              name: cronctl-<app>-<job>-spec
          - name: secret
            secret:
              secretName: <referenced secret>
```

#### 5e — Reconciliation orchestration (`internal/reconcile/`)

- `Plan(manifest, backends) (Plan, error)` — for each backend, list managed entries, compute desired set, diff into create/update/delete operations. Return as a structured plan.
- `Apply(plan) (Result, error)` — execute the plan. Sequence: deletes → updates → creates (deletes first to free up resources/names).
- `Diff(manifest, backends) (Plan, error)` — same as Plan but always returns; never executes. Used by `cronctl diff`.
- `Drift(manifest, backends) (DriftReport, error)` — read managed entries, recompute hashes, report mismatches.

Concurrent-apply safety: `Apply` acquires a global flock at `/var/lock/cronctl/apply.lock` (or per-backend for K8s, where optimistic concurrency handles it).

**Acceptance criteria for Phase 5:**
- All three backends pass a shared end-to-end test suite (containerized): create a job, list it, update its schedule, run the trigger shim, observe correct firing, delete it, observe absence.
- Trigger shim passes failure-injection tests: app returns 500 → retries; app times out → marked timeout, retried; lock contention → exits 4; panic in shim code → recovers, releases lock, exits 3.
- K8s observability: `kubectl describe cronjob` shows skip events when `concurrencyPolicy` triggers.
- systemd observability: `journalctl -u cronctl-<app>-<job>` shows structured JSON.

**RFC updates:**
- Write `Backend Adapter Contract` (the Go interface, language-neutral).
- Write `Backend Fidelity Matrix` (the table from the design discussion, with the synthesis-first columns).
- Write `Trigger Shim Behavior` (the full per-fire lifecycle, exit codes, header injection, observability hooks).
- Add Changelog entry.

**Anti-goals:**
- No new backends beyond the three.
- No daemon, no state store, no central log database.
- No partial-fidelity rejections — synthesis-first means almost every manifest applies; document explicit cases where it doesn't.

---

### Phase 6 — CLI

**Scope:** Operator-friendly subcommands. The reconciler-side surface.

**Tasks:**

| Command | Purpose |
|---|---|
| `cronctl init` | Interactively scaffold `~/.cronctl/cronctl.yaml`. |
| `cronctl validate <source>` | Lint a manifest file or signed URL. No side effects. |
| `cronctl plan` / `cronctl diff` | Show what `apply` would change. JSON output for CI integration. |
| `cronctl apply [--dry-run]` | Reconcile manifest against backends. |
| `cronctl list [--source=manifest\|installed]` | List jobs. Default `installed`. `manifest` shows manifest source of truth. |
| `cronctl show <app>.<name>` | Detailed view: spec, current backend entries, recent history. |
| `cronctl prune [--app=<app>]` | Remove all `cronctl`-managed entries (with confirmation). |
| `cronctl drift` | Report entries whose installed state diverges from manifest. |
| `cronctl history <app>.<name> [--since=24h] [--status=failed]` | Aggregated history from backend-native log sources. |
| `cronctl trigger <app>.<name>` | (Phase 5 — same binary, exposed as a top-level subcommand.) |
| `cronctl version` | Version, build info, target platforms. |
| `cronctl completion <bash\|zsh\|fish>` | Emit shell completion script. |

Cross-cutting:
- **Output formats**: `--output table` (default for TTY), `--output json` (default for non-TTY, scriptable), `--output yaml`. CI integration relies on JSON being stable.
- **Color**: auto-detect TTY; `--no-color` and `NO_COLOR` env honored.
- **Verbosity**: `-v` for debug, `-q` for errors-only.
- **Config resolution**: `--config <path>` → `$CRONCTL_CONFIG` → `~/.cronctl/cronctl.yaml` → `/etc/cronctl/cronctl.yaml`. First match wins.
- **Help**: every command has `--help` with examples. Top-level `cronctl --help` groups commands (Reconcile, Inspect, Maintain).
- **Exit codes** (documented):
  - `0`: success
  - `1`: generic error
  - `2`: usage / config error
  - `3`: backend unreachable
  - `4`: auth failure (manifest fetch)
  - `5`: drift detected (for `drift` and `diff` when `--exit-on-drift` is set)
- **Confirmations**: `prune`, destructive ops require `--yes` or interactive prompt.
- **Pretty output**: tables with column-width heuristics, color-coded statuses, spinners for long ops.

**Acceptance:**
- Every command has at least one integration test: scaffold a temporary backend (a fake crontab file in a tmpdir; a kind cluster for K8s), run the command, assert output and exit code.
- Snapshot tests for help text.
- Shell completions generated and verified for bash/zsh/fish.
- `cronctl --help` reviewed manually for clarity.

**RFC updates:**
- Write the `CLI` section: command reference, config schema, output formats, exit codes, completion installation, JSON output guarantees for CI use.
- Add Changelog entry.

**Anti-goals:**
- No web UI.
- No interactive TUI (Bubble Tea, etc.) — keep CLI text-based.
- Do not duplicate reconciliation logic in the CLI; commands are thin wrappers around `internal/reconcile/` and `internal/backends/`.

---

### Phase 7 — Polish: distribution, examples, docs, Go SDK

**Scope:** Ship-readiness.

**Tasks:**

*Distribution:*
- `goreleaser` builds binaries for Linux (amd64, arm64), macOS (amd64, arm64), Windows (amd64). Signed where possible (cosign for binaries, sigstore for images).
- Multi-arch Docker image at `ghcr.io/<owner>/cronctl:<version>` and `:latest`. Image is `FROM gcr.io/distroless/static` + binary. < 30MB.
- Homebrew tap formula.
- deb and rpm packages.
- Install script: `curl -fsSL https://cronctl.dev/install.sh | sh` (host the script later — for v1, a section in README is fine).

*Helm chart (alpha):*
- `deploy/helm/cronctl/` — Deployment + ConfigMap + ServiceAccount + RBAC for the reconciler running as a CI-style Job, plus per-app `ConfigMap` templates.
- Chart marked `alpha`, documented as such.

*Go SDK extraction (`pkg/cronsdk`):*
- Public Go API: `cronsdk.Verify(opts) error`, `cronsdk.Headers` constants. Repackages `internal/auth/` and `internal/headers/`.
- One-page godoc with a copy-pasteable example for verifying triggers in a Go HTTP handler.
- Versioned independently of the binary if desired (semver via `pkg/cronsdk/v0/...` if needed; otherwise tied to repo tag).

*Examples polish:*
- Each example has its own README with run instructions and the manifest it produces.
- `examples/go-app/` uses `cronsdk` for verification — proves the Go SDK works.

*Documentation:*
- Top-level README: tagline, install, 5-minute quickstart (local app + crontab backend), link to RFC.
- Architecture diagram (Mermaid in README) showing the manifest → reconciler → host scheduler → trigger flow.
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md` (HMAC vulnerability reporting instructions).
- A `docs/` directory with per-backend setup guides: `crontab.md`, `systemd.md`, `kubernetes.md`. Each shows: prerequisites, how to install `cronctl trigger` on the host, how to run `cronctl apply`, troubleshooting.

*Release prep:*
- npm publish dry-run for `@cronctl/sdk`.
- `goreleaser release --snapshot` succeeds locally.
- GitHub release workflow via `goreleaser` + `changesets` for the TS package.

**Acceptance:**
- A new contributor can clone, run the README quickstart, and see a job fire in under 5 minutes.
- Docker image builds and runs.
- `go install github.com/<owner>/cronctl/cmd/cronctl@latest` works.
- `pnpm add @cronctl/sdk` works in a sample project (npm publish dry-run).
- All packages have working release scripts.

**RFC updates:**
- Write the `Deployment` section: bare-metal (crontab/systemd), Docker, Kubernetes.
- Set `Status: Stable for v1`.
- Add Changelog entry.

**Anti-goals:**
- Do not publish to npm or push real release tags in this phase. Tag a release candidate (`v1.0.0-rc.1`) and stop.
- Publishing v1.0.0 is a separate human decision, not part of Claude Code's plan.

---

## 9. Cross-cutting rules (apply at every phase)

1. **Read before writing.** Re-read RFC.md, DECISIONS.md, OPEN_QUESTIONS.md at the start of every phase.
2. **One source of truth for the manifest shape.** Zod schema in `@cronctl/sdk`, mirrored in Go via `internal/manifest/`. JSON Schema is generated. CI enforces TS↔Go agreement via `manifest-vectors.json`.
3. **Conformance vectors are sacred.** `auth-vectors.json` and `manifest-vectors.json` define correctness for every implementation, current and future. Adding a vector is a spec change requiring an RFC update.
4. **No exceptions for expected failures.** Validation, auth, parse failures return `Result<T, E>` (TS) or `(T, error)` with sentinel/typed errors (Go). Throw/panic only for programmer bugs.
5. **Never log secrets.** HMAC secrets, full request bodies on signature failures — redact. Add a `redact()` helper in both languages and use it.
6. **Constant-time comparison only for HMAC.** Never `===`, never `bytes.Equal`. CI greps for both.
7. **TLS only.** Manifest URLs and trigger URLs require `https://` (or `http://localhost`/`127.0.0.1` for dev). Reject elsewhere.
8. **Inject the clock for testing.** Never call `time.Now()` or `Date.now()` directly in business logic. Take a `Clock` parameter.
9. **No surprise concurrency.** All shared state is explicit. Go: no package-level mutable variables. TS: no module-level mutable state in core.
10. **Tests are not optional.** Every PR adds tests for the changed surface. Coverage cannot drop.
11. **Update the RFC at the end of every phase.** The RFC is the deliverable, not the code.
12. **Open questions are first-class.** Surface uncertainties in OPEN_QUESTIONS.md and ask before deciding unilaterally.
13. **Do not relitigate locked decisions.** If a locked decision needs to change, raise it as an OPEN_QUESTION first.
14. **Conventional Commits + changesets** for every behavior-changing change.
15. **Prefer deletion to abstraction.** Layers that don't earn their keep get removed.
16. **Synthesis-first, always.** When a policy can be enforced by the trigger shim, the shim does it. Backend-native enforcement is defense-in-depth, not the primary mechanism.

---

## 10. What "done" looks like for v1

A developer can:

```bash
# In their app
pnpm add @cronctl/sdk
```

```ts
import { createCron } from '@cronctl/sdk';
import express from 'express';

const cron = createCron({
  app: 'billing-service',
  secret: [process.env.CRON_SECRET_V2!, process.env.CRON_SECRET_V1!],
});

cron.register({
  name: 'reconcile-payments',
  schedule: '*/15 * * * *',
  policy: { concurrency: 'Forbid', timeout_seconds: 120 },
  handler: async (ctx) => {
    // ... do work, idempotent on ctx.runId
    return { ok: true };
  },
});

const app = express();
app.use(cron.express());
app.listen(3000);
```

And separately, on a server (or in CI):

```bash
brew install cronctl
cronctl init
cronctl apply --manifest=https://billing.internal/.well-known/cron-manifest

# Inspect
cronctl list
# APP              NAME                  SCHEDULE        BACKEND   NEXT       LAST
# billing-service  reconcile-payments    */15 * * * *    crontab   in 4m 12s  ok

cronctl history billing-service.reconcile-payments --since=24h
cronctl drift
```

That, plus a complete RFC, plus tests, plus Docker images, plus documentation, plus a Go SDK for verification, is v1.

---

## 11. Pointers Claude Code should remember

- The RFC at `packages/spec/RFC.md` is the product. Code serves the RFC.
- `~/open-sources/fhir-dsl` is the conventions reference for the TS side. Read it before scaffolding.
- The Go binary does two things: reconciler (`apply`, `diff`, `list`, etc.) and trigger executor (`trigger`). One binary, subcommand-dispatched.
- The manifest is the source of truth. Backends are translators. The shim is the runtime.
- **Synthesis-first.** When in doubt about a policy, the shim enforces it.
- Same HMAC scheme for manifest and triggers. Multiple acceptable secrets for rotation.
- **No daemon. No state store. No central history database.** History is read on-demand from backend-native sources.
- v1 backends: crontab, systemd-timer, kubernetes. No others.
- v1 lock backends: `flock` (host scope), Redis (global scope). No others.
- Manifest can come from a URL or a local file (`--manifest=file:./manifest.json`).
- Conformance vectors define correctness. Both TS and Go pass `auth-vectors.json` and `manifest-vectors.json` at every commit.
- Resist scope creep. Items not in this plan go to OPEN_QUESTIONS.md, not into the code.
