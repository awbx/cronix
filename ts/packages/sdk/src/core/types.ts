import type { Job, NormalizedManifest } from "./manifest.js";

export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

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
export type JobDefinition = {
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
  handler?: JobHandler;
};

/**
 * Per-fire context handed to the registered handler. All fields are derived
 * from the verified incoming request.
 *
 * Common fields are top-level. The fire-time triplet that most handlers don't
 * touch lives under `ctx.meta`. The flat `fireTime` / `fireTimeActual` /
 * `previousSuccessTime` aliases remain for backwards compatibility and are
 * tagged `@deprecated` — they will be removed in v0.2.
 */
export type JobContext = {
  app: string;
  name: string;
  runId: string;
  attempt: number;
  body: Uint8Array;
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
  /** @deprecated Use `ctx.meta.fireTime`. Will be removed in v0.2. */
  fireTime: Date | null;
  /** @deprecated Use `ctx.meta.fireTimeActual`. Will be removed in v0.2. */
  fireTimeActual: Date | null;
  /** @deprecated Use `ctx.meta.previousSuccessTime`. Will be removed in v0.2. */
  previousSuccessTime: Date | null;
};

export type HandlerResult = { ok: boolean; status?: number; body?: string | Uint8Array };

export type JobHandler = (ctx: JobContext) => HandlerResult | Promise<HandlerResult>;

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

export type VerifyTriggerResult =
  | { ok: true; secretIndex: number; ctx: JobContext; run: () => Promise<HandlerResult> }
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
 * Returned by `createCron()`. The complete public surface.
 *
 * Three tiers of integration are supported, in order of less → more glue:
 *
 * **Tier 1 — zero glue**: `cron.handle(request)` returns a fully-formed
 * `Response`. Wire two routes (manifest, trigger) and you're done.
 *
 * ```ts
 * app.all(MANIFEST_PATH, c => cron.handle(c.req.raw));
 * app.all('/api/v1/scheduled/:name', c => cron.handle(c.req.raw));
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
export type CronInstance = {
  app: string;
  /** Declare a job. `def.handler` may be omitted; bind later via `cron.on`. */
  register: (def: JobDefinition) => void;
  /** Bind (or rebind) a handler to an already-registered job. */
  on: (name: string, handler: JobHandler) => void;
  manifest: () => NormalizedManifest;
  /** Verify a request to the manifest endpoint. Accepts a Web `Request` or `VerifyRequestObject`. */
  verifyManifest: (req: VerifyInput, opts?: VerifyTimeOptions) => Promise<VerifyManifestResult>;
  /** Verify a request to a trigger endpoint. Accepts a Web `Request` or `VerifyRequestObject`. */
  verifyTrigger: (req: VerifyInput, opts?: VerifyTimeOptions) => Promise<VerifyTriggerResult>;
  /** Zero-glue dispatcher: routes manifest vs trigger by URL path; always returns a `Response`. */
  handle: (req: Request, opts?: VerifyTimeOptions) => Promise<Response>;
  /** @deprecated Use `verifyManifest` / `verifyTrigger`. Removed in v0.2. */
  verify: (req: VerifyRequest) => Promise<VerifyResult>;
};
