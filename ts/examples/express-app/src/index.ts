import { createCron } from "@awbx/cronix-sdk";
import { mount } from "@awbx/cronix-sdk/express";
import express from "express";

type CronEnv = {
  Bindings: { logger: { info(m: string): void } };
  Variables: { traceId: string };
};

const cron = createCron<CronEnv>({
  app: "billing-service",
  baseUrl: process.env.PUBLIC_URL ?? "http://localhost:3000",
  secret: process.env.CRON_SECRET ?? "whsec_dev_primary",
  env: { logger: console },
});

cron.register({
  name: "reconcile-payments",
  schedule: "*/15 * * * *",
  auth: { secret_refs: ["env:CRON_SECRET"] },
  handler: async (ctx) => {
    ctx.env.logger.info(`[cron] ${ctx.name} run=${ctx.runId} trace=${ctx.var.traceId}`);
    return { ok: true };
  },
});

const app = express();
mount(app, cron, { vars: () => ({ traceId: crypto.randomUUID() }) });

const port = Number(process.env.PORT ?? 3000);
app.listen(port, () => console.log(`express example up on :${port}`));
