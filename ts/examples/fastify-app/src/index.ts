import { handle, rawBody } from "@awbx/cronix-adapter-fastify";
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import Fastify from "fastify";

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

const app = Fastify({ logger: true });
rawBody(app);
app.all(
  MANIFEST_PATH,
  handle((req) => cron.handle(req)),
);
app.all(
  `${TRIGGER_PATH_PREFIX}:name`,
  handle((req) => cron.handle(req, { vars: { traceId: crypto.randomUUID() } })),
);

const port = Number(process.env.PORT ?? 3000);
app.listen({ port, host: "0.0.0.0" }).catch((err) => {
  app.log.error(err);
  process.exit(1);
});
