<p align="center">
  <img src="docs/public/cronix-mark.svg" alt="cronix" width="120" />
</p>

<h1 align="center">cronix</h1>

<p align="center"><strong>Cron jobs as code.</strong> Your handler is the source of truth — cronix reconciles it onto <code>crontab</code>, <code>systemd-timer</code>, Kubernetes, or AWS EventBridge.</p>

<p align="center">
  <a href="https://www.npmjs.com/package/@awbx/cronix-sdk"><img src="https://img.shields.io/npm/v/@awbx/cronix-sdk.svg?label=%40awbx%2Fcronix-sdk" alt="npm version" /></a>
  <a href="https://pkg.go.dev/github.com/awbx/cronix/go"><img src="https://pkg.go.dev/badge/github.com/awbx/cronix/go.svg" alt="Go Reference" /></a>
  <a href="https://github.com/awbx/cronix/actions/workflows/ci.yml"><img src="https://github.com/awbx/cronix/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/awbx/cronix/actions/workflows/release.yml"><img src="https://github.com/awbx/cronix/actions/workflows/release.yml/badge.svg" alt="Release" /></a>
  <a href="./LICENSE"><img src="https://img.shields.io/npm/l/@awbx/cronix-sdk.svg" alt="License" /></a>
</p>

<p align="center">
  <a href="https://awbx.github.io/cronix/"><strong>Docs</strong></a> ·
  <a href="https://awbx.github.io/cronix/quickstart/"><strong>Quick start</strong></a> ·
  <a href="./spec/RFC.md"><strong>RFC</strong></a> ·
  <a href="./ts/examples/"><strong>Examples</strong></a>
</p>

---


https://github.com/user-attachments/assets/cf642aa2-67db-4b34-9ce7-6bf9ece5a5ab

---

> ⚠️ **Under active development.** The on-the-wire spec is stable; APIs may evolve before v1.0. Try it and file issues.

## The problem

Today, "I need a scheduled job" has three answers — none of them tell you the whole picture:

- 🟧 **In-app queue** (BullMQ / Agenda) — needs Redis ops, repeats stack on restart, schedule lives in code *and* in Redis.
- 🟧 **In-process** (node-cron / cron) — stops with the process, every replica fires it (N pods → N runs), no audit, no retries.
- 🟧 **Host scheduler** (crontab / systemd / k8s) — per-machine install, ssh-edit drift, no who-changed-what audit, silent failures.

Whichever you pick, you can't answer: *is this running anywhere right now? who changed the schedule? did the last run succeed?*

## The flip

`cronix` puts the schedule next to the handler. Your app's `/.well-known/cron-manifest` endpoint is the source of truth. `cronix apply` reconciles it against whichever scheduler the host provides. The host scheduler does the firing. A small Go binary, `cronix trigger`, handles HMAC signing, concurrency locks, timeouts, and retries on every fire.

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
import { createCron } from "@awbx/cronix-sdk";
import { Hono } from "hono";

const cron = createCron({
  app: "billing-service",
  baseUrl: "https://billing.example.com",
  secret: process.env.CRON_SECRET!,
});

cron.register({
  name: "reconcile-payments",
  schedule: "*/15 * * * *", // ← lives next to the handler
  handler: async (ctx) => {
    console.log(`fired ${ctx.name} run=${ctx.runId}`);
    // your work here
    return { ok: true };
  },
});

const app = new Hono();
app.all("/.well-known/cron-manifest", (c) => cron.handle(c.req.raw));
app.all("/api/v1/scheduled/:name", (c) => cron.handle(c.req.raw));

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
app.all("/.well-known/cron-manifest", handle((req) => cron.handle(req)));

// Fastify (rawBody installs a wildcard parser to keep bytes-as-sent)
import { handle, rawBody } from "@awbx/cronix-adapter-fastify";
rawBody(app);
app.all("/.well-known/cron-manifest", handle((req) => cron.handle(req)));

// Koa (mount before any body-parser middleware)
import { handle } from "@awbx/cronix-adapter-koa";
router.all("/.well-known/cron-manifest", handle((req) => cron.handle(req)));

// NestJS (Express by default — bootstrap with `bodyParser: false`)
import { handle } from "@awbx/cronix-adapter-nest";
app.use("/.well-known/cron-manifest", handle((req) => cron.handle(req)));
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

cronix is open source under Apache 2.0 — issues, discussions, and PRs are welcome. A few things worth knowing before you dive in:

- **The RFC is the product.** Behavior changes are discussed and agreed before code lands. The protocol shape (manifest, signing, headers) is the contract; everything else is an implementation detail.
- **Both languages stay in lock-step.** Manifest shape, header format, and signing scheme changes must land in TypeScript (`@awbx/cronix-sdk`) and Go (`internal/manifest`, `internal/auth`) in the same PR, with both passing the shared `manifest-vectors.json` and `auth-vectors.json`.
- **Conformance vectors are sacred.** Adding or modifying one is a spec change.

Full dev setup, branch flow, and release process: [CONTRIBUTING.md](./CONTRIBUTING.md).

Quick paths to help if you're new:

- **File an issue** about something that surprised you — bad error messages, missing docs, unclear flags. No issue is too small.
- **Add an example** for a stack we don't yet cover (Bun-only, Cloudflare Workers, AWS Lambda app, etc.).
- **Port the SDK** — Python and Ruby SDKs are wide open. The conformance vectors give you a green-light test suite.

## Verify a release

Every release is signed with [cosign](https://github.com/sigstore/cosign) keyless, using the OIDC identity of this repository's GitHub Actions release workflow. There is no public key to track and no secret to compromise — the signature is bound to the workflow itself via Sigstore's Fulcio CA.

**Verify a downloaded binary.** Grab `checksums.txt`, `checksums.txt.sig`, and `checksums.txt.pem` from the release page alongside your binary:

```sh
cosign verify-blob \
  --certificate-identity-regexp 'https://github.com/awbx/cronix/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  checksums.txt

# Then confirm your binary's hash is in the (now-trusted) checksums file:
sha256sum -c --ignore-missing checksums.txt
```

**Verify a container image** (signatures are attached in-registry):

```sh
cosign verify \
  --certificate-identity-regexp 'https://github.com/awbx/cronix/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/awbx/cronix:v1.0.0-amd64
```

Both commands print the signing certificate's identity. If the identity does not start with `https://github.com/awbx/cronix/`, the artifact was not built by this project — do not use it.

## License

Apache 2.0 © Abdelhadi Sabani — see [LICENSE](./LICENSE). Releases before v1.0.0 were distributed under MIT; the historical text lives at [LICENSE-MIT](./LICENSE-MIT).
