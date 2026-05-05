import type { Context, Middleware } from "koa";

export type FetchHandler = (req: Request) => Response | Promise<Response>;

/**
 * Lift a Web Fetch handler to Koa middleware. Reads the raw request bytes
 * from the underlying node stream, runs the handler, and writes the Web
 * `Response` back to the Koa context.
 *
 * Mount cronix routes **before** any body-parser middleware (e.g.
 * `koa-bodyparser`). HMAC verification needs the bytes-as-sent — once
 * a parser consumes the stream, we can't recover the canonical bytes.
 * If you must run a parser earlier, configure it to expose `rawBody`
 * on `ctx.request` (koa-bodyparser does this) and the adapter picks
 * it up automatically.
 *
 * ```ts
 * import Koa from "koa";
 * import Router from "@koa/router";
 * import { handle } from "@awbx/cronix-adapter-koa";
 * import { MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
 *
 * const app = new Koa();
 * const router = new Router();
 * router.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
 * router.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) =>
 *   cron.handle(req, { vars: { traceId: crypto.randomUUID() } }),
 * ));
 * app.use(router.routes());
 * ```
 */
export function handle(fn: FetchHandler): Middleware {
  return async (ctx: Context) => {
    const init: RequestInit = {
      method: ctx.method,
      headers: ctx.headers as Record<string, string>,
    };
    const body = await readRawBody(ctx);
    if (body) init.body = body as BodyInit;
    const url = `http://${ctx.headers.host ?? "localhost"}${ctx.originalUrl}`;
    const webReq = new globalThis.Request(url, init);
    const webRes = await fn(webReq);
    ctx.status = webRes.status;
    webRes.headers.forEach((v, k) => {
      ctx.set(k, v);
    });
    ctx.body = Buffer.from(await webRes.arrayBuffer());
  };
}

async function readRawBody(ctx: Context): Promise<Buffer | null> {
  if (ctx.method === "GET" || ctx.method === "HEAD") return null;
  const stashed = (ctx.request as { rawBody?: unknown }).rawBody;
  if (stashed instanceof Buffer) return stashed;
  if (typeof stashed === "string") return Buffer.from(stashed);
  if (!ctx.req.readable) return null;
  return new Promise<Buffer>((resolve, reject) => {
    const chunks: Buffer[] = [];
    ctx.req.on("data", (c: Buffer) => chunks.push(c));
    ctx.req.on("end", () => resolve(Buffer.concat(chunks)));
    ctx.req.on("error", reject);
  });
}
