import { REPLAY_WINDOW_DEFAULT_SECONDS, verify as verifySignature } from "./auth.js";
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
  CronEnv,
  CronInstance,
  DefaultEnv,
  ErrorResponseFn,
  HandlerResult,
  HeadersInput,
  Hooks,
  JobContext,
  JobDefinition,
  JobHandler,
  Logger,
  VerifyFailure,
  VerifyHandleOptions,
  VerifyInput,
  VerifyManifestResult,
  VerifyRequest,
  VerifyRequestObject,
  VerifyResult,
  VerifyTimeOptions,
  VerifyTriggerResult,
} from "./types.js";

/** D-034: SDKs MUST reject replay-window values below this minimum. */
export const REPLAY_WINDOW_MIN_SECONDS = 30;

export const MANIFEST_PATH = "/.well-known/cron-manifest";
export const TRIGGER_PATH_PREFIX = "/api/v1/scheduled/";

const HTTP_OK = 200;
const HTTP_BAD_REQUEST = 400;
const HTTP_UNAUTHORIZED = 401;
const HTTP_NOT_FOUND = 404;
const HTTP_INTERNAL = 500;
const HTTP_UNAVAILABLE = 503;

export type CreateCronOptions<E extends CronEnv = DefaultEnv> = {
  app: string;
  /** Base URL the manifest publishes for trigger endpoints. No trailing slash required. */
  baseUrl: string;
  /** One or more secrets. Function form is re-evaluated on every verify call. Ignored when `skipVerify: true`. */
  secret: string | string[] | (() => string | string[]);
  /** App-scoped bindings available as `ctx.env` to every handler. Hono parity: `env`. */
  env?: E["Bindings"];
  /**
   * Default per-fire variables. Merged with anything passed to
   * `cron.handle(req, {vars})` (or `verifyTrigger`/`verifyManifest`); the
   * per-fire `vars` win on key collisions. Useful for app-wide defaults
   * (e.g. a fixed `region` or `service` tag) that any route can override.
   */
  vars?: E["Variables"];
  /**
   * D-031 — disable HMAC verification on incoming requests entirely.
   * Trust must come from the network boundary (mTLS, internal cluster
   * service, dev environment). Outgoing requests from `cronix trigger`
   * are still signed; the wire format is unchanged. Per-job overrides
   * via `JobDefinition.skipVerify` take precedence.
   *
   * ⚠ Footgun. The SDK emits a warn-level log line when this is true.
   */
  skipVerify?: boolean;
  /**
   * D-032 — fire-and-forget observability hooks. Errors thrown inside
   * any hook are caught and logged via `logger.error`; they MUST NOT
   * break the request.
   */
  hooks?: Hooks<E>;
  /**
   * Override the default error-response shape. Receives the structured
   * failure (without `toResponse`); returns a Web Response. Default is
   * plain JSON `{code, message}` with the appropriate status code.
   */
  errorResponse?: ErrorResponseFn;
  /**
   * Pluggable logger for SDK-internal events (boot warnings, hook errors,
   * etc.). Defaults to `console`.
   */
  logger?: Logger;
  /**
   * D-034 — replay window for the HMAC timestamp check, in seconds.
   * Defaults to 300 (per §Replay window). MUST be ≥ 30; the SDK throws
   * at instance construction if violated. Per-call `maxSkewSeconds`
   * overrides this for that call.
   */
  replayWindowSeconds?: number;
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
export function createCron<E extends CronEnv = DefaultEnv>(options: CreateCronOptions<E>): CronInstance<E> {
  const jobs = new Map<string, { def: JobDefinition<E> }>();
  const handlers = new Map<string, JobHandler<E>>();
  const bindings = (options.env ?? {}) as NonNullable<E["Bindings"]>;
  const defaultVars = (options.vars ?? {}) as Record<string, unknown>;
  const logger: Logger = options.logger ?? (console as unknown as Logger);
  const hooks: Hooks<E> = options.hooks ?? {};
  const errorResponse: ErrorResponseFn | undefined = options.errorResponse;

  // D-034: validate replay window at construction time, before any traffic.
  if (options.replayWindowSeconds !== undefined && options.replayWindowSeconds < REPLAY_WINDOW_MIN_SECONDS) {
    throw new Error(
      `cronix: replayWindowSeconds must be >= ${REPLAY_WINDOW_MIN_SECONDS}s (got ${options.replayWindowSeconds}; D-034)`,
    );
  }
  const replayWindow = options.replayWindowSeconds ?? REPLAY_WINDOW_DEFAULT_SECONDS;

  // D-031: skipVerify is loud — emit one warn line at boot.
  if (options.skipVerify === true) {
    logger.warn(
      `cronix: skipVerify=true — HMAC verification disabled for app "${options.app}". ` +
        `Trust must come from the network boundary (mTLS, internal service, dev only).`,
    );
  }

  // Run a hook with errors swallowed + logged. Awaits if it returns a promise.
  const runHook = async <Args extends unknown[]>(
    name: keyof Hooks<E>,
    fn: ((...a: Args) => void | Promise<void>) | undefined,
    ...args: Args
  ): Promise<void> => {
    if (!fn) return;
    try {
      await fn(...args);
    } catch (e) {
      logger.error(`cronix: hook ${String(name)} threw —`, e);
    }
  };

  const resolveSecrets = (): string[] => {
    const raw = typeof options.secret === "function" ? options.secret() : options.secret;
    return Array.isArray(raw) ? raw : [raw];
  };

  // Per-instance errorResult — closes over the optional errorResponse override
  // so toResponse() goes through it when the user supplied one.
  const errorResult = (status: number, code: string, message: string): VerifyFailure => {
    const failure = { ok: false as const, status, code, message };
    return {
      ...failure,
      toResponse: () => (errorResponse ? errorResponse(failure) : jsonResponse(status, { code, message })),
    };
  };

  const buildJob = (def: JobDefinition<E>): Job => ({
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

  const dispatch = async (ctx: JobContext<E>): Promise<HandlerResult> => {
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
    const start = nowMs();
    await runHook("onTriggerStart", hooks.onTriggerStart, ctx);
    try {
      const result = await handler(ctx);
      const ms = nowMs() - start;
      if (result.ok) {
        await runHook("onTriggerSuccess", hooks.onTriggerSuccess, ctx, result, ms);
      } else {
        await runHook("onTriggerError", hooks.onTriggerError, ctx, result, ms);
      }
      return result;
    } catch (e) {
      const ms = nowMs() - start;
      await runHook("onTriggerError", hooks.onTriggerError, ctx, e, ms);
      throw e;
    }
  };

  const register = (def: JobDefinition<E>): void => {
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

  const on = (name: string, handler: JobHandler<E>): void => {
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
    bypass: boolean,
  ): Promise<{ ok: true; secretIndex: number; unverified: boolean } | VerifyFailure> => {
    // D-031 / D-033: skipVerify accepts everything without checking the HMAC.
    // Returns the sentinel secretIndex -1 so callers can audit "this trigger
    // was unverified" via JobContext.unverified.
    if (bypass) return { ok: true, secretIndex: -1, unverified: true };

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
      // Per-call maxSkewSeconds (from VerifyRequestObject or test override) wins;
      // otherwise the instance-level replayWindow applies.
      maxSkewSeconds: n.maxSkewSeconds ?? replayWindow,
    });
    if (!sigResult.ok) {
      return errorResult(HTTP_UNAUTHORIZED, sigResult.error.code, sigResult.error.message);
    }
    return { ok: true, secretIndex: sigResult.value.secretIndex, unverified: false };
  };

  const verifyManifest = async (req: VerifyInput, opts?: VerifyHandleOptions<E>): Promise<VerifyManifestResult> => {
    const n = await normalizeVerifyInput(req, opts);
    if (n.method.toUpperCase() !== "GET") {
      const f = errorResult(HTTP_BAD_REQUEST, "BadMethod", `manifest fetches must be GET, got ${n.method}`);
      await runHook("onVerifyFailure", hooks.onVerifyFailure, stripToResponse(f), req);
      return f;
    }
    if (n.path !== MANIFEST_PATH) {
      const f = errorResult(HTTP_NOT_FOUND, "BadPath", `manifest path must be ${MANIFEST_PATH}, got ${n.path}`);
      await runHook("onVerifyFailure", hooks.onVerifyFailure, stripToResponse(f), req);
      return f;
    }
    const r = await verifySignatureOnly(n, options.skipVerify === true);
    if (!r.ok) {
      await runHook("onVerifyFailure", hooks.onVerifyFailure, stripToResponse(r), req);
      return r;
    }
    await runHook("onManifestRequest", hooks.onManifestRequest, req);
    return { ok: true, secretIndex: r.secretIndex };
  };

  const verifyTrigger = async (req: VerifyInput, opts?: VerifyHandleOptions<E>): Promise<VerifyTriggerResult<E>> => {
    const n = await normalizeVerifyInput(req, opts);

    // Resolve the job name first so we can check per-job skipVerify (D-033).
    const name = triggerNameFromPath(n.path);
    if (!name) {
      const f = errorResult(
        HTTP_NOT_FOUND,
        "BadPath",
        `trigger path must start with ${TRIGGER_PATH_PREFIX}, got ${n.path}`,
      );
      await runHook("onVerifyFailure", hooks.onVerifyFailure, stripToResponse(f), req);
      return f;
    }
    const entry = jobs.get(name);
    if (!entry) {
      const f = errorResult(HTTP_NOT_FOUND, "UnknownJob", `no registered job named ${name}`);
      await runHook("onVerifyFailure", hooks.onVerifyFailure, stripToResponse(f), req);
      return f;
    }
    const bypass = entry.def.skipVerify ?? options.skipVerify === true;

    const r = await verifySignatureOnly(n, bypass);
    if (!r.ok) {
      await runHook("onVerifyFailure", hooks.onVerifyFailure, stripToResponse(r), req);
      return r;
    }

    const mergedVars = { ...defaultVars, ...((opts?.vars ?? {}) as Record<string, unknown>) } as NonNullable<
      E["Variables"]
    >;
    const ctx = buildContext<E>(options.app, name, n, bindings, mergedVars, r.unverified);
    return {
      ok: true,
      secretIndex: r.secretIndex,
      ctx,
      run: () => dispatch(ctx),
    };
  };

  const handle = async (req: Request, opts?: VerifyHandleOptions<E>): Promise<Response> => {
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
    // Legacy ctx is the un-typed JobContext (DefaultEnv); cast for the deprecated shape.
    return { ok: true, kind: "trigger", secretIndex: r.secretIndex, ctx: r.ctx as unknown as JobContext, run: r.run };
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

function buildContext<E extends CronEnv>(
  app: string,
  name: string,
  n: NormalizedRequest,
  env: NonNullable<E["Bindings"]>,
  initialVars: NonNullable<E["Variables"]> | undefined,
  unverified = false,
): JobContext<E> {
  const runId = pickHeader(n.headers, HeaderRunId) ?? "";
  const attemptStr = pickHeader(n.headers, HeaderAttempt) ?? "1";
  const attempt = Number.parseInt(attemptStr, 10);
  const fireTime = parseUnix(pickHeader(n.headers, HeaderFireTime));
  const fireTimeActual = parseUnix(pickHeader(n.headers, HeaderFireTimeActual));
  const previousSuccessTime = parseUnix(pickHeader(n.headers, HeaderPreviousSuccessTime));
  const vars = { ...((initialVars ?? {}) as Record<string, unknown>) };
  const ctx = {
    app,
    name,
    runId,
    attempt: Number.isFinite(attempt) && attempt > 0 ? attempt : 1,
    body: n.body,
    unverified,
    headers: singleValuedHeaders(n.headers),
    text: () => new TextDecoder("utf-8", { fatal: true }).decode(n.body),
    json: <T>() => JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(n.body)) as T,
    meta: { fireTime, fireTimeActual, previousSuccessTime },
    env,
    var: vars as JobContext<E>["var"],
    set: <K extends keyof JobContext<E>["var"]>(key: K, value: JobContext<E>["var"][K]) => {
      (vars as Record<string, unknown>)[key as string] = value;
    },
    get: <K extends keyof JobContext<E>["var"]>(key: K) =>
      (vars as Record<string, unknown>)[key as string] as JobContext<E>["var"][K],
    fireTime,
    fireTimeActual,
    previousSuccessTime,
  };
  return ctx as JobContext<E>;
}

function nowMs(): number {
  return typeof performance !== "undefined" && typeof performance.now === "function" ? performance.now() : Date.now();
}

function stripToResponse(f: VerifyFailure): Omit<VerifyFailure, "toResponse"> {
  // Hooks should see the structured fields, not the response builder.
  return { ok: false, status: f.status, code: f.code, message: f.message };
}

function parseUnix(v: string | undefined): Date | null {
  if (v === undefined) return null;
  const n = Number(v);
  if (!Number.isFinite(n)) return null;
  return new Date(n * 1000);
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
