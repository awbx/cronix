import { verify as verifySignature } from "./auth.js";
import {
  HeaderAttempt,
  HeaderFireTime,
  HeaderFireTimeActual,
  HeaderPreviousSuccessTime,
  HeaderRunId,
  HeaderSignature,
} from "./headers.js";
import { applyDefaults, type Job, type Manifest, parseManifest } from "./manifest.js";
import type {
  CronInstance,
  HandlerResult,
  HeadersInput,
  JobContext,
  JobDefinition,
  JobHandler,
  VerifyFailure,
  VerifyInput,
  VerifyManifestResult,
  VerifyRequest,
  VerifyRequestObject,
  VerifyResult,
  VerifyTimeOptions,
  VerifyTriggerResult,
} from "./types.js";

export const MANIFEST_PATH = "/.well-known/cron-manifest";
export const TRIGGER_PATH_PREFIX = "/api/v1/scheduled/";

const HTTP_OK = 200;
const HTTP_BAD_REQUEST = 400;
const HTTP_UNAUTHORIZED = 401;
const HTTP_NOT_FOUND = 404;
const HTTP_INTERNAL = 500;
const HTTP_UNAVAILABLE = 503;

export type CreateCronOptions = {
  app: string;
  /** Base URL the manifest publishes for trigger endpoints. No trailing slash required. */
  baseUrl: string;
  /** One or more secrets. Function form is re-evaluated on every verify call. */
  secret: string | string[] | (() => string | string[]);
};

/**
 * Build a cronix instance.
 *
 * Three tiers of integration; pick the one that matches your style:
 *
 * **Tier 1 — zero glue** (`cron.handle`):
 * ```ts
 * const cron = createCron({ app, baseUrl, secret });
 * cron.register({ name, schedule, handler });
 *
 * app.all('/.well-known/cron-manifest', c => cron.handle(c.req.raw));
 * app.all('/api/v1/scheduled/:name', c => cron.handle(c.req.raw));
 * ```
 *
 * **Tier 2 — explicit verify**:
 * ```ts
 * app.get(MANIFEST_PATH, async c => {
 *   const r = await cron.verifyManifest(c.req.raw);
 *   if (!r.ok) return r.toResponse();
 *   return Response.json(cron.manifest());
 * });
 *
 * app.post('/api/v1/scheduled/:name', async c => {
 *   const r = await cron.verifyTrigger(c.req.raw);
 *   if (!r.ok) return r.toResponse();
 *   const out = await r.run();
 *   return new Response(out.body ?? null, { status: out.status ?? (out.ok ? 200 : 500) });
 * });
 * ```
 *
 * **Tier 3 — late handler binding**: omit `handler` from `register` and bind
 * it later from another file with `cron.on(name, handler)`.
 */
export function createCron(options: CreateCronOptions): CronInstance {
  const jobs = new Map<string, { def: JobDefinition }>();
  const handlers = new Map<string, JobHandler>();

  const resolveSecrets = (): string[] => {
    const raw = typeof options.secret === "function" ? options.secret() : options.secret;
    return Array.isArray(raw) ? raw : [raw];
  };

  const buildJob = (def: JobDefinition): Job => ({
    name: def.name,
    schedule: def.schedule,
    schedules: def.schedules,
    timezone: def.timezone,
    request: {
      method: def.method,
      url: def.urlOverride ?? `${options.baseUrl.replace(/\/+$/, "")}${TRIGGER_PATH_PREFIX}${def.name}`,
      headers: def.headers,
      body: def.body,
    },
    policy: def.policy,
    auth: def.auth,
  });

  const dispatch = async (ctx: JobContext): Promise<HandlerResult> => {
    if (!jobs.has(ctx.name)) {
      return {
        ok: false,
        status: HTTP_NOT_FOUND,
        body: JSON.stringify({ code: "UnknownJob", message: `no registered job named ${ctx.name}` }),
      };
    }
    const handler = handlers.get(ctx.name);
    if (!handler) {
      return {
        ok: false,
        status: HTTP_UNAVAILABLE,
        body: JSON.stringify({ code: "NoHandler", message: `no handler bound for job ${ctx.name}` }),
      };
    }
    return handler(ctx);
  };

  const register = (def: JobDefinition): void => {
    if (jobs.has(def.name)) {
      throw new Error(`cronix: job already registered: ${def.name}`);
    }
    const probe: Manifest = { version: 1, app: options.app, jobs: [buildJob(def)] };
    const parsed = parseManifest(probe);
    if (!parsed.ok) {
      throw new Error(
        `cronix: invalid job definition for ${def.name}: ${parsed.error.issues
          .map((i) => `${i.path.join("/")}: ${i.message}`)
          .join("; ")}`,
      );
    }
    jobs.set(def.name, { def });
    if (def.handler) handlers.set(def.name, def.handler);
  };

  const on = (name: string, handler: JobHandler): void => {
    if (!jobs.has(name)) {
      throw new Error(`cronix: cannot bind handler — no job registered named ${name}`);
    }
    handlers.set(name, handler);
  };

  const manifestFn = () => {
    const built: Manifest = {
      version: 1,
      app: options.app,
      jobs: [...jobs.values()].map((e) => buildJob(e.def)),
    };
    const parsed = parseManifest(built);
    if (!parsed.ok) {
      throw new Error(
        `cronix: built manifest fails validation — likely a register-time bug. issues: ${JSON.stringify(parsed.error.issues)}`,
      );
    }
    return applyDefaults(parsed.value);
  };

  const verifySignatureOnly = async (
    n: NormalizedRequest,
  ): Promise<{ ok: true; secretIndex: number } | VerifyFailure> => {
    const sig = pickHeader(n.headers, HeaderSignature);
    if (sig === undefined) {
      return errorResult(HTTP_UNAUTHORIZED, "MissingSignature", `missing ${HeaderSignature} header`);
    }
    const secrets = resolveSecrets();
    const sigResult = await verifySignature({
      secrets,
      method: n.method,
      path: n.path,
      body: n.body,
      header: sig,
      ...(n.now !== undefined ? { now: n.now } : {}),
      ...(n.maxSkewSeconds !== undefined ? { maxSkewSeconds: n.maxSkewSeconds } : {}),
    });
    if (!sigResult.ok) {
      return errorResult(HTTP_UNAUTHORIZED, sigResult.error.code, sigResult.error.message);
    }
    return { ok: true, secretIndex: sigResult.value.secretIndex };
  };

  const verifyManifest = async (req: VerifyInput, opts?: VerifyTimeOptions): Promise<VerifyManifestResult> => {
    const n = await normalizeVerifyInput(req, opts);
    if (n.method.toUpperCase() !== "GET") {
      return errorResult(HTTP_BAD_REQUEST, "BadMethod", `manifest fetches must be GET, got ${n.method}`);
    }
    if (n.path !== MANIFEST_PATH) {
      return errorResult(HTTP_NOT_FOUND, "BadPath", `manifest path must be ${MANIFEST_PATH}, got ${n.path}`);
    }
    const r = await verifySignatureOnly(n);
    if (!r.ok) return r;
    return { ok: true, secretIndex: r.secretIndex };
  };

  const verifyTrigger = async (req: VerifyInput, opts?: VerifyTimeOptions): Promise<VerifyTriggerResult> => {
    const n = await normalizeVerifyInput(req, opts);
    const r = await verifySignatureOnly(n);
    if (!r.ok) return r;

    const name = triggerNameFromPath(n.path);
    if (!name) {
      return errorResult(
        HTTP_NOT_FOUND,
        "BadPath",
        `trigger path must start with ${TRIGGER_PATH_PREFIX}, got ${n.path}`,
      );
    }
    if (!jobs.has(name)) {
      return errorResult(HTTP_NOT_FOUND, "UnknownJob", `no registered job named ${name}`);
    }
    const ctx = buildContext(options.app, name, n);
    return {
      ok: true,
      secretIndex: r.secretIndex,
      ctx,
      run: () => dispatch(ctx),
    };
  };

  const handle = async (req: Request, opts?: VerifyTimeOptions): Promise<Response> => {
    const url = new URL(req.url);
    const path = url.pathname;

    if (path === MANIFEST_PATH) {
      const r = await verifyManifest(req, opts);
      if (!r.ok) return r.toResponse();
      return jsonResponse(HTTP_OK, manifestFn());
    }
    if (triggerNameFromPath(path) !== null) {
      const r = await verifyTrigger(req, opts);
      if (!r.ok) return r.toResponse();
      const out = await r.run();
      return responseFromHandlerResult(out);
    }
    return jsonResponse(HTTP_NOT_FOUND, { code: "UnknownPath", message: `path ${path} is not served by cronix` });
  };

  // Legacy verify({kind, …}) — implemented in terms of the new methods.
  const verify = async (req: VerifyRequest): Promise<VerifyResult> => {
    const adapted: VerifyRequestObject = {
      method: req.method,
      path: req.path,
      body: req.body,
      headers: req.headers,
      ...(req.now !== undefined ? { now: req.now } : {}),
      ...(req.maxSkewSeconds !== undefined ? { maxSkewSeconds: req.maxSkewSeconds } : {}),
    };
    if (req.kind === "manifest") {
      const r = await verifyManifest(adapted);
      if (!r.ok) return { ok: false, status: r.status, code: r.code, message: r.message };
      return { ok: true, kind: "manifest", secretIndex: r.secretIndex };
    }
    const r = await verifyTrigger(adapted);
    if (!r.ok) return { ok: false, status: r.status, code: r.code, message: r.message };
    return { ok: true, kind: "trigger", secretIndex: r.secretIndex, ctx: r.ctx, run: r.run };
  };

  return {
    app: options.app,
    register,
    on,
    manifest: manifestFn,
    verifyManifest,
    verifyTrigger,
    handle,
    verify,
  };
}

type NormalizedRequest = {
  method: string;
  path: string;
  body: Uint8Array;
  headers: Record<string, string | string[] | undefined>;
  now?: number;
  maxSkewSeconds?: number;
};

async function normalizeVerifyInput(input: VerifyInput, opts?: VerifyTimeOptions): Promise<NormalizedRequest> {
  if (isWebRequest(input)) {
    const url = new URL(input.url);
    const ab = await input.arrayBuffer();
    return {
      method: input.method,
      path: url.pathname,
      body: new Uint8Array(ab),
      headers: headersToRecord(input.headers),
      ...(opts?.now !== undefined ? { now: opts.now } : {}),
      ...(opts?.maxSkewSeconds !== undefined ? { maxSkewSeconds: opts.maxSkewSeconds } : {}),
    };
  }
  // opts overrides values on the object form, if both present.
  const now = opts?.now ?? input.now;
  const maxSkewSeconds = opts?.maxSkewSeconds ?? input.maxSkewSeconds;
  return {
    method: input.method,
    path: input.path,
    body: input.body,
    headers: input.headers instanceof Headers ? headersToRecord(input.headers) : input.headers,
    ...(now !== undefined ? { now } : {}),
    ...(maxSkewSeconds !== undefined ? { maxSkewSeconds } : {}),
  };
}

function isWebRequest(input: VerifyInput): input is Request {
  if (typeof Request !== "undefined" && input instanceof Request) return true;
  // Duck-typing fallback: Workers / older runtimes may have a non-instanceof Request shape.
  const r = input as Partial<Request>;
  return typeof r.url === "string" && typeof r.method === "string" && typeof r.arrayBuffer === "function";
}

function headersToRecord(h: HeadersInput): Record<string, string | string[] | undefined> {
  if (h instanceof Headers) {
    const out: Record<string, string> = {};
    h.forEach((v, k) => {
      out[k.toLowerCase()] = v;
    });
    return out;
  }
  return h;
}

function triggerNameFromPath(path: string): string | null {
  if (!path.startsWith(TRIGGER_PATH_PREFIX)) return null;
  const rest = path.slice(TRIGGER_PATH_PREFIX.length);
  if (rest.length === 0 || rest.includes("/")) return null;
  return rest;
}

function pickHeader(headers: Record<string, string | string[] | undefined>, name: string): string | undefined {
  const want = name.toLowerCase();
  for (const k of Object.keys(headers)) {
    if (k.toLowerCase() === want) {
      const v = headers[k];
      if (Array.isArray(v)) return v[0];
      return v;
    }
  }
  return undefined;
}

function singleValuedHeaders(headers: Record<string, string | string[] | undefined>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const k of Object.keys(headers)) {
    const v = headers[k];
    if (v === undefined) continue;
    out[k.toLowerCase()] = Array.isArray(v) ? (v[0] ?? "") : v;
  }
  return out;
}

function buildContext(app: string, name: string, n: NormalizedRequest): JobContext {
  const runId = pickHeader(n.headers, HeaderRunId) ?? "";
  const attemptStr = pickHeader(n.headers, HeaderAttempt) ?? "1";
  const attempt = Number.parseInt(attemptStr, 10);
  const fireTime = parseUnix(pickHeader(n.headers, HeaderFireTime));
  const fireTimeActual = parseUnix(pickHeader(n.headers, HeaderFireTimeActual));
  const previousSuccessTime = parseUnix(pickHeader(n.headers, HeaderPreviousSuccessTime));
  return {
    app,
    name,
    runId,
    attempt: Number.isFinite(attempt) && attempt > 0 ? attempt : 1,
    body: n.body,
    headers: singleValuedHeaders(n.headers),
    text: () => new TextDecoder("utf-8", { fatal: true }).decode(n.body),
    json: <T>() => JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(n.body)) as T,
    meta: { fireTime, fireTimeActual, previousSuccessTime },
    fireTime,
    fireTimeActual,
    previousSuccessTime,
  };
}

function parseUnix(v: string | undefined): Date | null {
  if (v === undefined) return null;
  const n = Number(v);
  if (!Number.isFinite(n)) return null;
  return new Date(n * 1000);
}

function errorResult(status: number, code: string, message: string): VerifyFailure {
  return {
    ok: false,
    status,
    code,
    message,
    toResponse: () => jsonResponse(status, { code, message }),
  };
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function responseFromHandlerResult(out: HandlerResult): Response {
  const status = out.status ?? (out.ok ? HTTP_OK : HTTP_INTERNAL);
  if (out.body === undefined) return new Response(null, { status });
  if (typeof out.body === "string") return new Response(out.body, { status });
  return new Response(out.body as BodyInit, { status });
}
