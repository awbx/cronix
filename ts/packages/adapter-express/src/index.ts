import type { Request as ExpressRequest, Response as ExpressResponse, RequestHandler } from "express";
import express from "express";

export type FetchHandler = (req: Request) => Response | Promise<Response>;

export type ExpressHandleOptions = {
  /** Body-size limit forwarded to `express.raw`. Default `10mb`. */
  limit?: string;
};

/**
 * Lift a Web Fetch handler to an Express middleware. Captures the raw
 * request body (so HMAC verification has the bytes-as-sent), turns the
 * Express req into a Web `Request`, runs the handler, and pipes the
 * `Response` back to the Express reply.
 *
 * ```ts
 * import { handle } from "@awbx/cronix-adapter-express";
 *
 * app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
 * app.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) =>
 *   cron.handle(req, { vars: { traceId: crypto.randomUUID() } }),
 * ));
 * ```
 *
 * `fn` is any `(req: Request) => Response | Promise<Response>` — usually
 * `cron.handle`, but you can wrap it for logging, auth, routing across
 * multiple cron instances, etc.
 */
export function handle(fn: FetchHandler, opts: ExpressHandleOptions = {}): RequestHandler {
  const raw = express.raw({ type: "*/*", limit: opts.limit ?? "10mb" });
  return (req, res, next) => {
    raw(req, res, (err) => {
      if (err) return next(err);
      lift(fn, req, res).catch(next);
    });
  };
}

async function lift(fn: FetchHandler, req: ExpressRequest, res: ExpressResponse): Promise<void> {
  const init: RequestInit = {
    method: req.method,
    headers: req.headers as Record<string, string>,
  };
  if (req.method !== "GET" && req.body instanceof Buffer) init.body = req.body;
  const url = `http://${req.headers.host ?? "localhost"}${req.originalUrl}`;
  const webReq = new globalThis.Request(url, init);
  const webRes = await fn(webReq);
  res.status(webRes.status);
  webRes.headers.forEach((v, k) => {
    res.setHeader(k, v);
  });
  res.end(Buffer.from(await webRes.arrayBuffer()));
}
