import { createCron, MANIFEST_PATH } from "@awbx/cronix-sdk";
import express, { type Request, type Response } from "express";

const cron = createCron({
  app: "billing-service",
  baseUrl: process.env.PUBLIC_URL ?? "http://localhost:3000",
  secret: [process.env.CRON_SECRET_V2 ?? "whsec_dev_primary", process.env.CRON_SECRET_V1 ?? "whsec_dev_old"],
});

cron.register({
  name: "reconcile-payments",
  schedule: "*/15 * * * *",
  policy: { concurrency: "Forbid", timeout_seconds: 120 },
  auth: { secret_refs: ["env:CRON_SECRET"] },
  handler: async (ctx) => {
    console.log(`[cron] ${ctx.name} run=${ctx.runId} attempt=${ctx.attempt}`);
    return { ok: true, status: 202 };
  },
});

cron.register({
  name: "settle-invoices",
  schedules: ["0 2 * * *", "0 14 * * 1-5"],
  timezone: "Europe/Paris",
  handler: async (ctx) => {
    console.log(`[cron] ${ctx.name} run=${ctx.runId}`);
    return { ok: true };
  },
});

const app = express();

app.get(MANIFEST_PATH, async (req: Request, res: Response) => {
  const result = await cron.verify({
    kind: "manifest",
    method: req.method,
    path: req.originalUrl.split("?")[0] ?? req.path,
    body: new Uint8Array(0),
    headers: req.headers as Record<string, string | string[] | undefined>,
  });
  if (!result.ok) {
    res.status(result.status).json({ code: result.code, message: result.message });
    return;
  }
  res.json(cron.manifest());
});

app.post(
  "/api/v1/scheduled/:name",
  express.raw({ type: "*/*", limit: "10mb" }),
  async (req: Request, res: Response) => {
    const body =
      req.body instanceof Buffer
        ? new Uint8Array(req.body.buffer, req.body.byteOffset, req.body.byteLength)
        : new Uint8Array(0);
    const result = await cron.verify({
      kind: "trigger",
      method: req.method,
      path: req.originalUrl.split("?")[0] ?? req.path,
      body,
      headers: req.headers as Record<string, string | string[] | undefined>,
    });
    if (!result.ok) {
      res.status(result.status).json({ code: result.code, message: result.message });
      return;
    }
    if (result.kind !== "trigger") {
      res.status(500).json({ code: "InternalError", message: "expected trigger outcome" });
      return;
    }
    const out = await result.run();
    const status = out.status ?? (out.ok ? 200 : 500);
    if (out.body !== undefined) res.status(status).send(out.body);
    else res.status(status).end();
  },
);

const port = Number(process.env.PORT ?? 3000);
app.listen(port, () => console.log(`express example up on :${port}`));
