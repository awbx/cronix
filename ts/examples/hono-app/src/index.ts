import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { serve } from "@hono/node-server";
import { Hono } from "hono";

// Tier 1 — zero glue. cron.handle(req) does manifest fetch and trigger
// dispatch internally and returns a fully-formed Response. All your route
// has to do is hand the Web Request over.

const cron = createCron({
  app: "billing-service",
  baseUrl: globalThis.process?.env?.PUBLIC_URL ?? "http://localhost:3000",
  secret: globalThis.process?.env?.CRON_SECRET ?? "whsec_dev_primary",
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

const app = new Hono();

app.all(MANIFEST_PATH, (c) => cron.handle(c.req.raw));
app.all(`${TRIGGER_PATH_PREFIX}:name`, (c) => cron.handle(c.req.raw));

const port = Number(globalThis.process?.env?.PORT ?? 3000);
if (typeof globalThis.process !== "undefined") {
  serve({ fetch: app.fetch, port });
  console.log(`hono example up on :${port}`);
}

export default app;
