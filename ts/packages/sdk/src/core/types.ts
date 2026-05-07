import type { Job, NormalizedManifest } from "./manifest.js";

export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

/**
 * Generic environment shape, modelled on Hono's `Hono<{ Bindings, Variables }>`.
 *
 * - **Bindings** are app-scoped values supplied once at `createCron({env: {...}})`.
 *   Examples: a database client, a logger, a config object. Available everywhere
 *   as `ctx.env.<key>`.
 * - **Variables** are per-fire values supplied at `cron.handle(req, {vars: {...}})`
 *   (or `verifyTrigger` / `verifyManifest`). Examples: a trace id, a tenant id
 *   derived from the request. Available as `ctx.var.<key>`, plus runtime
 *   `ctx.get(key)` / `ctx.set(key, val)`. App-wide defaults can be set at
 *   `createCron({vars: {...}})`; per-fire vars are merged on top, winning on
 *   key collisions.
 *
 * ```ts
 * type Env = {
 *   Bindings: { db: Database; logger: Logger };
 *   Variables: { traceId: string };
 * };
 * const cron = createCron<Env>({ app, baseUrl, secret, env: { db, logger: console } });
 *
 * cron.register({
 *   name: "ping",
 *   schedule: "@hourly",
 *   handler: async (ctx) => {
 *     await ctx.env.db.query(...);          // typed Database
 *     ctx.env.logger.info(ctx.var.traceId); // typed string
 *     return { ok: true };
 *   },
 * });
 *
 * app.all(TRIGGER_PATH, (c) =>
 *   cron.handle(c.req.raw, { vars: { traceId: crypto.randomUUID() } }),
 * );
 * ```
 */
export type CronEnv = {
  Bindings?: Record<string, unknown>;
  Variables?: Record<string, unknown>;
};

/** Default empty env used when the developer doesn't supply a generic. */
export type DefaultEnv = { Bindings: Record<string, never>; Variables: Record<string, never> };

type Bindings<E extends CronEnv> = E["Bindings"] extends infer B
  ? B extends undefined
    ? Record<string, never>
    : B
  : Record<string, never>;
type Variables<E extends CronEnv> = E["Variables"] extends infer V
  ? V extends undefined
    ? Record<string, never>
    : V
  : Record<string, never>;

/**
 * Declarative description of one cron job. Hand to `cron.register()`.
 *
 * `url` is **not** part of JobDefinition — the SDK derives it from the
 * `baseUrl` passed to createCron() and the conventional path
 * `/api/v1/scheduled/<name>`. Apps that need to override the URL convention
 * pass `urlOverride`.
 *
 * `handler` is **optional**. Omit it to register a job for manifest purposes
 * only and bind the handler later via `cron.on(name, handler)` — useful when
 * handlers live in their own files.
 */
export type JobDefinition<E extends CronEnv = DefaultEnv> = {
  name: string;
  schedule?: string;
  schedules?: string[];
  timezone?: string;
  method?: HttpMethod;
  headers?: Record<string, string>;
  body?: string;
  urlOverride?: string;
  policy?: Job["policy"];
  auth?: Job["auth"];
  handler?: JobHandler<E>;
  /**
   * Skip HMAC verification for THIS job only — D-033. Falls back to the
   * instance-level `skipVerify` when unset. ⚠ Trust must come from the
   * network (mTLS, internal cluster service, etc.).
   *
   * Other per-job overrides (secret, replayWindowSeconds, enabled, tags)
   * are RFC-documented future direction and not yet wired in this SDK.
   */
  skipVerify?: boolean;
};

/**
 * Per-fire context handed to the registered handler. All fields are derived
 * from the verified incoming request, with optional app-scoped bindings
 * (set at `createCron`) and per-fire variables (set at `cron.handle`).
 *
 * Common fields are top-level. The fire-time triplet that most handlers don't
 * touch lives under `ctx.meta`. The flat `fireTime` / `fireTimeActual` /
 * `previousSuccessTime` aliases remain for backwards compatibility and are
 * tagged `@deprecated`.
 */
export type JobContext<E extends CronEnv = DefaultEnv> = {
  app: string;
  name: string;
  runId: string;
  attempt: number;
  body: Uint8Array;
  /**
   * `true` when this trigger arrived at a route configured with
   * `skipVerify: true` (instance- or job-level). Handlers SHOULD branch on
   * this for high-stakes work — e.g. refuse to mutate billing state from
   * an unverified request even if the surrounding network is trusted.
   */
  unverified: boolean;
  /** Lower-cased request headers, single-valued. Useful for handlers that branch on a custom trigger header. */
  headers: Record<string, string>;
  /** Lazy UTF-8 text decode; throws on non-UTF-8. */
  text: () => string;
  /** Lazy JSON parse; throws on bad JSON / non-UTF-8. */
  json: <T = unknown>() => T;
  meta: {
    fireTime: Date | null;
    fireTimeActual: Date | null;
    previousSuccessTime: Date | null;
  };
  /** App-scoped bindings, supplied at `createCron({env: ...})`. Empty object by default. */
  env: Bindings<E>;
  /** Per-fire variables, supplied at `cron.handle(req, {vars: ...})`. Empty object by default. */
  var: Variables<E>;
  /** Set a per-fire variable at runtime; mirrors Hono's `c.set(key, val)`. */
  set: <K extends keyof Variables<E>>(key: K, value: Variables<E>[K]) => void;
  /** Read a per-fire variable; mirrors Hono's `c.get(key)`. */
  get: <K extends keyof Variables<E>>(key: K) => Variables<E>[K];
  /** @deprecated Use `ctx.meta.fireTime`. Will be removed in v0.2. */
  fireTime: Date | null;
  /** @deprecated Use `ctx.meta.fireTimeActual`. Will be removed in v0.2. */
  fireTimeActual: Date | null;
  /** @deprecated Use `ctx.meta.previousSuccessTime`. Will be removed in v0.2. */
  previousSuccessTime: Date | null;
};

export type HandlerResult = { ok: boolean; status?: number; body?: string | Uint8Array };

export type JobHandler<E extends CronEnv = DefaultEnv> = (ctx: JobContext<E>) => HandlerResult | Promise<HandlerResult>;

/**
 * Headers shape accepted by the new verify methods. `Headers` (Web Fetch),
 * a plain Record, or undefined values are all accepted; the SDK normalizes.
 */
export type HeadersInput = Headers | Record<string, string | string[] | undefined>;

/**
 * Object form for verify inputs — for frameworks that don't expose a Web
 * `Request` (Node http, older Express). The new methods also accept a
 * `Request` directly; this shape is the fallback.
 */
export type VerifyRequestObject = {
  method: string;
  path: string;
  body: Uint8Array;
  headers: HeadersInput;
  /** Override "now" for tests. Defaults to Date.now()/1000. */
  now?: number;
  maxSkewSeconds?: number;
};

/** Either a Web Fetch `Request` or the object fallback. */
export type VerifyInput = Request | VerifyRequestObject;

/** Optional verify-side overrides — pin "now" and skew window (mainly for tests). */
export type VerifyTimeOptions = {
  now?: number;
  maxSkewSeconds?: number;
};

/** Options accepted by the new verify methods + handle: time-pin plus per-fire variables. */
export type VerifyHandleOptions<E extends CronEnv = DefaultEnv> = VerifyTimeOptions & {
  vars?: Variables<E>;
};

/**
 * Common error shape on the new verify methods. `toResponse()` builds a
 * ready-to-send Web `Response` so the route can `return r.toResponse()`
 * without assembling the JSON body by hand.
 */
export type VerifyFailure = {
  ok: false;
  status: number;
  code: string;
  message: string;
  toResponse(): Response;
};

export type VerifyManifestResult = { ok: true; secretIndex: number } | VerifyFailure;

export type VerifyTriggerResult<E extends CronEnv = DefaultEnv> =
  | { ok: true; secretIndex: number; ctx: JobContext<E>; run: () => Promise<HandlerResult> }
  | VerifyFailure;

/**
 * @deprecated Pre-v0.2 input shape with a `kind` discriminator. Use
 * `cron.verifyManifest(req)` or `cron.verifyTrigger(req)` instead — both
 * accept either a Web `Request` or a `VerifyRequestObject`.
 */
export type VerifyRequest = {
  kind: "manifest" | "trigger";
  method: string;
  path: string;
  body: Uint8Array;
  headers: Record<string, string | string[] | undefined>;
  now?: number;
  maxSkewSeconds?: number;
};

/**
 * @deprecated Result of the legacy `cron.verify({kind, …})`. Use the
 * `verifyManifest` / `verifyTrigger` methods instead.
 */
export type VerifyResult =
  | { ok: true; kind: "manifest"; secretIndex: number }
  | { ok: true; kind: "trigger"; secretIndex: number; ctx: JobContext; run: () => Promise<HandlerResult> }
  | { ok: false; status: number; code: string; message: string };

/**
 * Returned by `createCron()`. The complete public surface, generic on the
 * environment type so handlers see typed `ctx.env` and `ctx.var`.
 *
 * Three tiers of integration are supported, in order of less → more glue:
 *
 * **Tier 1 — zero glue**: `cron.handle(request, {vars})` returns a fully-formed
 * `Response`. Wire two routes (manifest, trigger) and you're done.
 *
 * ```ts
 * app.all(MANIFEST_PATH, c => cron.handle(c.req.raw));
 * app.all(TRIGGER_PATH, c => cron.handle(c.req.raw, { vars: { traceId } }));
 * ```
 *
 * **Tier 2 — explicit verify + dispatch**: `cron.verifyManifest()` and
 * `cron.verifyTrigger()` return a verdict; on error, call `r.toResponse()`;
 * on success, run the handler and shape your own response. Useful when you
 * want logging or metrics between the verify and the run.
 *
 * **Tier 3 — late handler binding**: `cron.register({name, schedule})`
 * declares a job with no handler; bind it later from another file with
 * `cron.on(name, handler)`. Lets handlers live in their own modules.
 */
/**
 * Pluggable logger shape — anything with these four methods works
 * (console, pino, winston, custom). Defaults to `console`.
 */
export type Logger = {
  debug?: (...args: unknown[]) => void;
  info: (...args: unknown[]) => void;
  warn: (...args: unknown[]) => void;
  error: (...args: unknown[]) => void;
};

/**
 * Observability hooks — fire-and-forget. Errors thrown inside any hook
 * are caught and logged; they MUST NOT propagate to the response.
 * D-032.
 */
export type Hooks<E extends CronEnv = DefaultEnv> = {
  /** Failed verify result + the original request that failed. */
  onVerifyFailure?: (failure: Omit<VerifyFailure, "toResponse">, req: VerifyInput) => void | Promise<void>;
  /** Verified context, just before the handler runs. */
  onTriggerStart?: (ctx: JobContext<E>) => void | Promise<void>;
  /** Verified context + handler result + elapsed milliseconds. */
  onTriggerSuccess?: (ctx: JobContext<E>, result: HandlerResult, ms: number) => void | Promise<void>;
  /** Verified context + thrown error + elapsed milliseconds. */
  onTriggerError?: (ctx: JobContext<E>, err: unknown, ms: number) => void | Promise<void>;
  /** Authenticated manifest fetch — no body, just the Web Request. */
  onManifestRequest?: (req: VerifyInput) => void | Promise<void>;
};

/**
 * Override the default error-response shape. Receives the structured
 * failure; returns a Web Response. Default is plain JSON `{code, message}`
 * with the appropriate status code.
 */
export type ErrorResponseFn = (failure: Omit<VerifyFailure, "toResponse">) => Response;

export type CronInstance<E extends CronEnv = DefaultEnv> = {
  app: string;
  /** Declare a job. `def.handler` may be omitted; bind later via `cron.on`. */
  register: (def: JobDefinition<E>) => void;
  /** Bind (or rebind) a handler to an already-registered job. */
  on: (name: string, handler: JobHandler<E>) => void;
  manifest: () => NormalizedManifest;
  /** Verify a request to the manifest endpoint. Accepts a Web `Request` or `VerifyRequestObject`. */
  verifyManifest: (req: VerifyInput, opts?: VerifyHandleOptions<E>) => Promise<VerifyManifestResult>;
  /** Verify a request to a trigger endpoint. Accepts a Web `Request` or `VerifyRequestObject`. */
  verifyTrigger: (req: VerifyInput, opts?: VerifyHandleOptions<E>) => Promise<VerifyTriggerResult<E>>;
  /** Zero-glue dispatcher: routes manifest vs trigger by URL path; always returns a `Response`. */
  handle: (req: Request, opts?: VerifyHandleOptions<E>) => Promise<Response>;
  /** @deprecated Use `verifyManifest` / `verifyTrigger`. Removed in v0.2. */
  verify: (req: VerifyRequest) => Promise<VerifyResult>;
};
