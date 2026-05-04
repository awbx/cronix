import { createCron, MANIFEST_PATH } from "@awbx/cronix-sdk";
import { serve } from "@hono/node-server";
import { Hono } from "hono";

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

app.get(MANIFEST_PATH, async (c) => {
  const url = new URL(c.req.url);
  const result = await cron.verify({
    kind: "manifest",
    method: c.req.method,
    path: url.pathname,
    body: new Uint8Array(0),
    headers: rawHeaders(c.req.raw.headers),
  });
  if (!result.ok) return c.json({ code: result.code, message: result.message }, result.status as 401 | 400 | 404);
  return c.json(cron.manifest());
});

app.post("/api/v1/scheduled/:name", async (c) => {
  const url = new URL(c.req.url);
  const ab = await c.req.arrayBuffer();
  const body = new Uint8Array(ab);
  const result = await cron.verify({
    kind: "trigger",
    method: c.req.method,
    path: url.pathname,
    body,
    headers: rawHeaders(c.req.raw.headers),
  });
  if (!result.ok) return c.json({ code: result.code, message: result.message }, result.status as 401 | 400 | 404);
  if (result.kind !== "trigger") return c.json({ code: "InternalError", message: "expected trigger" }, 500);
  const out = await result.run();
  const status = out.status ?? (out.ok ? 200 : 500);
  return out.body !== undefined ? new Response(out.body as BodyInit, { status }) : new Response(null, { status });
});

function rawHeaders(headers: Headers): Record<string, string | string[] | undefined> {
  const out: Record<string, string | string[] | undefined> = {};
  headers.forEach((v, k) => {
    out[k.toLowerCase()] = v;
  });
  return out;
}

const port = Number(globalThis.process?.env?.PORT ?? 3000);
// Node/Bun launcher. On Workers, export `default app` instead.
if (typeof globalThis.process !== "undefined") {
  serve({ fetch: app.fetch, port });
  console.log(`hono example up on :${port}`);
}

export default app;
