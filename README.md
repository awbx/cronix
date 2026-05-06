# cronix

[![npm version](https://img.shields.io/npm/v/@awbx/cronix-sdk.svg?label=%40awbx%2Fcronix-sdk)](https://www.npmjs.com/package/@awbx/cronix-sdk)
[![Go Reference](https://pkg.go.dev/badge/github.com/awbx/cronix/go.svg)](https://pkg.go.dev/github.com/awbx/cronix/go)
[![CI](https://github.com/awbx/cronix/actions/workflows/ci.yml/badge.svg)](https://github.com/awbx/cronix/actions/workflows/ci.yml)
[![Release](https://github.com/awbx/cronix/actions/workflows/release.yml/badge.svg)](https://github.com/awbx/cronix/actions/workflows/release.yml)
[![License](https://img.shields.io/npm/l/@awbx/cronix-sdk.svg)](./LICENSE)


https://github.com/user-attachments/assets/7506551d-4c2c-4d8a-ac61-9b6a8a0e4d55


> **Cron jobs as code.** Apps declare scheduled work in their own code; cronix reconciles those declarations against the host's native scheduler — `crontab`, `systemd-timer`, Kubernetes, or AWS EventBridge Scheduler.

> ⚠️ **Under active development.** The on-the-wire spec is stable; APIs may evolve before v1.0. Try it and file issues.

## Why

Today the schedule for a job lives somewhere different from the code that handles it — in a UI, an EventBridge rule, a hand-edited crontab, a separate YAML repo. Changes require coordinating two places. Drift is invisible. Reviewers see the handler change but miss the schedule change.

`cronix` puts the schedule next to the handler. Your app is the source of truth for its own schedules via a `/.well-known/cron-manifest` endpoint. `cronix apply` reconciles that manifest against whichever scheduler the host provides. The host scheduler does the firing. A small Go binary, `cronix trigger`, handles HMAC signing, concurrency locks, timeouts, and retries on every fire.

The protocol is the product. The reconciler and SDKs are reference implementations.

## Install

### CLI (the reconciler)

```bash
# macOS — Homebrew
brew install awbx/cronix/cronix

# Linux / macOS — one-liner
curl -fsSL https://raw.githubusercontent.com/awbx/cronix/main/install.sh | sh

# Pin a version + custom install dir
curl -fsSL https://raw.githubusercontent.com/awbx/cronix/main/install.sh \
  | CRONIX_VERSION=v0.7.2 INSTALL_DIR=/usr/local/bin sh

# Linux packages — grab from the latest release
# https://github.com/awbx/cronix/releases/latest
#   cronix_<ver>_linux_amd64.deb  (Debian/Ubuntu)
#   cronix_<ver>_linux_amd64.rpm  (RHEL/Fedora/openSUSE)
#   cronix_<ver>_linux_amd64.apk  (Alpine)

# Go developers
go install github.com/awbx/cronix/go/cmd/cronix@latest

# Docker
docker pull awbx/cronix
```

Verify:

```bash
cronix version
```

### App SDK

```bash
# TypeScript
pnpm add @awbx/cronix-sdk

# Framework adapters (only if you need them — see below)
pnpm add @awbx/cronix-adapter-express
pnpm add @awbx/cronix-adapter-fastify
pnpm add @awbx/cronix-adapter-koa
pnpm add @awbx/cronix-adapter-nest

# Go (signature verification only)
go get github.com/awbx/cronix/go/pkg/cronsdk
```

## Quick start (TypeScript + Hono)

```ts
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { Hono } from "hono";

const cron = createCron({
  app: "billing-service",
  baseUrl: "https://billing.example.com",
  secret: process.env.CRON_SECRET!,
});

cron.register({
  name: "reconcile-payments",
  schedule: "*/15 * * * *",
  auth: { secret_refs: ["env:CRON_SECRET"] },
  handler: async (ctx) => {
    console.log(`fired ${ctx.name} run=${ctx.runId}`);
    // your work here
    return { ok: true };
  },
});

const app = new Hono();
app.all(MANIFEST_PATH, (c) => cron.handle(c.req.raw));
app.all(`${TRIGGER_PATH_PREFIX}:name`, (c) => cron.handle(c.req.raw));

export default app;
```

Reconcile from your laptop or CI:

```bash
cronix apply \
  --manifest https://billing.example.com/.well-known/cron-manifest \
  --backend crontab \
  --crontab-path /etc/crontab \
  --trigger-bin /usr/local/bin/cronix \
  --secret-ref env:CRON_SECRET
```

That's it. Your `*/15 * * * *` line lives in your app code; `cron(8)` actually fires it; `cronix trigger` signs the request and POSTs back to your handler.

## Examples

Runnable mini-apps, each one ~50 lines:

| Example | Stack |
|---|---|
| [ts/examples/hono-app](./ts/examples/hono-app) | Hono — runs unchanged on Node, Bun, Cloudflare Workers |
| [ts/examples/express-app](./ts/examples/express-app) | Express + `@awbx/cronix-adapter-express` |
| [ts/examples/fastify-app](./ts/examples/fastify-app) | Fastify + `@awbx/cronix-adapter-fastify` |
| [ts/examples/hand-rolled](./ts/examples/hand-rolled) | No framework — just `node:http` + `verifyManifest`/`verifyTrigger` |
| [go/examples/go-app](./go/examples/go-app) | Go `net/http` server using `pkg/cronsdk` for HMAC verify |

Each example has a README with the exact `pnpm dev` (or `go run`) command and a curl recipe to test end-to-end.

## How it works

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
              host scheduler (crontab / systemd / k8s CronJob / EventBridge)
                       │
                       │ (3) invoke on schedule
                       ▼
                cronix trigger (Go)
                       │   • acquires lock
                       │   • signs HMAC (Stripe-style)
                       │   • POSTs to your handler
                       │   • applies timeout + retries
                       ▼
                app handler (verifies signature, dedupes by run-id)
```

## Backends

| Backend | What it writes | Setup |
|---|---|---|
| `crontab` | `/etc/crontab` lines with `# cronix:owned` markers | [docs/src/content/docs/backends/crontab.md](./docs/src/content/docs/backends/crontab.md) |
| `systemd-timer` | `.timer` + `.service` units in `/etc/systemd/system` | [docs/src/content/docs/backends/systemd.md](./docs/src/content/docs/backends/systemd.md) |
| `kubernetes` | `CronJob` + `ConfigMap` per job | [docs/src/content/docs/backends/kubernetes.md](./docs/src/content/docs/backends/kubernetes.md) |
| `aws-scheduler` | EventBridge Schedules → cronix-trigger Lambda | [docs/src/content/docs/backends/aws.md](./docs/src/content/docs/backends/aws.md) |

cronix tracks ownership inside each resource — it never touches lines, units, or objects it didn't create. Run alongside hand-edited entries safely.

## Framework adapters (TypeScript)

For frameworks that don't speak Web Fetch natively, install the matching sibling adapter package. Each one exports a `handle()` that lifts any `(req: Request) => Response | Promise<Response>` into a framework-native handler:

```ts
// Express
import { handle } from "@awbx/cronix-adapter-express";
app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));

// Fastify (rawBody installs a wildcard parser to keep bytes-as-sent)
import { handle, rawBody } from "@awbx/cronix-adapter-fastify";
rawBody(app);
app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));

// Koa (mount before any body-parser middleware)
import { handle } from "@awbx/cronix-adapter-koa";
router.all(MANIFEST_PATH, handle((req) => cron.handle(req)));

// NestJS (Express by default — bootstrap with `bodyParser: false`)
import { handle } from "@awbx/cronix-adapter-nest";
app.use(MANIFEST_PATH, handle((req) => cron.handle(req)));
```

Hono, Bun, Workers, Vercel/Next.js, and Deno all serve a Web `Request` natively — no adapter needed; just call `cron.handle(req)` directly from your route.

## Documentation

- **Documentation site** — https://awbx.github.io/cronix/ (sources in [`docs/src/content/docs/`](./docs/src/content/docs/))
- [spec/RFC.md](./spec/RFC.md) — protocol, manifest, authentication, SDK contract, backend contract
- [CONTRIBUTING.md](./CONTRIBUTING.md) — dev setup, repo layout, conformance vectors
- [SECURITY.md](./SECURITY.md) — vulnerability disclosure

## Project status

| Area | State |
|---|---|
| Spec | RFC v1 frozen — see [spec/RFC.md](./spec/RFC.md) |
| Backends | `crontab`, `systemd-timer`, `kubernetes`, `aws-scheduler` — all reconcile end-to-end |
| CLI | `init`, `validate`, `plan` / `diff`, `apply`, `drift`, `list`, `global-status`, `show`, `prune`, `history`, `trigger`, `version`, `completion` |
| TypeScript SDK | `@awbx/cronix-sdk` + 4 framework adapters, conformance-tested against shared spec vectors |
| Go SDK | `pkg/cronsdk` — HMAC verify only, conformance-tested |
| Distribution | Homebrew tap, deb / rpm / apk, Docker, npm |

## Contributing

cronix is open source under MIT — issues, discussions, and PRs are welcome. A few things worth knowing before you dive in:

- **The RFC is the product.** Behavior changes are discussed and agreed before code lands. The protocol shape (manifest, signing, headers) is the contract; everything else is an implementation detail.
- **Both languages stay in lock-step.** Manifest shape, header format, and signing scheme changes must land in TypeScript (`@awbx/cronix-sdk`) and Go (`internal/manifest`, `internal/auth`) in the same PR, with both passing the shared `manifest-vectors.json` and `auth-vectors.json`.
- **Conformance vectors are sacred.** Adding or modifying one is a spec change.

Full dev setup, branch flow, and release process: [CONTRIBUTING.md](./CONTRIBUTING.md).

Quick paths to help if you're new:

- **File an issue** about something that surprised you — bad error messages, missing docs, unclear flags. No issue is too small.
- **Add an example** for a stack we don't yet cover (Bun-only, Cloudflare Workers, AWS Lambda app, etc.).
- **Port the SDK** — Python and Ruby SDKs are wide open. The conformance vectors give you a green-light test suite.

## License

MIT © Abdelhadi Sabani — see [LICENSE](./LICENSE).
