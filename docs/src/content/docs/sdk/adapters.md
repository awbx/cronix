---
title: Framework adapters
description: Per-framework helpers for Express, Fastify, Koa, NestJS
---

`@awbx/cronix-sdk` returns a `cron.handle(req)` function that takes a Web Fetch `Request` and returns a Web Fetch `Response`. Frameworks that already speak Web Fetch (Hono, Bun, Workers, Deno, Vercel/Next.js route handlers) can call it directly. Frameworks built on the Node `http` module need a thin bridge — that's what these adapters are.

## Why adapters

The SDK is runtime-agnostic by design: it uses Web Crypto and Web Fetch only, no `node:crypto`, no Express types. That keeps a single codebase running on Node, Bun, Deno, Workers, and Edge — but it means apps on Express, Fastify, Koa, or Nest need a small lift step to:

1. Capture the **raw request body bytes** before any JSON parser consumes them. HMAC verification needs the bytes-as-sent — once a parser has reshaped them, the canonical signed string doesn't match.
2. Construct a Web `Request` from the framework's req shape.
3. Pipe the returned Web `Response` (status, headers, body) back into the framework's reply.

Each adapter is a few dozen lines doing exactly that. The signature is always the same:

```ts
handle((req: Request) => cron.handle(req))
```

You can wrap `cron.handle` with anything you like — logging, metrics, multi-tenant routing — as long as the wrapper returns a `Response`.

## Express

```bash
pnpm add @awbx/cronix-adapter-express
```

```ts
import express from "express";
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { handle } from "@awbx/cronix-adapter-express";

const cron = createCron({ app, baseUrl, secret });
cron.register({ name: "reconcile", schedule: "@hourly", handler: async () => ({ ok: true }) });

const app = express();
app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
app.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) =>
  cron.handle(req, { vars: { traceId: crypto.randomUUID() } }),
));
app.listen(3000);
```

| Option | Type | Default | Notes |
|---|---|---|---|
| `limit` | `string` | `"10mb"` | Body-size limit forwarded to `express.raw`. |

The adapter installs its own `express.raw({ type: "*/*" })` parser scoped to the cron routes — you can use any other body parser globally without affecting it.

## Fastify

```bash
pnpm add @awbx/cronix-adapter-fastify
```

Fastify's default JSON parser would consume the body before HMAC verification runs. Call `rawBody(app)` once before registering cronix routes to install a wildcard `parseAs: "buffer"` parser.

```ts
import Fastify from "fastify";
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { handle, rawBody } from "@awbx/cronix-adapter-fastify";

const cron = createCron({ app, baseUrl, secret });
cron.register({ name: "reconcile", schedule: "@hourly", handler: async () => ({ ok: true }) });

const app = Fastify();
rawBody(app);
app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
app.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));
await app.listen({ port: 3000 });
```

Gotcha: `rawBody(app)` calls `removeAllContentTypeParsers()`. If you need other content-type parsers in the same app, register them after `rawBody` or use a separate Fastify instance for cron routes.

## Koa

```bash
pnpm add @awbx/cronix-adapter-koa
```

Mount cronix routes **before** any body-parser middleware (`koa-bodyparser`, `@koa/multer`, etc.). Once a parser consumes the request stream, the canonical bytes are gone.

```ts
import Koa from "koa";
import Router from "@koa/router";
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { handle } from "@awbx/cronix-adapter-koa";

const cron = createCron({ app, baseUrl, secret });
cron.register({ name: "reconcile", schedule: "@hourly", handler: async () => ({ ok: true }) });

const app = new Koa();
const router = new Router();
router.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
router.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));
app.use(router.routes());
// app.use(bodyParser()); // ← AFTER cron routes, never before
app.listen(3000);
```

If you must run a body parser earlier in the chain, configure it to expose `rawBody` on `ctx.request` (`koa-bodyparser` does this when you pass `{ enableRawBody: true }`) and the adapter picks it up automatically.

## NestJS

```bash
pnpm add @awbx/cronix-adapter-nest
```

NestJS-on-Express runs Express's body parser by default. Bootstrap with `bodyParser: false` so the cronix adapter sees the raw bytes:

```ts
import { NestFactory } from "@nestjs/core";
import { NestExpressApplication } from "@nestjs/platform-express";
import { AppModule } from "./app.module";

const app = await NestFactory.create<NestExpressApplication>(AppModule, {
  bodyParser: false,
});
```

Then mount cronix as Express middleware on the underlying instance:

```ts
import { handle } from "@awbx/cronix-adapter-nest";
import { MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";

app.use(MANIFEST_PATH, handle((req) => cron.handle(req)));
app.use(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));
```

Or invoke from inside a Nest controller:

```ts
import { All, Controller, Next, Req, Res } from "@nestjs/common";
import type { NextFunction, Request, Response } from "express";
import { handle } from "@awbx/cronix-adapter-nest";

@Controller()
export class CronController {
  private readonly wrap = handle((req) => cron.handle(req));

  @All("/.well-known/cron-manifest")
  manifest(@Req() req: Request, @Res() res: Response, @Next() next: NextFunction) {
    return this.wrap(req, res, next);
  }
}
```

For NestFastify apps, install [`@awbx/cronix-adapter-fastify`](#fastify) directly — the underlying Fastify req/reply shapes match.

## No-adapter platforms

Any runtime that natively exposes Web Fetch `Request` / `Response` calls `cron.handle` directly. No bridge needed.

### Hono (canonical Web Fetch demo)

```ts
import { Hono } from "hono";
import { createCron } from "@awbx/cronix-sdk";

const cron = createCron({
  app: "billing",
  baseUrl: "https://billing.example.com",
  secret: process.env.CRON_SECRET!,
});
cron.register({
  name: "reconcile",
  schedule: "@hourly",
  handler: async (ctx) => {
    console.log(`run ${ctx.runId} attempt ${ctx.attempt}`);
    return { ok: true };
  },
});

const app = new Hono();
app.all("*", (c) => cron.handle(c.req.raw, { vars: { traceId: crypto.randomUUID() } }));

export default app;
```

### Bun

```ts
Bun.serve({
  port: 3000,
  fetch: (req) => cron.handle(req),
});
```

### Cloudflare Workers

```ts
export default {
  fetch(req: Request) {
    return cron.handle(req);
  },
};
```

### Vercel / Next.js (App Router)

```ts
// app/[...cronix]/route.ts
import { cron } from "@/lib/cron";

export const GET = (req: Request) => cron.handle(req);
export const POST = (req: Request) => cron.handle(req);
```

Make sure the route is on the **Node** runtime (the default) or the **Edge** runtime — both work because the SDK uses Web Crypto only.

### Deno

```ts
Deno.serve({ port: 3000 }, (req) => cron.handle(req));
```

## See also

- [TypeScript SDK](/cronix/sdk/typescript/) — what `cron.handle` actually does.
- [Manifest reference](/cronix/concepts/manifest/) — the JSON shape these routes serve and accept.
- [`cronix apply`](/cronix/cli/apply/) — fetches the manifest from your app and pushes it to a backend.
