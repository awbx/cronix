import type { FastifyInstance, FastifyReply, FastifyRequest, RouteHandlerMethod } from "fastify";

export type FetchHandler = (req: Request) => Response | Promise<Response>;

/**
 * Install a wildcard `parseAs: "buffer"` content-type parser so cronix
 * routes receive the bytes-as-sent (Fastify's default JSON parser would
 * consume the body before HMAC verification). Call once per app, before
 * registering any cronix routes.
 *
 * ```ts
 * import { handle, rawBody } from "@awbx/cronix-adapter-fastify";
 *
 * const app = Fastify();
 * rawBody(app);
 * app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
 * ```
 */
export function rawBody(app: FastifyInstance): void {
  app.removeAllContentTypeParsers();
  app.addContentTypeParser("*", { parseAs: "buffer" }, (_req, body, done) => done(null, body));
}

/**
 * Lift a Web Fetch handler to a Fastify route handler. Turns the Fastify
 * req into a Web `Request`, runs the handler, and pipes the `Response`
 * back to the Fastify reply.
 *
 * `fn` is any `(req: Request) => Response | Promise<Response>` — usually
 * `cron.handle`, but you can wrap it for logging, auth, routing across
 * multiple cron instances, etc.
 *
 * ```ts
 * app.all(MANIFEST_PATH, handle((req) => cron.handle(req)));
 * app.all(`${TRIGGER_PATH_PREFIX}:name`, handle((req) =>
 *   cron.handle(req, { vars: { traceId: crypto.randomUUID() } }),
 * ));
 * ```
 */
export function handle(fn: FetchHandler): RouteHandlerMethod {
  return async (req: FastifyRequest, reply: FastifyReply) => {
    const init: RequestInit = {
      method: req.method,
      headers: req.headers as Record<string, string>,
    };
    if (req.method !== "GET" && req.body instanceof Buffer) init.body = req.body;
    const url = `http://${req.headers.host ?? "localhost"}${req.url}`;
    const webReq = new globalThis.Request(url, init);
    const webRes = await fn(webReq);
    reply.code(webRes.status);
    webRes.headers.forEach((v, k) => {
      reply.header(k, v);
    });
    return reply.send(Buffer.from(await webRes.arrayBuffer()));
  };
}
