import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";
import { MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "../core/registry.js";
import type { CronEnv, CronInstance, VerifyHandleOptions } from "../core/types.js";

export type FastifyMountOptions<E extends CronEnv> = {
  /** Per-fire variables derived from the Fastify request. Runs once per fire. */
  vars?: (req: FastifyRequest) => VerifyHandleOptions<E>["vars"];
  /** Override the default manifest path (`/.well-known/cron-manifest`). */
  manifestPath?: string;
  /** Override the default trigger path pattern (`/api/v1/scheduled/:name`). */
  triggerPath?: string;
  /**
   * Skip installing the wildcard raw-body parser. Set `true` if your app
   * already replaces parsers and captures Buffer bodies. Default `false` —
   * the adapter installs a parser scoped to its own routes only.
   */
  skipRawBodyParser?: boolean;
};

/**
 * Mount cronix routes on a Fastify app in one line. Wires the manifest
 * fetch and trigger paths to `cron.handle`, with the Fastify req/reply
 * lifted to a Web `Request`/`Response` internally.
 *
 * Fastify's default JSON parser would consume the body before signature
 * verification, so the adapter installs a wildcard `parseAs: "buffer"`
 * parser to keep the bytes-as-sent. Set `skipRawBodyParser: true` if your
 * app already does this.
 *
 * ```ts
 * import Fastify from "fastify";
 * import { mount } from "@awbx/cronix-sdk/fastify";
 *
 * const app = Fastify();
 * mount(app, cron, { vars: () => ({ traceId: crypto.randomUUID() }) });
 * app.listen({ port: 3000 });
 * ```
 */
export function mount<E extends CronEnv>(
  app: FastifyInstance,
  cron: CronInstance<E>,
  opts: FastifyMountOptions<E> = {},
): void {
  const manifestPath = opts.manifestPath ?? MANIFEST_PATH;
  const triggerPath = opts.triggerPath ?? `${TRIGGER_PATH_PREFIX}:name`;
  if (!opts.skipRawBodyParser) {
    app.removeAllContentTypeParsers();
    app.addContentTypeParser("*", { parseAs: "buffer" }, (_req, body, done) => done(null, body));
  }
  const handler = makeHandler(cron, opts);
  app.all(manifestPath, handler);
  app.all(triggerPath, handler);
}

function makeHandler<E extends CronEnv>(cron: CronInstance<E>, opts: FastifyMountOptions<E>) {
  return async (req: FastifyRequest, reply: FastifyReply) => {
    const init: RequestInit = {
      method: req.method,
      headers: req.headers as Record<string, string>,
    };
    if (req.method !== "GET" && req.body instanceof Buffer) init.body = req.body;
    const url = `http://${req.headers.host ?? "localhost"}${req.url}`;
    const webReq = new globalThis.Request(url, init);
    const handleOpts = opts.vars ? ({ vars: opts.vars(req) } as VerifyHandleOptions<E>) : undefined;
    const webRes = await cron.handle(webReq, handleOpts);
    reply.code(webRes.status);
    webRes.headers.forEach((v, k) => {
      reply.header(k, v);
    });
    return reply.send(Buffer.from(await webRes.arrayBuffer()));
  };
}
