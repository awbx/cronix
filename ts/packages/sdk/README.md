# @awbx/cronix-sdk

App-side SDK for [cronix](https://github.com/awbx/cronix). Declare cron jobs in your application code, build the manifest the cronix CLI reconciles, and verify signed triggers when they fire.

This package is **framework-agnostic**. It accepts a Web `Request` and returns a `Response` — drop it into Hono, Bun, Workers, Vercel route handlers, or any runtime that exposes `Request` natively. For frameworks that don't (Express, Fastify, Koa, NestJS), install a sister adapter package.

## Adapter packages

| Framework | Package |
|---|---|
| Express   | [`@awbx/cronix-adapter-express`](../adapter-express)   |
| Fastify   | [`@awbx/cronix-adapter-fastify`](../adapter-fastify)   |
| Koa       | [`@awbx/cronix-adapter-koa`](../adapter-koa)           |
| NestJS    | [`@awbx/cronix-adapter-nest`](../adapter-nest)         |
| Hono      | not needed — `cron.handle(c.req.raw)` |
| Bun / Workers / Vercel | not needed — Web `Request` is native |

Each adapter is a small (~50 LOC) wrapper that captures the request body as raw bytes (HMAC verification depends on this) and lifts the framework's req/reply pair into Web `Request` / `Response`.

## Install

```bash
pnpm add @awbx/cronix-sdk
# + optionally one adapter package for your framework
pnpm add @awbx/cronix-adapter-express   # for example
```

## Quick start (Hono — no adapter needed)

```ts
import { Hono } from "hono";
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";

const cron = createCron({
  app: "billing",
  baseUrl: "https://billing.internal.example.com",
  secret: process.env.CRON_SECRET!,
});

cron.register({ name: "reconcile", schedule: "*/15 * * * *" }, async (ctx) => {
  console.log("reconciling…", ctx.runId);
});

const app = new Hono();
app.all(MANIFEST_PATH, (c) => cron.handle(c.req.raw));
app.all(`${TRIGGER_PATH_PREFIX}:name`, (c) => cron.handle(c.req.raw));

export default app;
```

Then declare the same job(s) in a `manifest.cronix.json` (or generate it from `cron.manifest()`) and run:

```bash
cronix apply --manifest ./manifest.cronix.json --backend kubernetes
```

## API surface

### `createCron(opts)`

Returns a `CronInstance` bound to your app. Configures the manifest's app id, the base URL the host scheduler hits, and the HMAC secret(s) used for signed-trigger verification.

### `cron.register({ name, schedule, … }, handler)`

Declare a job. The job is added to the manifest and the handler is dispatched when a verified trigger arrives at `/api/v1/scheduled/<name>`.

### `cron.handle(request)`

One-shot router. Inspects the `Request`'s pathname:
- `MANIFEST_PATH` → returns the JSON manifest.
- `${TRIGGER_PATH_PREFIX}<name>` → verifies HMAC + dispatches the registered handler.
- Anything else → 404.

Returns a `Response`. This is the only method most apps need.

### `cron.manifest()`

Returns the parsed manifest object. Useful if you'd rather render it yourself or pipe it through your own validation.

### Lower-level

- `cron.verifyManifest(req)` / `cron.verifyTrigger(req)` — opt out of `handle()` if you want to insert logging/metrics between verify and dispatch.
- `MANIFEST_PATH`, `TRIGGER_PATH_PREFIX`, `HeaderSignature`, `HeaderRunId` — wire-format constants if you're hand-rolling.
- `sign(opts)`, `verify(opts)` — raw HMAC primitives, exposed for conformance tests.

## What the SDK does *not* do

- **No scheduler.** Reconciling the manifest into actual cron entries is the job of the `cronix` Go CLI / reconciler.
- **No transport.** The SDK builds a manifest and verifies triggers; getting the host scheduler to fire those triggers is the reconciler's job.
- **No retry policy.** Retries, locks, and timeouts are enforced by the trigger shim on the host side. The SDK trusts a fired trigger and runs the handler once.

## Wire format

The TypeScript SDK and the Go reconciler agree byte-for-byte on:
- The manifest schema (29 conformance vectors at `spec/manifest-vectors.json`).
- HMAC sign/verify (35 vectors at `spec/auth-vectors.json`).

If you implement another SDK in another language, those vectors are the contract.
