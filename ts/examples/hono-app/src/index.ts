import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { serve } from "@hono/node-server";
import { Hono } from "hono";

// Demonstrates the Hono-style generic env: Bindings (app-scoped, set at
// createCron) and Variables (per-fire, set at cron.handle). Both flow into
// the handler with full type inference: ctx.env.<key> and ctx.var.<key>.

type CronEnv = {
  Bindings: {
    db: { query(sql: string): Promise<unknown> };
    logger: { info(msg: string): void };
  };
  Variables: {
    traceId: string;
  };
};

const fakeDb = { query: async (sql: string) => ({ sql, rows: 0 }) };

const cron = createCron<CronEnv>({
  app: "billing-service",
  baseUrl: globalThis.process?.env?.PUBLIC_URL ?? "http://localhost:3000",
  secret: globalThis.process?.env?.CRON_SECRET ?? "whsec_dev_primary",
  env: { db: fakeDb, logger: console },
});

cron.register({
  name: "reconcile-payments",
  schedule: "*/15 * * * *",
  auth: { secret_refs: ["env:CRON_SECRET"] },
  handler: async (ctx) => {
    ctx.env.logger.info(`[cron] ${ctx.name} run=${ctx.runId} trace=${ctx.var.traceId}`);
    await ctx.env.db.query("UPDATE payments SET ...");
    return { ok: true };
  },
});

const app = new Hono();

app.all(MANIFEST_PATH, (c) => cron.handle(c.req.raw));
app.all(`${TRIGGER_PATH_PREFIX}:name`, (c) => cron.handle(c.req.raw, { vars: { traceId: crypto.randomUUID() } }));

const port = Number(globalThis.process?.env?.PORT ?? 3000);
if (typeof globalThis.process !== "undefined") {
  serve({ fetch: app.fetch, port });
  console.log(`hono example up on :${port}`);
}

export default app;
