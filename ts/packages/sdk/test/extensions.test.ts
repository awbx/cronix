/**
 * Coverage for the SDK extension points (RFC §SDK Contract / Extension
 * points; D-030..D-035): skipVerify, hooks, errorResponse, replay-window
 * validation, and the standalone verify utilities.
 */
import { describe, expect, it, vi } from "vitest";
import {
  createCron,
  type Logger,
  MANIFEST_PATH,
  parseSignatureHeader,
  REPLAY_WINDOW_MIN_SECONDS,
  sign,
  signRequest,
  TRIGGER_PATH_PREFIX,
  verifyManifestRequest,
  verifyTriggerRequest,
} from "../src/core/index.js";

const SECRET = "whsec_test_primary_aaaaaaaaaaaaaaaaaaaaaaaaaaa";
const NOW = 1_730_000_000;

const triggerUrl = (job: string) => `https://billing.example.com${TRIGGER_PATH_PREFIX}${job}`;
const manifestUrl = () => `https://billing.example.com${MANIFEST_PATH}`;

const silentLogger: Logger = { debug: () => {}, info: () => {}, warn: () => {}, error: () => {} };

describe("skipVerify (D-031)", () => {
  it("instance-level skipVerify accepts unsigned triggers and stamps ctx.unverified", async () => {
    const cron = createCron({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: "ignored",
      skipVerify: true,
      logger: silentLogger,
    });
    const seen: { unverified: boolean }[] = [];
    cron.register({
      name: "noop",
      schedule: "@hourly",
      handler: (ctx) => {
        seen.push({ unverified: ctx.unverified });
        return { ok: true };
      },
    });

    const res = await cron.handle(new Request(triggerUrl("noop"), { method: "POST", body: "{}" }));
    expect(res.status).toBe(200);
    expect(seen).toEqual([{ unverified: true }]);
  });

  it("instance-level skipVerify accepts unsigned manifest fetches", async () => {
    const cron = createCron({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: "ignored",
      skipVerify: true,
      logger: silentLogger,
    });
    cron.register({ name: "noop", schedule: "@hourly", handler: () => ({ ok: true }) });
    const res = await cron.handle(new Request(manifestUrl()));
    expect(res.status).toBe(200);
  });

  it("per-job skipVerify allows that job only — others still require signatures", async () => {
    const cron = createCron({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: SECRET,
    });
    cron.register({
      name: "open-job",
      schedule: "@hourly",
      skipVerify: true,
      handler: (ctx) => {
        expect(ctx.unverified).toBe(true);
        return { ok: true };
      },
    });
    cron.register({
      name: "closed-job",
      schedule: "@hourly",
      handler: (ctx) => {
        expect(ctx.unverified).toBe(false);
        return { ok: true };
      },
    });

    const openRes = await cron.handle(new Request(triggerUrl("open-job"), { method: "POST", body: "" }));
    expect(openRes.status).toBe(200);

    const closedRes = await cron.handle(new Request(triggerUrl("closed-job"), { method: "POST", body: "" }));
    expect(closedRes.status).toBe(401);
  });

  it("emits a one-line warn when instance skipVerify is true (D-031)", () => {
    const warn = vi.fn();
    createCron({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: "ignored",
      skipVerify: true,
      logger: { ...silentLogger, warn },
    });
    expect(warn).toHaveBeenCalledTimes(1);
    expect(warn.mock.calls[0]?.[0]).toMatch(/skipVerify=true/);
  });
});

describe("hooks (D-032)", () => {
  it("calls onTriggerStart / onTriggerSuccess / onManifestRequest in order", async () => {
    const events: string[] = [];
    const cron = createCron({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: SECRET,
      logger: silentLogger,
      hooks: {
        onManifestRequest: () => {
          events.push("manifest");
        },
        onTriggerStart: (ctx) => {
          events.push(`start:${ctx.name}`);
        },
        onTriggerSuccess: (ctx, _r, ms) => {
          events.push(`success:${ctx.name}:ms>=0=${ms >= 0}`);
        },
      },
    });
    cron.register({ name: "noop", schedule: "@hourly", handler: () => ({ ok: true }) });

    const ts = NOW;
    const body = new TextEncoder().encode("");
    const sigT = await sign({
      secret: SECRET,
      method: "POST",
      path: `${TRIGGER_PATH_PREFIX}noop`,
      body,
      timestamp: ts,
    });
    await cron.handle(
      new Request(triggerUrl("noop"), {
        method: "POST",
        body: "",
        headers: { "X-Cron-Signature": sigT.header },
      }),
      { now: ts },
    );

    const sigM = await sign({
      secret: SECRET,
      method: "GET",
      path: MANIFEST_PATH,
      body: new Uint8Array(0),
      timestamp: ts,
    });
    await cron.handle(new Request(manifestUrl(), { headers: { "X-Cron-Signature": sigM.header } }), { now: ts });

    expect(events).toEqual(["start:noop", "success:noop:ms>=0=true", "manifest"]);
  });

  it("calls onVerifyFailure on missing signature, NOT onTriggerStart", async () => {
    const events: string[] = [];
    const cron = createCron({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: SECRET,
      logger: silentLogger,
      hooks: {
        onVerifyFailure: (failure) => {
          events.push(`fail:${failure.code}`);
        },
        onTriggerStart: () => {
          events.push("start");
        },
      },
    });
    cron.register({ name: "noop", schedule: "@hourly", handler: () => ({ ok: true }) });

    const res = await cron.handle(new Request(triggerUrl("noop"), { method: "POST", body: "" }));
    expect(res.status).toBe(401);
    expect(events).toEqual(["fail:MissingSignature"]);
  });

  it("hook errors are caught and routed to logger.error, never break the response", async () => {
    const errLog = vi.fn();
    const cron = createCron({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: SECRET,
      logger: { ...silentLogger, error: errLog },
      hooks: {
        onTriggerStart: () => {
          throw new Error("hook boom");
        },
      },
    });
    cron.register({ name: "noop", schedule: "@hourly", handler: () => ({ ok: true, status: 200, body: "ok" }) });

    const ts = NOW;
    const body = new TextEncoder().encode("");
    const sig = await sign({ secret: SECRET, method: "POST", path: `${TRIGGER_PATH_PREFIX}noop`, body, timestamp: ts });
    const res = await cron.handle(
      new Request(triggerUrl("noop"), {
        method: "POST",
        body: "",
        headers: { "X-Cron-Signature": sig.header },
      }),
      { now: ts },
    );

    expect(res.status).toBe(200);
    expect(errLog).toHaveBeenCalled();
    expect(String(errLog.mock.calls[0]?.[0])).toMatch(/onTriggerStart threw/);
  });
});

describe("errorResponse override", () => {
  it("custom errorResponse builds the failure body", async () => {
    const cron = createCron({
      app: "billing",
      baseUrl: "https://billing.example.com",
      secret: SECRET,
      errorResponse: (f) =>
        new Response(JSON.stringify({ ok: false, err: { code: f.code, msg: f.message } }), {
          status: f.status,
          headers: { "content-type": "application/json", "x-error": f.code },
        }),
    });
    cron.register({ name: "noop", schedule: "@hourly", handler: () => ({ ok: true }) });

    const res = await cron.handle(new Request(triggerUrl("noop"), { method: "POST", body: "" }));
    expect(res.status).toBe(401);
    expect(res.headers.get("x-error")).toBe("MissingSignature");
    const body = (await res.json()) as { ok: boolean; err: { code: string; msg: string } };
    expect(body.ok).toBe(false);
    expect(body.err.code).toBe("MissingSignature");
  });
});

describe("replayWindowSeconds (D-034)", () => {
  it("rejects values below 30 at construction", () => {
    expect(() =>
      createCron({
        app: "billing",
        baseUrl: "https://billing.example.com",
        secret: SECRET,
        replayWindowSeconds: 5,
        logger: silentLogger,
      }),
    ).toThrowError(/must be >= 30/);
  });

  it(`accepts ${REPLAY_WINDOW_MIN_SECONDS}s exactly`, () => {
    expect(() =>
      createCron({
        app: "billing",
        baseUrl: "https://billing.example.com",
        secret: SECRET,
        replayWindowSeconds: REPLAY_WINDOW_MIN_SECONDS,
        logger: silentLogger,
      }),
    ).not.toThrow();
  });
});

describe("standalone verify utilities (D-035)", () => {
  it("verifyTriggerRequest accepts a Web Request and returns the parsed name + body", async () => {
    const ts = NOW;
    const path = `${TRIGGER_PATH_PREFIX}reconcile-payments`;
    const body = new TextEncoder().encode("hello");
    const sig = await sign({ secret: SECRET, method: "POST", path, body, timestamp: ts });

    const req = new Request(triggerUrl("reconcile-payments"), {
      method: "POST",
      body: "hello",
      headers: { "X-Cron-Signature": sig.header },
    });
    const r = await verifyTriggerRequest(req, { secret: SECRET, now: ts });
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.name).toBe("reconcile-payments");
      expect(new TextDecoder().decode(r.body)).toBe("hello");
    }
  });

  it("verifyTriggerRequest rejects an unsigned request with MissingSignature 401", async () => {
    const r = await verifyTriggerRequest(new Request(triggerUrl("noop"), { method: "POST", body: "" }), {
      secret: SECRET,
    });
    expect(r.ok).toBe(false);
    if (!r.ok) {
      expect(r.status).toBe(401);
      expect(r.code).toBe("MissingSignature");
    }
  });

  it("verifyManifestRequest rejects POSTs with BadMethod 400", async () => {
    const r = await verifyManifestRequest(new Request(manifestUrl(), { method: "POST" }), { secret: SECRET });
    expect(r.ok).toBe(false);
    if (!r.ok) {
      expect(r.status).toBe(400);
      expect(r.code).toBe("BadMethod");
    }
  });

  it("signRequest produces a header that round-trips through parseSignatureHeader", async () => {
    const r = await signRequest({
      secret: SECRET,
      method: "POST",
      path: `${TRIGGER_PATH_PREFIX}noop`,
      body: new Uint8Array(0),
      timestamp: NOW,
    });
    const parsed = parseSignatureHeader(r.header);
    expect(parsed.ok).toBe(true);
    if (parsed.ok) {
      expect(parsed.value.ts).toBe(NOW);
      expect(parsed.value.sigHex).toMatch(/^[0-9a-f]{64}$/);
    }
  });
});
