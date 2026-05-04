# cronix

> **Cron jobs as code.** Apps declare scheduled work in their own code; cronix reconciles those declarations against the host's native scheduler.

> ⚠️ **Pre-alpha — under active development.** No release yet; APIs and on-the-wire shapes will change. Do not run in production.

## The pitch

Today the schedule for a job lives somewhere different from the code that handles it — in a UI, an EventBridge rule, a hand-edited crontab, a separate YAML repo. Changes require coordinating two places. Drift is invisible. Reviewers see the handler change but miss the schedule change.

`cronix` puts the schedule next to the handler. The application is the source of truth for its own schedules via a manifest endpoint. `cronix apply` reconciles that manifest against whatever scheduler the host provides — `crontab`, `systemd-timer`, Kubernetes — installing, updating, or removing entries as needed. The host's native scheduler does the firing. A small Go binary, `cronix trigger`, handles HMAC signing, concurrency locks, timeouts, and retries on every fire.

The protocol is the product. The reconciler and SDK are reference implementations.

## Repo layout (polyglot monorepo)

```
cronix/
├── spec/         # language-neutral: RFC, DECISIONS, JSON Schema, conformance vectors
├── ts/           # TypeScript workspace (pnpm) — @awbx/cronix-sdk + framework adapters + examples
├── go/           # Go module (github.com/awbx/cronix/go) — cmd/cronix binary + internal/ + pkg/cronsdk
├── deploy/       # Dockerfile, Helm chart — language-neutral
├── .github/      # CI workflows
└── PLAN.md       # Implementation plan
```

Future SDKs (Python, Ruby, …) get their own top-level directory. The `spec/` directory is the source of truth for cross-language correctness — every SDK passes the same `manifest-vectors.json` and `auth-vectors.json`.

## Architecture

```
              app (your service)
              GET /.well-known/cron-manifest
              POST /api/v1/scheduled/<name>
                       │
                       │ (1) read manifest
                       ▼
                cronix apply (Go)
                       │
                       │ (2) install/update/delete entries
                       ▼
              host scheduler (crontab / systemd-timer / k8s CronJob)
                       │
                       │ (3) invoke on schedule
                       ▼
                cronix trigger (Go)
                       │   • acquires lock
                       │   • signs HMAC
                       │   • POSTs to your handler
                       │   • applies timeout + retries
                       ▼
                app handler (verifies signature, dedupes by run-id)
```

## Status

v1 release candidate. The on-the-wire spec is frozen; per-backend live integration for systemd-timer and kubernetes is the remaining follow-up. The authoritative on-the-wire spec lives in [spec/RFC.md](./spec/RFC.md). Implementation history is tracked in [PLAN.md](./PLAN.md).

## Documentation

- [spec/RFC.md](./spec/RFC.md) — protocol, manifest, authentication, SDK contract, backend contract, CLI, deployment
- [docs/crontab.md](./docs/crontab.md), [docs/systemd.md](./docs/systemd.md), [docs/kubernetes.md](./docs/kubernetes.md) — per-backend setup
- [CONTRIBUTING.md](./CONTRIBUTING.md), [SECURITY.md](./SECURITY.md)
- [ts/examples/express-app](./ts/examples/express-app), [ts/examples/fastify-app](./ts/examples/fastify-app), [ts/examples/hono-app](./ts/examples/hono-app), [ts/examples/hand-rolled](./ts/examples/hand-rolled), [go/examples/go-app](./go/examples/go-app)

## Build

```bash
# TypeScript
cd ts
pnpm install
pnpm build && pnpm test && pnpm lint && pnpm typecheck

# Go
cd go
go build ./...
go test ./...
go vet ./...

# Multi-platform binaries (snapshot — no release) from repo root
goreleaser build --snapshot --clean
```

## Install

```bash
# CLI — one-liner (Linux/macOS, amd64/arm64)
curl -fsSL https://raw.githubusercontent.com/awbx/cronix/main/install.sh | sh

# CLI — pinned version + custom install dir
curl -fsSL https://raw.githubusercontent.com/awbx/cronix/main/install.sh \
  | CRONIX_VERSION=v0.1.1 INSTALL_DIR=/usr/local/bin sh

# CLI — Go install
go install github.com/awbx/cronix/go/cmd/cronix@latest

# CLI — Docker
docker pull awbx/cronix

# TypeScript SDK
pnpm add @awbx/cronix-sdk
```

## License

MIT © Abdelhadi Sabani
