import type { AddressInfo } from "node:net";
import express from "express";
import Fastify from "fastify";
import { Hono } from "hono";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { type CronInstance, createCron, HeaderRunId, HeaderSignature, MANIFEST_PATH, sign } from "../src/core/index.js";

const SECRET = "whsec_test_primary_aaaaaaaaaaaaaaaaaaaaaaaaaaa";

function makeCron(): CronInstance {
  const cron = createCron({
    app: "billing",
    baseUrl: "https://billing.example.com",
    secret: SECRET,
  });
  cron.register({
    name: "ping",
    schedule: "@hourly",
    handler: async (ctx) => ({
      ok: true,
      status: 202,
      body: JSON.stringify({ name: ctx.name, runId: ctx.runId, attempt: ctx.attempt }),
    }),
  });
  return cron;
}

type Hit = { status: number; bodyText: string };

type Driver = {
  name: string;
  setup: () => Promise<{ cron: CronInstance; hit: (req: HitRequest) => Promise<Hit>; teardown: () => Promise<void> }>;
};

type HitRequest = {
  method: "GET" | "POST";
  path: string;
  body: Uint8Array;
  headers: Record<string, string>;
};

const expressDriver: Driver = {
  name: "express",
  setup: async () => {
    const cron = makeCron();
    const app = express();
    app.get(MANIFEST_PATH, async (req, res) => {
      const r = await cron.verify({
        kind: "manifest",
        method: req.method,
        path: req.originalUrl.split("?")[0] ?? req.path,
        body: new Uint8Array(0),
        headers: req.headers as Record<string, string | string[] | undefined>,
      });
      if (!r.ok) {
        res.status(r.status).json({ code: r.code });
        return;
      }
      res.json(cron.manifest());
    });
    app.post("/api/v1/scheduled/:name", express.raw({ type: "*/*" }), async (req, res) => {
      const buf = req.body instanceof Buffer ? req.body : Buffer.alloc(0);
      const body = new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
      const r = await cron.verify({
        kind: "trigger",
        method: req.method,
        path: req.originalUrl.split("?")[0] ?? req.path,
        body,
        headers: req.headers as Record<string, string | string[] | undefined>,
      });
      if (!r.ok) {
        res.status(r.status).json({ code: r.code });
        return;
      }
      if (r.kind !== "trigger") {
        res.status(500).end();
        return;
      }
      const out = await r.run();
      res.status(out.status ?? (out.ok ? 200 : 500)).end(out.body);
    });
    const server = app.listen(0);
    await new Promise<void>((resolve) => server.once("listening", () => resolve()));
    const addr = server.address() as AddressInfo;
    const baseUrl = `http://127.0.0.1:${addr.port}`;
    return {
      cron,
      hit: async (req) => {
        const res = await fetch(`${baseUrl}${req.path}`, {
          method: req.method,
          headers: req.headers,
          body: req.method === "GET" ? undefined : Buffer.from(req.body),
        });
        return { status: res.status, bodyText: await res.text() };
      },
      teardown: async () => {
        if (typeof (server as { closeAllConnections?: () => void }).closeAllConnections === "function") {
          (server as { closeAllConnections: () => void }).closeAllConnections();
        }
        await new Promise<void>((resolve) => server.close(() => resolve()));
      },
    };
  },
};

const fastifyDriver: Driver = {
  name: "fastify",
  setup: async () => {
    const cron = makeCron();
    const app = Fastify();
    app.removeAllContentTypeParsers();
    app.addContentTypeParser("*", { parseAs: "buffer" }, (_req, body, done) => {
      done(null, body);
    });
    app.get(MANIFEST_PATH, async (req, reply) => {
      const r = await cron.verify({
        kind: "manifest",
        method: req.method,
        path: req.url.split("?")[0] ?? req.url,
        body: new Uint8Array(0),
        headers: req.headers as Record<string, string | string[] | undefined>,
      });
      if (!r.ok) return reply.code(r.status).send({ code: r.code });
      return reply.code(200).send(cron.manifest());
    });
    app.post("/api/v1/scheduled/:name", async (req, reply) => {
      const buf = req.body instanceof Buffer ? req.body : Buffer.alloc(0);
      const body = new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
      const r = await cron.verify({
        kind: "trigger",
        method: req.method,
        path: req.url.split("?")[0] ?? req.url,
        body,
        headers: req.headers as Record<string, string | string[] | undefined>,
      });
      if (!r.ok) return reply.code(r.status).send({ code: r.code });
      if (r.kind !== "trigger") return reply.code(500).send();
      const out = await r.run();
      return reply.code(out.status ?? (out.ok ? 200 : 500)).send(out.body);
    });
    return {
      cron,
      hit: async (req) => {
        const res = await app.inject({
          method: req.method,
          url: req.path,
          headers: req.headers,
          payload: Buffer.from(req.body),
        });
        return { status: res.statusCode, bodyText: res.body };
      },
      teardown: async () => {
        await app.close();
      },
    };
  },
};

const honoDriver: Driver = {
  name: "hono",
  setup: async () => {
    const cron = makeCron();
    const app = new Hono();
    app.get(MANIFEST_PATH, async (c) => {
      const url = new URL(c.req.url);
      const r = await cron.verify({
        kind: "manifest",
        method: c.req.method,
        path: url.pathname,
        body: new Uint8Array(0),
        headers: rawHeaders(c.req.raw.headers),
      });
      if (!r.ok) return c.json({ code: r.code }, r.status as 401 | 400 | 404);
      return c.json(cron.manifest());
    });
    app.post("/api/v1/scheduled/:name", async (c) => {
      const url = new URL(c.req.url);
      const ab = await c.req.arrayBuffer();
      const body = new Uint8Array(ab);
      const r = await cron.verify({
        kind: "trigger",
        method: c.req.method,
        path: url.pathname,
        body,
        headers: rawHeaders(c.req.raw.headers),
      });
      if (!r.ok) return c.json({ code: r.code }, r.status as 401 | 400 | 404);
      if (r.kind !== "trigger") return c.json({}, 500);
      const out = await r.run();
      const status = out.status ?? (out.ok ? 200 : 500);
      return new Response(out.body ?? null, { status });
    });
    return {
      cron,
      hit: async (req) => {
        const res = await app.fetch(
          new Request(`http://example.test${req.path}`, {
            method: req.method,
            headers: req.headers,
            body: req.method === "GET" ? undefined : req.body,
          }),
        );
        return { status: res.status, bodyText: await res.text() };
      },
      teardown: async () => undefined,
    };
  },
};

function rawHeaders(headers: Headers): Record<string, string | string[] | undefined> {
  const out: Record<string, string | string[] | undefined> = {};
  headers.forEach((v, k) => {
    out[k.toLowerCase()] = v;
  });
  return out;
}

const drivers: Driver[] = [expressDriver, fastifyDriver, honoDriver];

for (const driver of drivers) {
  describe(`integration: ${driver.name}`, () => {
    let env: Awaited<ReturnType<Driver["setup"]>>;

    beforeAll(async () => {
      env = await driver.setup();
    });

    afterAll(async () => {
      await env.teardown();
    });

    it("serves a signed manifest fetch", async () => {
      const path = MANIFEST_PATH;
      const { header } = await sign({
        secret: SECRET,
        method: "GET",
        path,
        body: new Uint8Array(0),
        timestamp: Math.floor(Date.now() / 1000),
      });
      const res = await env.hit({
        method: "GET",
        path,
        body: new Uint8Array(0),
        headers: { [HeaderSignature.toLowerCase()]: header },
      });
      expect(res.status).toBe(200);
      const body = JSON.parse(res.bodyText);
      expect(body.app).toBe("billing");
      expect(body.jobs[0]?.name).toBe("ping");
    });

    it("rejects manifest fetch without signature", async () => {
      const res = await env.hit({
        method: "GET",
        path: MANIFEST_PATH,
        body: new Uint8Array(0),
        headers: {},
      });
      expect(res.status).toBe(401);
    });

    it("dispatches a signed trigger", async () => {
      const path = "/api/v1/scheduled/ping";
      const body = new TextEncoder().encode('{"hello":"world"}');
      const { header } = await sign({
        secret: SECRET,
        method: "POST",
        path,
        body,
        timestamp: Math.floor(Date.now() / 1000),
      });
      const res = await env.hit({
        method: "POST",
        path,
        body,
        headers: {
          [HeaderSignature.toLowerCase()]: header,
          [HeaderRunId.toLowerCase()]: "run-abc",
          "content-type": "application/json",
        },
      });
      expect(res.status).toBe(202);
      const body2 = JSON.parse(res.bodyText);
      expect(body2.runId).toBe("run-abc");
      expect(body2.name).toBe("ping");
    });

    it("rejects trigger with tampered body", async () => {
      const path = "/api/v1/scheduled/ping";
      const original = new TextEncoder().encode("original");
      const tampered = new TextEncoder().encode("tampered");
      const { header } = await sign({
        secret: SECRET,
        method: "POST",
        path,
        body: original,
        timestamp: Math.floor(Date.now() / 1000),
      });
      const res = await env.hit({
        method: "POST",
        path,
        body: tampered,
        headers: { [HeaderSignature.toLowerCase()]: header },
      });
      expect(res.status).toBe(401);
    });

    it("returns 404 for unknown trigger names", async () => {
      const path = "/api/v1/scheduled/nope";
      const { header } = await sign({
        secret: SECRET,
        method: "POST",
        path,
        body: new Uint8Array(0),
        timestamp: Math.floor(Date.now() / 1000),
      });
      const res = await env.hit({
        method: "POST",
        path,
        body: new Uint8Array(0),
        headers: { [HeaderSignature.toLowerCase()]: header },
      });
      expect(res.status).toBe(404);
    });
  });
}
