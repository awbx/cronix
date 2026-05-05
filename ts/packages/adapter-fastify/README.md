# @awbx/cronix-adapter-fastify

Fastify adapter for [`@awbx/cronix-sdk`](../sdk). Lifts a Web Fetch handler into a Fastify route handler with a raw-body content-type parser so HMAC verification has the canonical bytes.

## Install

```bash
pnpm add @awbx/cronix-sdk @awbx/cronix-adapter-fastify
```

`fastify` is a peer dep — bring your own.

## Usage

```ts
import Fastify from "fastify";
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { handle, rawBody } from "@awbx/cronix-adapter-fastify";

const cron = createCron({
  app: "billing",
  baseUrl: "https://billing.internal.example.com",
  secret: process.env.CRON_SECRET!,
});

cron.register({ name: "reconcile", schedule: "*/15 * * * *" }, async (ctx) => {
  // ...
});

const app = Fastify();
rawBody(app);                                                  // ← required, register first
app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
app.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));
await app.listen({ port: 3000 });
```

## API

### `rawBody(app)`

Replaces every content-type parser on the Fastify instance with a wildcard `parseAs: "buffer"` parser, so request bodies arrive as `Buffer` and HMAC verification sees the bytes-as-sent. Call **once per app**, before registering cronix routes.

If you need Fastify's built-in JSON parsing for non-cronix routes, register cronix on a sub-app via `app.register` and call `rawBody` only on the sub-app.

### `handle(fn)`

Returns a `RouteHandlerMethod` that:
1. Turns the Fastify `req` into a Web `Request` (method, headers, raw body buffer, full URL).
2. Awaits `fn(request)`.
3. Pipes the returned `Response` back to the Fastify reply.

`fn` is any `(req: Request) => Response | Promise<Response>` — usually `cron.handle`, but you can wrap it for logging, auth, fan-out across multiple cron instances, etc.

## NestFastify

NestJS apps running on Fastify (`@nestjs/platform-fastify`) can use this adapter directly — the underlying `FastifyRequest` / `FastifyReply` shapes match what the adapter expects.
