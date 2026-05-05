# @awbx/cronix-adapter-koa

Koa adapter for [`@awbx/cronix-sdk`](../sdk). Lifts a Web Fetch handler into Koa middleware while preserving the bytes-as-sent for HMAC verification.

## Install

```bash
pnpm add @awbx/cronix-sdk @awbx/cronix-adapter-koa
```

`koa` is a peer dep — bring your own.

## Usage

```ts
import Koa from "koa";
import Router from "@koa/router";
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { handle } from "@awbx/cronix-adapter-koa";

const cron = createCron({
  app: "billing",
  baseUrl: "https://billing.internal.example.com",
  secret: process.env.CRON_SECRET!,
});

cron.register({ name: "reconcile", schedule: "*/15 * * * *" }, async (ctx) => {
  // ...
});

const app = new Koa();
const router = new Router();
router.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
router.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));
app.use(router.routes());
app.listen(3000);
```

## API

### `handle(fn)`

Returns a Koa `Middleware` that:
1. Reads the raw request bytes from the underlying node stream.
2. Reconstructs a Web `Request` (method, headers, body, full URL).
3. Awaits `fn(request)`.
4. Writes the returned `Response` back to the Koa context.

`fn` is any `(req: Request) => Response | Promise<Response>` — usually `cron.handle`, but you can wrap it for logging, auth, fan-out across multiple cron instances, etc.

## Body parser ordering

Mount cronix routes **before** any body-parser middleware (`koa-bodyparser`, `@koa/bodyparser`, etc.). Once a parser consumes the request stream, the canonical bytes are gone and HMAC verification fails.

If a parser must run earlier — for example, you mount cronix as a sub-router after `app.use(bodyparser())` — configure that parser to expose `rawBody` on `ctx.request`. `koa-bodyparser` does this by default; the adapter detects it and uses it transparently.
