---
title: TypeScript SDK
description: "@awbx/cronix-sdk API reference"
---

`@awbx/cronix-sdk` is the reference SDK for declaring cron jobs from inside your application. It builds the [manifest](/cronix/concepts/manifest/) that [`cronix apply`](/cronix/cli/apply/) reads, and verifies the signed HTTP triggers your backend sends. It is runtime-agnostic — Web Crypto and Web Fetch only — so the same code runs on Node 20+, Bun, Deno, Workers, and Edge.

## Install

```bash
pnpm add @awbx/cronix-sdk
```

If your framework does not natively expose Web `Request` / `Response` objects, install one of the [framework adapters](/cronix/sdk/adapters/) alongside the SDK.

## createCron(options)

Build a cron instance. One per app.

```ts
import { createCron } from "@awbx/cronix-sdk";

const cron = createCron({
  app: "billing-service",
  baseUrl: "https://billing.example.com",
  secret: process.env.CRON_SECRET!,
});
```

| Option | Type | Required | Purpose |
|---|---|---|---|
| `app` | `string` | yes | App id. Lowercase letters, digits, hyphens; up to 63 chars. Surfaces in the manifest and in CLI output. |
| `baseUrl` | `string` | yes | Public URL the backend will reach. Used to derive each job's trigger URL (`<baseUrl>/api/v1/scheduled/<name>`). No trailing slash required. |
| `secret` | `string \| string[] \| () => string \| string[]` | yes | HMAC secret(s). Pass an array (or function returning an array) to support secret rotation: the verifier tries each in order and returns the matching index. |
| `env` | `E["Bindings"]` | no | App-scoped values exposed to every handler as `ctx.env`. Database clients, loggers, config. |
| `vars` | `E["Variables"]` | no | Per-fire defaults exposed as `ctx.var`. Anything passed to `cron.handle({vars})` is merged on top and wins on key collisions. |

### Secret rotation

The function form is re-evaluated on every verify call, so it composes with a secrets manager that rotates in the background:

```ts
const cron = createCron({
  app: "billing-service",
  baseUrl,
  secret: () => [secretsManager.current(), secretsManager.previous()],
});
```

Rotate the backend's outgoing secret first, then drop the old one from this list once you've confirmed nothing is signing with it.

### Typed env and vars

The instance is generic on a `{ Bindings, Variables }` shape modelled on Hono. Handlers see `ctx.env` and `ctx.var` typed:

```ts
type Env = {
  Bindings: { db: Database; logger: Logger };
  Variables: { traceId: string };
};

const cron = createCron<Env>({
  app: "billing-service",
  baseUrl,
  secret: process.env.CRON_SECRET!,
  env: { db, logger: console },
});
```

## cron.register(jobDef)

Declare one job. The SDK validates the definition immediately and throws on shape errors so misconfiguration fails at boot, not at first fire.

```ts
cron.register({
  name: "reconcile-payments",
  schedule: "0 * * * *",
  timezone: "UTC",
  policy: { concurrency: "Forbid", timeout_seconds: 300 },
  handler: async (ctx) => {
    await ctx.env.db.query("...");
    return { ok: true };
  },
});
```

### JobDefinition

| Field | Type | Default | Notes |
|---|---|---|---|
| `name` | `string` | required | Lowercase letters, digits, hyphens; up to 63 chars. Becomes the trigger path suffix. |
| `schedule` | `string` | one-of required | Five-field cron expression or shortcut (`@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`, `@every Ns/m/h`). |
| `schedules` | `string[]` | one-of required | Multiple expressions. Mutually exclusive with `schedule`. Up to 64 entries. |
| `timezone` | `string` | `"UTC"` | IANA name (e.g. `Europe/Paris`). Backend interprets the schedule in this zone. |
| `method` | `"GET" \| "POST" \| "PUT" \| "PATCH" \| "DELETE"` | `"POST"` | HTTP method the backend uses to trigger. |
| `headers` | `Record<string, string>` | `{}` | Extra static headers added to every trigger. |
| `body` | `string` | `""` | Static request body. Useful for tagging fires. |
| `urlOverride` | `string` | derived | Replace the conventional `<baseUrl>/api/v1/scheduled/<name>` URL. Rare. |
| `policy` | `JobPolicy` | see below | Concurrency, timeout, retry. |
| `auth` | `JobAuth` | `{}` | Secret references for the backend to inject. |
| `handler` | `JobHandler` | optional | Run on each fire. Omit to bind later via `cron.on()`. |

### policy

| Field | Type | Default | Notes |
|---|---|---|---|
| `concurrency` | `"Allow" \| "Forbid" \| "Replace"` | `"Forbid"` | What to do if a fire starts while a previous one is still running. |
| `concurrency_scope` | `"host" \| "global"` | `"host"` | Whether the concurrency rule applies per-host or cluster-wide. Backend-dependent. |
| `timeout_seconds` | `number` | `60` | Per-fire deadline. Range 1..600. |
| `retries` | `RetryPolicy` | see below | Retry after a failed fire. |

### policy.retries

| Field | Type | Default | Notes |
|---|---|---|---|
| `max_attempts` | `number` | `3` | Total attempts including the first. Range 1..10. |
| `min_seconds` | `number` | `1` | Backoff floor. |
| `max_seconds` | `number` | `60` | Backoff ceiling. Must be ≥ `min_seconds`. |

### auth

| Field | Type | Default | Notes |
|---|---|---|---|
| `secret_refs` | `string[]` | `[]` | Opaque references the backend resolves and injects (e.g. `aws:secretsmanager:cron-secret`). The SDK never resolves these — only validates the format. Up to 8 entries. |

## cron instance methods

### handle(req, opts?)

Zero-glue dispatcher. Routes manifest vs trigger by URL path; always returns a `Response`. Use this on Hono, Bun, Workers, Deno, Vercel/Next.js, and any other Web Fetch runtime.

```ts
import { Hono } from "hono";
const app = new Hono();
app.all("*", (c) => cron.handle(c.req.raw, { vars: { traceId: crypto.randomUUID() } }));
```

Signature: `(req: Request, opts?: { now?: number; maxSkewSeconds?: number; vars?: Variables }) => Promise<Response>`.

### verifyManifest(req, opts?)

Verify a request to the manifest endpoint. Returns a verdict you decide what to do with — useful when you want logging or metrics between verify and respond.

```ts
const r = await cron.verifyManifest(req);
if (!r.ok) return r.toResponse();
return Response.json(cron.manifest());
```

Signature: `(req: Request | VerifyRequestObject, opts?) => Promise<VerifyManifestResult>`.

### verifyTrigger(req, opts?)

Verify a request to a trigger endpoint. On success, returns the typed `ctx` and a `run()` thunk that dispatches to the registered handler.

```ts
const r = await cron.verifyTrigger(req);
if (!r.ok) return r.toResponse();
console.log(`fire ${r.ctx.name} run=${r.ctx.runId} attempt=${r.ctx.attempt}`);
const out = await r.run();
return new Response(out.body ?? null, { status: out.status });
```

Signature: `(req: Request | VerifyRequestObject, opts?) => Promise<VerifyTriggerResult>`.

### on(name, handler)

Bind (or rebind) a handler to an already-registered job. Lets you split declaration from implementation across files.

```ts
// jobs.ts
cron.register({ name: "send-invoices", schedule: "@daily" });

// handlers/send-invoices.ts
cron.on("send-invoices", async (ctx) => {
  await sendInvoices(ctx.env.db);
  return { ok: true };
});
```

### manifest()

Return the normalized manifest. Defaults are applied, jobs are sorted by name, headers are key-sorted. This is what `cronix apply` consumes.

```ts
const m = cron.manifest();
// { version: 1, app: "billing-service", jobs: [...] }
```

## Handler context (ctx)

Per-fire context handed to your handler. Common fields are top-level; the fire-time triplet lives under `ctx.meta`.

| Field | Type | Notes |
|---|---|---|
| `app` | `string` | The app id from `createCron`. |
| `name` | `string` | The job name being triggered. |
| `runId` | `string` | Unique id for this fire. Use it as a dedup key. |
| `attempt` | `number` | 1-based attempt counter. `>1` means a retry. |
| `body` | `Uint8Array` | Raw request bytes. |
| `headers` | `Record<string, string>` | Lower-cased, single-valued. |
| `text()` | `() => string` | Lazy UTF-8 decode of `body`. |
| `json<T>()` | `<T>() => T` | Lazy JSON parse of `body`. |
| `meta.fireTime` | `Date \| null` | Scheduled fire time. May be null for manual triggers. |
| `meta.fireTimeActual` | `Date \| null` | Actual moment the backend dispatched. |
| `meta.previousSuccessTime` | `Date \| null` | Last successful run, if the backend tracks it. |
| `env` | `Bindings` | App-scoped, supplied at `createCron`. |
| `var` | `Variables` | Per-fire, supplied at `cron.handle({vars})`. |
| `set(key, val)` | `(k, v) => void` | Mutate a per-fire variable. Mirrors Hono's `c.set`. |
| `get(key)` | `(k) => v` | Read a per-fire variable. Mirrors Hono's `c.get`. |

Handlers return a `HandlerResult`:

```ts
type HandlerResult = { ok: boolean; status?: number; body?: string | Uint8Array };
```

`status` defaults to `200` on `ok: true` and `500` on `ok: false`.

## Verification verdicts

`verifyManifest` returns:

```ts
type VerifyManifestResult =
  | { ok: true; secretIndex: number }
  | { ok: false; status: number; code: string; message: string; toResponse(): Response };
```

`verifyTrigger` returns:

```ts
type VerifyTriggerResult =
  | { ok: true; secretIndex: number; ctx: JobContext; run: () => Promise<HandlerResult> }
  | { ok: false; status: number; code: string; message: string; toResponse(): Response };
```

`secretIndex` is the position of the matching secret in the resolved secret list — useful for logging which key handled a fire during rotation. Failure verdicts carry `toResponse()` so a route can `return r.toResponse()` without assembling the JSON body by hand.

Failure codes you may see: `MissingSignature`, `MalformedHeader`, `StaleTimestamp`, `SignatureMismatch`, `BadMethod`, `BadPath`, `UnknownJob`.

## Validation helpers

### validateSchedule(expr)

Synchronously validate a schedule expression. Returns `null` on success or a human error message on failure. The same validator runs inside `register()`.

```ts
import { validateSchedule } from "@awbx/cronix-sdk";

validateSchedule("0 * * * *"); // null
validateSchedule("@hourly");   // null
validateSchedule("nope");      // "schedule must be 5 cron fields or a documented shortcut, got 1 field(s)"
```

### Header constants

Re-exported wire-format header names. Use these instead of typing the strings:

```ts
import {
  HeaderSignature,        // "X-Cron-Signature"
  HeaderRunId,            // "X-Cron-Run-Id"
  HeaderScheduleName,     // "X-Cron-Schedule-Name"
  HeaderFireTime,         // "X-Cron-Fire-Time"
  HeaderFireTimeActual,   // "X-Cron-Fire-Time-Actual"
  HeaderAttempt,          // "X-Cron-Attempt"
  HeaderPreviousSuccessTime, // "X-Cron-Previous-Success-Time"
} from "@awbx/cronix-sdk";
```

### Path constants

```ts
import { MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
// MANIFEST_PATH        === "/.well-known/cron-manifest"
// TRIGGER_PATH_PREFIX  === "/api/v1/scheduled/"
```

## Three integration tiers

Pick the tier that matches your style. All three coexist on the same `cron` instance.

### Tier 1 — zero glue (`cron.handle`)

One route, one call. The SDK routes manifest vs trigger and returns a fully-formed `Response`.

```ts
import { Hono } from "hono";
import { createCron } from "@awbx/cronix-sdk";

const cron = createCron({ app: "billing", baseUrl, secret: process.env.CRON_SECRET! });
cron.register({ name: "reconcile", schedule: "@hourly", handler: async () => ({ ok: true }) });

const app = new Hono();
app.all("*", (c) => cron.handle(c.req.raw));
export default app;
```

### Tier 2 — explicit verify and dispatch

You own the response shaping. Useful for logging, metrics, multi-tenant routing.

```ts
app.get(MANIFEST_PATH, async (c) => {
  const r = await cron.verifyManifest(c.req.raw);
  if (!r.ok) return r.toResponse();
  return Response.json(cron.manifest());
});

app.post(`${TRIGGER_PATH_PREFIX}:name`, async (c) => {
  const r = await cron.verifyTrigger(c.req.raw);
  if (!r.ok) return r.toResponse();
  console.log(`fire ${r.ctx.name} run=${r.ctx.runId}`);
  const out = await r.run();
  return new Response(out.body ?? null, { status: out.status ?? 200 });
});
```

### Tier 3 — late handler binding

Declare jobs centrally, bind handlers from the modules that own the work.

```ts
// jobs.ts
export const cron = createCron({ app, baseUrl, secret });
cron.register({ name: "send-invoices", schedule: "@daily" });
cron.register({ name: "reconcile",     schedule: "@hourly" });

// modules/invoices.ts
import { cron } from "../jobs";
cron.on("send-invoices", async (ctx) => {
  await sendInvoices(ctx.env.db);
  return { ok: true };
});
```

If a trigger arrives for a job whose handler isn't bound yet, the SDK responds `503 NoHandler` so the backend can retry once your code finishes loading.

## See also

- [Extension points](/cronix/sdk/extension-points/) — `skipVerify`, hooks, custom error responses, standalone verify utilities, replay-window override.
- [Manifest reference](/cronix/concepts/manifest/) — what `cron.manifest()` produces and what `cronix apply` consumes.
- [Framework adapters](/cronix/sdk/adapters/) — Express, Fastify, Koa, NestJS bridges.
- [Go SDK](/cronix/sdk/go/) — HMAC verification for Go services.
- [`cronix apply`](/cronix/cli/apply/) — push the manifest to a backend.
