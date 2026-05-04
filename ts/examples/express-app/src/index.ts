import { createCron, MANIFEST_PATH } from "@awbx/cronix-sdk";
import express, { type Request, type Response } from "express";
import { reconcilePayments, settleInvoices } from "./jobs.js";

// Tier 2 — late handler binding. Jobs are declared up top with no handler;
// handlers live in ./jobs.ts and bind via cron.on(name, handler). Useful
// when handlers are large enough to deserve their own files.

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
});
cron.register({
  name: "settle-invoices",
  schedules: ["0 2 * * *", "0 14 * * 1-5"],
  timezone: "Europe/Paris",
  auth: { secret_refs: ["env:CRON_SECRET"] },
});

cron.on("reconcile-payments", reconcilePayments);
cron.on("settle-invoices", settleInvoices);

const app = express();

app.get(MANIFEST_PATH, async (req: Request, res: Response) => {
  const r = await cron.verifyManifest({
    method: req.method,
    path: req.originalUrl.split("?")[0] ?? req.path,
    body: new Uint8Array(0),
    headers: req.headers as Record<string, string | string[] | undefined>,
  });
  if (!r.ok) return res.status(r.status).json({ code: r.code, message: r.message });
  res.json(cron.manifest());
});

app.post(
  "/api/v1/scheduled/:name",
  express.raw({ type: "*/*", limit: "10mb" }),
  async (req: Request, res: Response) => {
    const buf = req.body instanceof Buffer ? req.body : Buffer.alloc(0);
    const r = await cron.verifyTrigger({
      method: req.method,
      path: req.originalUrl.split("?")[0] ?? req.path,
      body: new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength),
      headers: req.headers as Record<string, string | string[] | undefined>,
    });
    if (!r.ok) return res.status(r.status).json({ code: r.code, message: r.message });
    const out = await r.run();
    const status = out.status ?? (out.ok ? 200 : 500);
    out.body !== undefined ? res.status(status).send(out.body) : res.status(status).end();
  },
);

const port = Number(process.env.PORT ?? 3000);
app.listen(port, () => console.log(`express example up on :${port}`));
