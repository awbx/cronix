import { createCron, MANIFEST_PATH } from "@awbx/cronix-sdk";
import Fastify from "fastify";

// Tier 2 — explicit verifyManifest / verifyTrigger. Useful when you want
// logging, metrics, or auth-attribution between verify and run.

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

// Replace fastify's built-in JSON parser so signature verification gets the
// raw bytes-as-sent. Without this, application/json bodies get parsed into
// objects and we can't recover the exact bytes.
app.removeAllContentTypeParsers();
app.addContentTypeParser("*", { parseAs: "buffer" }, (_req, body, done) => {
  done(null, body);
});

app.get(MANIFEST_PATH, async (req, reply) => {
  const r = await cron.verifyManifest({
    method: req.method,
    path: req.url.split("?")[0] ?? req.url,
    body: new Uint8Array(0),
    headers: req.headers as Record<string, string | string[] | undefined>,
  });
  if (!r.ok) return reply.code(r.status).send({ code: r.code, message: r.message });
  return reply.code(200).send(cron.manifest());
});

app.post("/api/v1/scheduled/:name", async (req, reply) => {
  const buf = req.body instanceof Buffer ? req.body : Buffer.alloc(0);
  const r = await cron.verifyTrigger({
    method: req.method,
    path: req.url.split("?")[0] ?? req.url,
    body: new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength),
    headers: req.headers as Record<string, string | string[] | undefined>,
  });
  if (!r.ok) return reply.code(r.status).send({ code: r.code, message: r.message });
  app.log.info({ job: r.ctx.name, runId: r.ctx.runId, attempt: r.ctx.attempt }, "cron fired");
  const out = await r.run();
  const status = out.status ?? (out.ok ? 200 : 500);
  return out.body !== undefined ? reply.code(status).send(out.body) : reply.code(status).send();
});

const port = Number(process.env.PORT ?? 3000);
app.listen({ port, host: "0.0.0.0" }).catch((err) => {
  app.log.error(err);
  process.exit(1);
});
