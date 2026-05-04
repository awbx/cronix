import { type CronInstance, createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import Fastify, { type FastifyReply, type FastifyRequest } from "fastify";

const cron = createCron({
  app: "billing-service",
  baseUrl: process.env.PUBLIC_URL ?? "http://localhost:3000",
  secret: process.env.CRON_SECRET ?? "whsec_dev_primary",
});

cron.register({
  name: "reconcile-payments",
  schedule: "*/15 * * * *",
  auth: { secret_refs: ["env:CRON_SECRET"] },
  handler: async (ctx) => {
    console.log(`[cron] ${ctx.name} run=${ctx.runId}`);
    return { ok: true };
  },
});

const app = Fastify({ logger: true });

// Capture raw bytes-as-sent so signature verification has the exact body.
app.removeAllContentTypeParsers();
app.addContentTypeParser("*", { parseAs: "buffer" }, (_req, body, done) => done(null, body));

const wrap = liftToFetch(cron);
app.all(MANIFEST_PATH, wrap);
app.all(`${TRIGGER_PATH_PREFIX}:name`, wrap);

// Adapt Fastify req/reply to Web Fetch so we can hand the request to cron.handle().
function liftToFetch(cron: CronInstance) {
  return async (req: FastifyRequest, reply: FastifyReply) => {
    const init: RequestInit = {
      method: req.method,
      headers: req.headers as Record<string, string>,
    };
    if (req.method !== "GET" && req.body instanceof Buffer) init.body = req.body;
    const webReq = new globalThis.Request(`http://${req.headers.host}${req.url}`, init);
    const webRes = await cron.handle(webReq);
    reply.code(webRes.status);
    webRes.headers.forEach((v, k) => {
      reply.header(k, v);
    });
    return reply.send(Buffer.from(await webRes.arrayBuffer()));
  };
}

const port = Number(process.env.PORT ?? 3000);
app.listen({ port, host: "0.0.0.0" }).catch((err) => {
  app.log.error(err);
  process.exit(1);
});
