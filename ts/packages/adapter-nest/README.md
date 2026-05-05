# @awbx/cronix-adapter-nest

NestJS adapter for [`@awbx/cronix-sdk`](../sdk). Thin re-export of [`@awbx/cronix-adapter-express`](../adapter-express) with Nest-specific bootstrap docs — Nest runs on Express by default and the same `handle` works as Express middleware or inside a Nest controller.

For NestFastify apps (`@nestjs/platform-fastify`), use [`@awbx/cronix-adapter-fastify`](../adapter-fastify) directly.

## Install

```bash
pnpm add @awbx/cronix-sdk @awbx/cronix-adapter-nest
```

## Bootstrap

Disable Nest's default body parser — HMAC verification needs the canonical bytes:

```ts
import { NestFactory } from "@nestjs/core";
import { NestExpressApplication } from "@nestjs/platform-express";
import { AppModule } from "./app.module";

const app = await NestFactory.create<NestExpressApplication>(AppModule, {
  bodyParser: false,
});
```

## Wiring options

### As Express middleware

```ts
import { handle } from "@awbx/cronix-adapter-nest";
import { MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";

app.use(MANIFEST_PATH, handle((req) => cron.handle(req)));
app.use(`${TRIGGER_PATH_PREFIX}:name`, handle((req) => cron.handle(req)));
```

### Inside a Nest controller

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

## API

Identical to [`@awbx/cronix-adapter-express`](../adapter-express): `handle(fn, opts?)` accepts an optional `{ limit }` to forward to `express.raw`.
