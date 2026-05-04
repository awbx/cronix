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

v1 release candidate. As of v0.3.0 all three backends — `crontab`, `systemd-timer`, `kubernetes` — fully reconcile against their host scheduler (`apply`, `plan`, `drift`, `list`, `prune`, `show`). The on-the-wire spec is frozen; remaining work is `cronix history` (run-record reads from journalctl / K8s logs) and the operator polish in PLAN §7. Authoritative spec: [spec/RFC.md](./spec/RFC.md). Implementation history: [PLAN.md](./PLAN.md).

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

## TypeScript SDK — minimal hono example

```ts
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { Hono } from "hono";

// Hono-style typed environment. Bindings are app-scoped (set once at
// createCron). Variables are per-fire (set at cron.handle).
type CronEnv = {
  Bindings: { db: Database; logger: Logger };
  Variables: { traceId: string };
};

const cron = createCron<CronEnv>({
  app: "billing-service",
  baseUrl: "https://billing.example.com",
  secret: process.env.CRON_SECRET!,
  env: { db, logger: console },        // ← app-scoped
});

cron.register({
  name: "reconcile-payments",
  schedule: "*/15 * * * *",
  auth: { secret_refs: ["env:CRON_SECRET"] },
  handler: async (ctx) => {
    // ctx.env.<key> and ctx.var.<key> are fully typed
    ctx.env.logger.info(`fired ${ctx.name} run=${ctx.runId} trace=${ctx.var.traceId}`);
    await ctx.env.db.query("UPDATE payments SET ...");
    return { ok: true };
  },
});

const app = new Hono();
app.all(MANIFEST_PATH, (c) => cron.handle(c.req.raw));
app.all(`${TRIGGER_PATH_PREFIX}:name`, (c) =>
  cron.handle(c.req.raw, { vars: { traceId: crypto.randomUUID() } }),  // ← per-fire
);
```

`cron.handle(req, opts)` is the **zero-glue** path. For more control there are explicit `cron.verifyManifest(req)` / `cron.verifyTrigger(req)` methods, plus `cron.on(name, handler)` for late-binding handlers from another file. Both methods accept the same `{vars}` option. See [`ts/examples/`](./ts/examples/) for the runnable hono / express / fastify variants.

### Framework adapters

For frameworks that don't speak Web Fetch natively, the SDK ships subpath adapters that lift any `(req: Request) => Response | Promise<Response>` to a framework-native handler. You wire your own routes — the adapter just bridges the request/response shapes:

```ts
// Express
import { handle } from "@awbx/cronix-sdk/express";
app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
app.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) =>
  cron.handle(req, { vars: { traceId: crypto.randomUUID() } }),
));

// Fastify (rawBody installs a wildcard parser to keep the bytes-as-sent)
import { handle, rawBody } from "@awbx/cronix-sdk/fastify";
rawBody(app);
app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
app.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));

// Koa (mount before any body-parser middleware so HMAC sees the raw bytes)
import { handle } from "@awbx/cronix-sdk/koa";
router.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
router.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));

// NestJS (Express by default — bootstrap with `bodyParser: false`)
import { handle } from "@awbx/cronix-sdk/nest";
app.use(MANIFEST_PATH, handle((req) => cron.handle(req)));
app.use(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));

// Vercel (Next.js route handlers / Edge functions — Web Request native)
// app/api/cron/[[...slug]]/route.ts
import { handle } from "@awbx/cronix-sdk/vercel";
export const POST = handle((req) => cron.handle(req));
export const GET = handle((req) => cron.handle(req));
```

Because `handle()` takes any fetch fn — not the cron instance directly — you can compose freely (logging, auth, routing across multiple cron instances). Hono / Workers / Bun / Deno serve a Web `Request` natively, so they don't need an adapter.

## License

MIT © Abdelhadi Sabani
