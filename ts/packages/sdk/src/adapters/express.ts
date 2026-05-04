import type { Application, Request as ExpressRequest, Response as ExpressResponse, RequestHandler } from "express";
import express from "express";
import { MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "../core/registry.js";
import type { CronEnv, CronInstance, VerifyHandleOptions } from "../core/types.js";

export type ExpressMountOptions<E extends CronEnv> = {
  /** Per-fire variables derived from the Express request. Runs once per fire. */
  vars?: (req: ExpressRequest) => VerifyHandleOptions<E>["vars"];
  /** Override the default manifest path (`/.well-known/cron-manifest`). */
  manifestPath?: string;
  /** Override the default trigger path pattern (`/api/v1/scheduled/:name`). */
  triggerPath?: string;
  /** Body-size limit forwarded to `express.raw`. Default `10mb`. */
  limit?: string;
};

/**
 * Mount cronix routes on an Express app in one line. Wires the manifest
 * fetch and trigger paths to `cron.handle`, with the Express req/res
 * lifted to a Web `Request`/`Response` internally.
 *
 * ```ts
 * import express from "express";
 * import { mount } from "@awbx/cronix-sdk/express";
 *
 * const app = express();
 * mount(app, cron, { vars: () => ({ traceId: crypto.randomUUID() }) });
 * app.listen(3000);
 * ```
 */
export function mount<E extends CronEnv>(
  app: Application,
  cron: CronInstance<E>,
  opts: ExpressMountOptions<E> = {},
): void {
  const manifestPath = opts.manifestPath ?? MANIFEST_PATH;
  const triggerPath = opts.triggerPath ?? `${TRIGGER_PATH_PREFIX}:name`;
  const middlewares = lift(cron, opts);
  app.all(manifestPath, ...middlewares);
  app.all(triggerPath, ...middlewares);
}

/**
 * Lower-level: returns the middleware tuple `[express.raw, handler]` so you
 * can mount the cron routes manually with custom paths or middleware order.
 *
 * ```ts
 * app.all(MY_PATH, ...lift(cron));
 * ```
 */
export function lift<E extends CronEnv>(
  cron: CronInstance<E>,
  opts: ExpressMountOptions<E> = {},
): readonly [RequestHandler, RequestHandler] {
  const limit = opts.limit ?? "10mb";
  return [express.raw({ type: "*/*", limit }), makeHandler(cron, opts)] as const;
}

function makeHandler<E extends CronEnv>(cron: CronInstance<E>, opts: ExpressMountOptions<E>): RequestHandler {
  return async (req: ExpressRequest, res: ExpressResponse) => {
    const init: RequestInit = {
      method: req.method,
      headers: req.headers as Record<string, string>,
    };
    if (req.method !== "GET" && req.body instanceof Buffer) init.body = req.body;
    const url = `http://${req.headers.host ?? "localhost"}${req.originalUrl}`;
    const webReq = new globalThis.Request(url, init);
    const handleOpts = opts.vars ? ({ vars: opts.vars(req) } as VerifyHandleOptions<E>) : undefined;
    const webRes = await cron.handle(webReq, handleOpts);
    res.status(webRes.status);
    webRes.headers.forEach((v, k) => {
      res.setHeader(k, v);
    });
    res.end(Buffer.from(await webRes.arrayBuffer()));
  };
}
