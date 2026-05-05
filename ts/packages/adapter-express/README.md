# @awbx/cronix-adapter-express

Express adapter for [`@awbx/cronix-sdk`](../sdk). Lifts a Web Fetch handler — typically `cron.handle` — into Express middleware while preserving the bytes-as-sent so HMAC verification doesn't break.

## Install

```bash
pnpm add @awbx/cronix-sdk @awbx/cronix-adapter-express
```

`express` is a peer dep — bring your own.

## Usage

```ts
import express from "express";
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { handle } from "@awbx/cronix-adapter-express";

const cron = createCron({
  app: "billing",
  baseUrl: "https://billing.internal.example.com",
  secret: process.env.CRON_SECRET!,
});

cron.register({ name: "reconcile", schedule: "*/15 * * * *" }, async (ctx) => {
  // ...
});

const app = express();
app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
app.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));
app.listen(3000);
```

## API

### `handle(fn, opts?)`

Returns an Express `RequestHandler` that:
1. Captures the raw request body via `express.raw({ type: "*/*" })`.
2. Reconstructs a Web `Request` from the Express req (method, headers, raw body, full URL).
3. Awaits `fn(request)`.
4. Pipes the returned `Response` back to the Express reply.

`fn` is any `(req: Request) => Response | Promise<Response>` — usually `cron.handle`, but you can wrap it for logging, auth, fan-out across multiple cron instances, etc.

**Options**

| Option  | Default  | Description                                          |
|---------|----------|------------------------------------------------------|
| `limit` | `"10mb"` | Body-size limit forwarded to `express.raw({ limit })`. |

## Why a dedicated adapter?

cronix verifies requests with HMAC over the canonical bytes. Express's default JSON / urlencoded parsers consume the request stream and lose the original byte sequence, so signed requests fail verification. This adapter installs `express.raw` per-route, scoped to the cronix endpoints, leaving the rest of your app's parsing untouched.

## NestJS

NestJS apps running on the default Express platform should use [`@awbx/cronix-adapter-nest`](../adapter-nest), which thin-wraps this adapter. NestFastify apps should use [`@awbx/cronix-adapter-fastify`](../adapter-fastify) directly.
