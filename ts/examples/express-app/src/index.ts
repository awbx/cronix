import { type CronInstance, createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import express, { type Request, type Response } from "express";

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

const app = express();
const wrap = liftToFetch(cron);

app.all(MANIFEST_PATH, express.raw({ type: "*/*" }), wrap);
app.all(`${TRIGGER_PATH_PREFIX}:name`, express.raw({ type: "*/*" }), wrap);

// Adapt Express req/res to Web Fetch so we can hand the request to cron.handle().
function liftToFetch(cron: CronInstance) {
  return async (req: Request, res: Response) => {
    const init: RequestInit = {
      method: req.method,
      headers: req.headers as Record<string, string>,
    };
    if (req.method !== "GET" && req.body instanceof Buffer) init.body = req.body;
    const webReq = new globalThis.Request(`http://${req.headers.host}${req.originalUrl}`, init);
    const webRes = await cron.handle(webReq);
    res.status(webRes.status);
    webRes.headers.forEach((v, k) => {
      res.setHeader(k, v);
    });
    res.end(Buffer.from(await webRes.arrayBuffer()));
  };
}

const port = Number(process.env.PORT ?? 3000);
app.listen(port, () => console.log(`express example up on :${port}`));
