import { describe, expect, it } from "vitest";
import { createCron, HeaderRunId, HeaderSignature, MANIFEST_PATH, sign } from "../src/core/index.js";

const SECRET = "whsec_test_primary_aaaaaaaaaaaaaaaaaaaaaaaaaaa";
const NOW = 1_730_000_000;

const makeInstance = () => {
  const calls: { name: string; runId: string; attempt: number; body: string }[] = [];
  const cron = createCron({
    app: "billing",
    baseUrl: "https://billing.example.com",
    secret: SECRET,
  });
  cron.register({
    name: "reconcile-payments",
    schedule: "*/15 * * * *",
    handler: (ctx) => {
      calls.push({
        name: ctx.name,
        runId: ctx.runId,
        attempt: ctx.attempt,
        body: ctx.body.length === 0 ? "" : ctx.text(),
      });
      return { ok: true, status: 202, body: "accepted" };
    },
  });
  cron.register({
    name: "settle-invoices",
    schedules: ["0 2 * * *", "0 14 * * 1-5"],
    timezone: "Europe/Paris",
    policy: { concurrency: "Forbid", timeout_seconds: 120 },
    handler: () => ({ ok: true }),
  });
  return { cron, calls };
};

const enc = (s: string) => new TextEncoder().encode(s);

describe("createCron — manifest", () => {
  it("builds a normalized manifest with derived URLs", () => {
    const { cron } = makeInstance();
    const m = cron.manifest();
    expect(m.app).toBe("billing");
    expect(m.jobs.map((j) => j.name)).toEqual(["reconcile-payments", "settle-invoices"]);
    expect(m.jobs[0]?.request.url).toBe("https://billing.example.com/api/v1/scheduled/reconcile-payments");
    expect(m.jobs[1]?.request.url).toBe("https://billing.example.com/api/v1/scheduled/settle-invoices");
    expect(m.jobs[1]?.policy.timeout_seconds).toBe(120);
  });

  it("rejects duplicate registrations", () => {
    const { cron } = makeInstance();
    expect(() =>
      cron.register({
        name: "reconcile-payments",
        schedule: "@hourly",
        handler: () => ({ ok: true }),
      }),
    ).toThrow(/already registered/);
  });

  it("rejects invalid job definitions at register time", () => {
    const cron = createCron({ app: "x", baseUrl: "https://x.example.com", secret: SECRET });
    expect(() =>
      cron.register({
        name: "Invalid",
        schedule: "@hourly",
        handler: () => ({ ok: true }),
      }),
    ).toThrow(/job name must match/);
    expect(() =>
      cron.register({
        name: "noschedule",
        handler: () => ({ ok: true }),
      }),
    ).toThrow(/schedule.*schedules/);
  });
});

describe("createCron — verify(manifest)", () => {
  const path = "/.well-known/cron-manifest";

  it("accepts a signed GET", async () => {
    const { cron } = makeInstance();
    const { header } = await sign({
      secret: SECRET,
      method: "GET",
      path,
      body: enc(""),
      timestamp: NOW,
    });
    const result = await cron.verify({
      kind: "manifest",
      method: "GET",
      path,
      body: new Uint8Array(0),
      headers: { [HeaderSignature.toLowerCase()]: header },
      now: NOW,
    });
    expect(result.ok && result.kind === "manifest").toBe(true);
  });

  it("rejects when signature header is missing", async () => {
    const { cron } = makeInstance();
    const result = await cron.verify({
      kind: "manifest",
      method: "GET",
      path,
      body: new Uint8Array(0),
      headers: {},
      now: NOW,
    });
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.code).toBe("MissingSignature");
      expect(result.status).toBe(401);
    }
  });

  it("rejects POST on the manifest path", async () => {
    const { cron } = makeInstance();
    const { header } = await sign({
      secret: SECRET,
      method: "POST",
      path,
      body: enc(""),
      timestamp: NOW,
    });
    const result = await cron.verify({
      kind: "manifest",
      method: "POST",
      path,
      body: new Uint8Array(0),
      headers: { [HeaderSignature.toLowerCase()]: header },
      now: NOW,
    });
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.code).toBe("BadMethod");
  });
});

describe("createCron — verify(trigger) and run()", () => {
  it("dispatches the registered handler with a JobContext", async () => {
    const { cron, calls } = makeInstance();
    const path = "/api/v1/scheduled/reconcile-payments";
    const body = enc('{"runId":"abc","attempt":1}');
    const { header } = await sign({ secret: SECRET, method: "POST", path, body, timestamp: NOW });
    const result = await cron.verify({
      kind: "trigger",
      method: "POST",
      path,
      body,
      headers: {
        [HeaderSignature.toLowerCase()]: header,
        [HeaderRunId.toLowerCase()]: "run-123",
        "x-cron-attempt": "2",
      },
      now: NOW,
    });
    expect(result.ok && result.kind === "trigger").toBe(true);
    if (!result.ok || result.kind !== "trigger") return;
    expect(result.ctx.name).toBe("reconcile-payments");
    expect(result.ctx.runId).toBe("run-123");
    expect(result.ctx.attempt).toBe(2);

    const out = await result.run();
    expect(out.ok).toBe(true);
    expect(out.status).toBe(202);
    expect(out.body).toBe("accepted");
    expect(calls).toHaveLength(1);
    expect(calls[0]?.runId).toBe("run-123");
  });

  it("returns 404 for unknown job names", async () => {
    const { cron } = makeInstance();
    const path = "/api/v1/scheduled/no-such-job";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const result = await cron.verify({
      kind: "trigger",
      method: "POST",
      path,
      body: new Uint8Array(0),
      headers: { [HeaderSignature.toLowerCase()]: header },
      now: NOW,
    });
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.code).toBe("UnknownJob");
  });

  it("returns 401 on tampered body", async () => {
    const { cron } = makeInstance();
    const path = "/api/v1/scheduled/reconcile-payments";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc("original"), timestamp: NOW });
    const result = await cron.verify({
      kind: "trigger",
      method: "POST",
      path,
      body: enc("tampered"),
      headers: { [HeaderSignature.toLowerCase()]: header },
      now: NOW,
    });
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.code).toBe("SignatureMismatch");
      expect(result.status).toBe(401);
    }
  });

  it("supports rotation via secret arrays", async () => {
    const SECRET_OLD = SECRET;
    const SECRET_NEW = "whsec_test_rotated_bbbbbbbbbbbbbbbbbbbbbbbbbbb";
    const cron = createCron({
      app: "x",
      baseUrl: "https://x.example.com",
      secret: [SECRET_NEW, SECRET_OLD],
    });
    cron.register({ name: "j", schedule: "@hourly", handler: () => ({ ok: true }) });
    const path = "/api/v1/scheduled/j";
    const { header } = await sign({ secret: SECRET_OLD, method: "POST", path, body: enc(""), timestamp: NOW });
    const result = await cron.verify({
      kind: "trigger",
      method: "POST",
      path,
      body: new Uint8Array(0),
      headers: { [HeaderSignature.toLowerCase()]: header },
      now: NOW,
    });
    expect(result.ok && result.kind === "trigger").toBe(true);
    if (result.ok && result.kind === "trigger") {
      expect(result.secretIndex).toBe(1);
    }
  });
});

const triggerPath = "/api/v1/scheduled/reconcile-payments";

describe("createCron — verifyManifest / verifyTrigger (split methods)", () => {
  it("verifyManifest accepts a Web Request directly", async () => {
    const { cron } = makeInstance();
    const { header } = await sign({
      secret: SECRET,
      method: "GET",
      path: MANIFEST_PATH,
      body: enc(""),
      timestamp: NOW,
    });
    const req = new Request(`https://billing.example.com${MANIFEST_PATH}`, {
      method: "GET",
      headers: { [HeaderSignature]: header },
    });
    const r = await cron.verifyManifest(req, { now: NOW });
    expect(r.ok).toBe(true);
  });

  it("verifyTrigger accepts a Web Request directly", async () => {
    const { cron, calls } = makeInstance();
    const body = enc('{"hello":"world"}');
    const { header } = await sign({ secret: SECRET, method: "POST", path: triggerPath, body, timestamp: NOW });
    const req = new Request(`https://billing.example.com${triggerPath}`, {
      method: "POST",
      headers: {
        [HeaderSignature]: header,
        [HeaderRunId]: "run-web",
        "x-cron-attempt": "3",
      },
      body,
    });
    const r = await cron.verifyTrigger(req, { now: NOW });
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.ctx.runId).toBe("run-web");
    expect(r.ctx.attempt).toBe(3);
    const out = await r.run();
    expect(out.ok).toBe(true);
    expect(calls).toHaveLength(1);
  });

  it("verifyManifest also accepts a VerifyRequestObject (Node-friendly)", async () => {
    const { cron } = makeInstance();
    const { header } = await sign({
      secret: SECRET,
      method: "GET",
      path: MANIFEST_PATH,
      body: enc(""),
      timestamp: NOW,
    });
    const r = await cron.verifyManifest({
      method: "GET",
      path: MANIFEST_PATH,
      body: new Uint8Array(0),
      headers: { [HeaderSignature.toLowerCase()]: header },
      now: NOW,
    });
    expect(r.ok).toBe(true);
  });

  it("error result.toResponse() emits a JSON Response with the right status", async () => {
    const { cron } = makeInstance();
    const r = await cron.verifyManifest({
      method: "GET",
      path: MANIFEST_PATH,
      body: new Uint8Array(0),
      headers: {},
      now: NOW,
    });
    expect(r.ok).toBe(false);
    if (r.ok) return;
    const res = r.toResponse();
    expect(res.status).toBe(401);
    expect(res.headers.get("content-type")).toContain("application/json");
    const body = (await res.json()) as { code: string; message: string };
    expect(body.code).toBe("MissingSignature");
    expect(typeof body.message).toBe("string");
  });
});

describe("createCron — handle (zero-glue dispatcher)", () => {
  it("returns the manifest JSON for a signed manifest GET", async () => {
    const { cron } = makeInstance();
    const { header } = await sign({
      secret: SECRET,
      method: "GET",
      path: MANIFEST_PATH,
      body: enc(""),
      timestamp: NOW,
    });
    const req = new Request(`https://billing.example.com${MANIFEST_PATH}`, {
      method: "GET",
      headers: { [HeaderSignature]: header },
    });
    const res = await cron.handle(req, { now: NOW });
    expect(res.status).toBe(200);
    const body = (await res.json()) as { app: string; jobs: { name: string }[] };
    expect(body.app).toBe("billing");
    expect(body.jobs.map((j) => j.name)).toEqual(["reconcile-payments", "settle-invoices"]);
  });

  it("dispatches a trigger and returns the handler's response", async () => {
    const { cron, calls } = makeInstance();
    const body = enc('{"x":1}');
    const { header } = await sign({ secret: SECRET, method: "POST", path: triggerPath, body, timestamp: NOW });
    const req = new Request(`https://billing.example.com${triggerPath}`, {
      method: "POST",
      headers: { [HeaderSignature]: header, [HeaderRunId]: "run-handle" },
      body,
    });
    const res = await cron.handle(req, { now: NOW });
    expect(res.status).toBe(202);
    expect(await res.text()).toBe("accepted");
    expect(calls).toHaveLength(1);
    expect(calls[0]?.runId).toBe("run-handle");
  });

  it("returns 401 on tampered body", async () => {
    const { cron } = makeInstance();
    const { header } = await sign({
      secret: SECRET,
      method: "POST",
      path: triggerPath,
      body: enc("original"),
      timestamp: NOW,
    });
    const req = new Request(`https://billing.example.com${triggerPath}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
      body: enc("tampered"),
    });
    const res = await cron.handle(req, { now: NOW });
    expect(res.status).toBe(401);
  });

  it("returns 404 for paths that aren't manifest or trigger", async () => {
    const { cron } = makeInstance();
    const req = new Request("https://billing.example.com/nope", { method: "GET" });
    const res = await cron.handle(req, { now: NOW });
    expect(res.status).toBe(404);
    const body = (await res.json()) as { code: string };
    expect(body.code).toBe("UnknownPath");
  });
});

describe("createCron — late handler binding via cron.on()", () => {
  it("register without handler + on() binds and dispatches", async () => {
    const cron = createCron({ app: "billing", baseUrl: "https://billing.example.com", secret: SECRET });
    cron.register({ name: "ping", schedule: "@hourly", auth: { secret_refs: ["env:S"] } });

    const path = "/api/v1/scheduled/ping";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://billing.example.com${path}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
    });

    // Before binding — dispatch resolves to NoHandler.
    const before = await cron.verifyTrigger(req.clone(), { now: NOW });
    expect(before.ok).toBe(true);
    if (!before.ok) return;
    const out1 = await before.run();
    expect(out1.ok).toBe(false);
    expect(out1.status).toBe(503);
    expect(String(out1.body)).toContain("NoHandler");

    // Bind and try again.
    let called = false;
    cron.on("ping", () => {
      called = true;
      return { ok: true, status: 200 };
    });
    const after = await cron.verifyTrigger(req, { now: NOW });
    expect(after.ok).toBe(true);
    if (!after.ok) return;
    const out2 = await after.run();
    expect(out2.ok).toBe(true);
    expect(called).toBe(true);
  });

  it("on() throws when binding to an unknown job", () => {
    const cron = createCron({ app: "x", baseUrl: "https://x.example.com", secret: SECRET });
    expect(() => cron.on("nonexistent", () => ({ ok: true }))).toThrow(/no job registered/);
  });

  it("on() rebinds the handler for an already-bound job", async () => {
    const cron = createCron({ app: "billing", baseUrl: "https://billing.example.com", secret: SECRET });
    cron.register({ name: "ping", schedule: "@hourly", handler: () => ({ ok: true, body: "first" }) });
    cron.on("ping", () => ({ ok: true, body: "second" }));

    const path = "/api/v1/scheduled/ping";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://billing.example.com${path}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
    });
    const r = await cron.verifyTrigger(req, { now: NOW });
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    const out = await r.run();
    expect(out.body).toBe("second");
  });
});

describe("createCron — typed env (Bindings) and var (Variables)", () => {
  it("ctx.env exposes app-scoped bindings supplied at createCron", async () => {
    type Db = { query: (sql: string) => Promise<string> };
    type Env = { Bindings: { db: Db; logger: { info: (msg: string) => void } } };
    const db: Db = { query: async (sql) => `result:${sql}` };
    const logged: string[] = [];
    const cron = createCron<Env>({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: SECRET,
      env: { db, logger: { info: (m) => logged.push(m) } },
    });
    let observed: { dbResult: string } | null = null;
    cron.register({
      name: "ping",
      schedule: "@hourly",
      handler: async (ctx) => {
        ctx.env.logger.info("ran"); // typed call
        const r = await ctx.env.db.query("SELECT 1");
        observed = { dbResult: r };
        return { ok: true };
      },
    });
    const path = "/api/v1/scheduled/ping";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://billing.example.com${path}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
    });
    const res = await cron.handle(req, { now: NOW });
    expect(res.status).toBe(200);
    expect(observed).toEqual({ dbResult: "result:SELECT 1" });
    expect(logged).toEqual(["ran"]);
  });

  it("ctx.var exposes per-fire variables supplied at handle()", async () => {
    type Env = { Variables: { traceId: string; tenantId: number } };
    const cron = createCron<Env>({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: SECRET,
    });
    let observed: { traceId: string; tenantId: number } | null = null;
    cron.register({
      name: "ping",
      schedule: "@hourly",
      handler: async (ctx) => {
        observed = { traceId: ctx.var.traceId, tenantId: ctx.var.tenantId };
        return { ok: true };
      },
    });
    const path = "/api/v1/scheduled/ping";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://billing.example.com${path}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
    });
    const res = await cron.handle(req, { now: NOW, vars: { traceId: "t-abc", tenantId: 42 } });
    expect(res.status).toBe(200);
    expect(observed).toEqual({ traceId: "t-abc", tenantId: 42 });
  });

  it("ctx.set/get mutate per-fire variables at runtime", async () => {
    type Env = { Variables: { counter: number } };
    const cron = createCron<Env>({
      app: "x",
      baseUrl: "https://x.example.com",
      secret: SECRET,
    });
    let final = 0;
    cron.register({
      name: "j",
      schedule: "@hourly",
      handler: async (ctx) => {
        ctx.set("counter", ctx.get("counter") + 1);
        ctx.set("counter", ctx.get("counter") + 1);
        final = ctx.var.counter;
        return { ok: true };
      },
    });
    const path = "/api/v1/scheduled/j";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://x.example.com${path}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
    });
    const res = await cron.handle(req, { now: NOW, vars: { counter: 10 } });
    expect(res.status).toBe(200);
    expect(final).toBe(12);
  });

  it("env defaults to empty object when omitted; var defaults too", async () => {
    const cron = createCron({
      app: "x",
      baseUrl: "https://x.example.com",
      secret: SECRET,
    });
    let envKeys: string[] = [];
    let varKeys: string[] = [];
    cron.register({
      name: "j",
      schedule: "@hourly",
      handler: async (ctx) => {
        envKeys = Object.keys(ctx.env);
        varKeys = Object.keys(ctx.var);
        return { ok: true };
      },
    });
    const path = "/api/v1/scheduled/j";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://x.example.com${path}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
    });
    await cron.handle(req, { now: NOW });
    expect(envKeys).toEqual([]);
    expect(varKeys).toEqual([]);
  });

  it("default vars from createCron merge with per-fire vars; per-fire wins on collision", async () => {
    type Env = { Variables: { region: string; traceId: string; tenantId: number } };
    const cron = createCron<Env>({
      app: "x",
      baseUrl: "https://x.example.com",
      secret: SECRET,
      vars: { region: "eu-west-1", traceId: "trace-default", tenantId: 0 },
    });
    let observed: { region: string; traceId: string; tenantId: number } | null = null;
    cron.register({
      name: "j",
      schedule: "@hourly",
      handler: async (ctx) => {
        observed = { region: ctx.var.region, traceId: ctx.var.traceId, tenantId: ctx.var.tenantId };
        return { ok: true };
      },
    });
    const path = "/api/v1/scheduled/j";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://x.example.com${path}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
    });
    // Per-fire passes traceId + tenantId; region falls through from createCron defaults.
    const res = await cron.handle(req, {
      now: NOW,
      vars: { region: "eu-west-1", traceId: "trace-fire", tenantId: 99 },
    });
    expect(res.status).toBe(200);
    expect(observed).toEqual({ region: "eu-west-1", traceId: "trace-fire", tenantId: 99 });
  });

  it("default vars survive when handle passes no vars at all", async () => {
    type Env = { Variables: { region: string } };
    const cron = createCron<Env>({
      app: "x",
      baseUrl: "https://x.example.com",
      secret: SECRET,
      vars: { region: "us-east-1" },
    });
    let region = "";
    cron.register({
      name: "j",
      schedule: "@hourly",
      handler: async (ctx) => {
        region = ctx.var.region;
        return { ok: true };
      },
    });
    const path = "/api/v1/scheduled/j";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://x.example.com${path}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
    });
    await cron.handle(req, { now: NOW }); // no vars supplied per-fire
    expect(region).toBe("us-east-1");
  });

  it("vars passed to verifyTrigger flow into ctx.var", async () => {
    type Env = { Variables: { who: string } };
    const cron = createCron<Env>({
      app: "x",
      baseUrl: "https://x.example.com",
      secret: SECRET,
    });
    cron.register({
      name: "j",
      schedule: "@hourly",
      handler: async (ctx) => ({ ok: true, body: `hi ${ctx.var.who}` }),
    });
    const path = "/api/v1/scheduled/j";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://x.example.com${path}`, {
      method: "POST",
      headers: { [HeaderSignature]: header },
    });
    const r = await cron.verifyTrigger(req, { now: NOW, vars: { who: "world" } });
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.ctx.var.who).toBe("world");
    const out = await r.run();
    expect(out.body).toBe("hi world");
  });
});

describe("JobContext shape (simplified)", () => {
  it("exposes ctx.headers and ctx.meta nesting", async () => {
    const cron = createCron({ app: "x", baseUrl: "https://x.example.com", secret: SECRET });
    let observedHeaders: Record<string, string> | null = null;
    let observedMetaKeys: string[] = [];
    let observedLegacy: Date | null = null;
    cron.register({
      name: "j",
      schedule: "@hourly",
      handler: (ctx) => {
        observedHeaders = ctx.headers;
        observedMetaKeys = Object.keys(ctx.meta);
        observedLegacy = ctx.fireTime;
        return { ok: true };
      },
    });
    const path = "/api/v1/scheduled/j";
    const { header } = await sign({ secret: SECRET, method: "POST", path, body: enc(""), timestamp: NOW });
    const req = new Request(`https://x.example.com${path}`, {
      method: "POST",
      headers: {
        [HeaderSignature]: header,
        "x-cron-fire-time": String(NOW),
        "x-custom-app-header": "yes",
      },
    });
    const r = await cron.verifyTrigger(req, { now: NOW });
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    await r.run();
    expect(observedHeaders).not.toBeNull();
    expect(observedHeaders?.["x-custom-app-header"]).toBe("yes");
    expect(observedMetaKeys.sort()).toEqual(["fireTime", "fireTimeActual", "previousSuccessTime"]);
    expect(observedLegacy).toEqual(new Date(NOW * 1000));
    expect(r.ctx.fireTime).toEqual(r.ctx.meta.fireTime);
  });
});
