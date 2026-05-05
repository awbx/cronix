import { type ExpressHandleOptions, handle as expressHandle, type FetchHandler } from "@awbx/cronix-adapter-express";

export type { FetchHandler };
export type NestHandleOptions = ExpressHandleOptions;

/**
 * NestJS adapter. Nest runs on Express by default, so the cronix Nest
 * adapter is a thin alias of the Express adapter with Nest-specific
 * setup documented below.
 *
 * Bootstrap with body parsing disabled — HMAC verification needs the
 * raw bytes-as-sent:
 *
 * ```ts
 * import { NestFactory } from "@nestjs/core";
 * import { NestExpressApplication } from "@nestjs/platform-express";
 *
 * const app = await NestFactory.create<NestExpressApplication>(AppModule, {
 *   bodyParser: false,
 * });
 * ```
 *
 * Then either register cronix as Express middleware on the underlying app:
 *
 * ```ts
 * import { handle } from "@awbx/cronix-adapter-nest";
 * import { MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
 *
 * app.use(MANIFEST_PATH, handle((req) => cron.handle(req)));
 * app.use(`${TRIGGER_PATH_PREFIX}:name`, handle((req) =>
 *   cron.handle(req, { vars: { traceId: crypto.randomUUID() } }),
 * ));
 * ```
 *
 * Or invoke from inside a Nest controller method:
 *
 * ```ts
 * import { All, Controller, Next, Req, Res } from "@nestjs/common";
 * import type { NextFunction, Request, Response } from "express";
 * import { handle } from "@awbx/cronix-adapter-nest";
 *
 * @Controller()
 * export class CronController {
 *   private readonly wrap = handle((req) => cron.handle(req));
 *   @All("/.well-known/cron-manifest")
 *   manifest(@Req() req: Request, @Res() res: Response, @Next() next: NextFunction) {
 *     return this.wrap(req, res, next);
 *   }
 * }
 * ```
 *
 * For NestFastify apps, use `@awbx/cronix-adapter-fastify` directly — the
 * underlying Fastify req/reply shapes match.
 */
export const handle = expressHandle;
