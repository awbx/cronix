import { describe, expect, it } from "vitest";
import { createCron, HeaderRunId, HeaderSignature, sign } from "../src/core/index.js";

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
