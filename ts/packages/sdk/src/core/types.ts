import type { Job, NormalizedManifest } from "./manifest.js";

export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

/**
 * Declarative description of one cron job. Hand to `cron.register()`.
 *
 * `url` is **not** part of JobDefinition — the SDK derives it from the
 * `baseUrl` passed to createCron() and the conventional path
 * `/api/v1/scheduled/<name>`. Apps that need to override the URL convention
 * pass `urlOverride`.
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
  handler: JobHandler;
};

/**
 * Per-fire context handed to the registered handler. All fields are derived
 * from the verified incoming request.
 */
export type JobContext = {
  app: string;
  name: string;
  runId: string;
  attempt: number;
  fireTime: Date | null;
  fireTimeActual: Date | null;
  previousSuccessTime: Date | null;
  body: Uint8Array;
  /** Lazy UTF-8 text decode; throws on non-UTF-8. */
  text: () => string;
  /** Lazy JSON parse; throws on bad JSON / non-UTF-8. */
  json: <T = unknown>() => T;
};

export type HandlerResult = { ok: boolean; status?: number; body?: string | Uint8Array };

export type JobHandler = (ctx: JobContext) => HandlerResult | Promise<HandlerResult>;

/**
 * Inputs to `cron.verify()`. All fields are Web-Standards shapes so the
 * SDK runs unchanged on Node, Bun, Deno, Workers, and Edge.
 *
 * `kind` discriminates the request:
 *   - `'manifest'` — incoming GET on `/.well-known/cron-manifest`. Verify
 *     succeeds when the signature checks out.
 *   - `'trigger'` — incoming POST on `/api/v1/scheduled/<name>`. Verify
 *     succeeds when the signature checks out AND the named job exists in
 *     the registry; the JobContext is then built from the request headers.
 */
export type VerifyRequest = {
  kind: "manifest" | "trigger";
  method: string;
  path: string;
  body: Uint8Array;
  headers: Record<string, string | string[] | undefined>;
  /** Override "now" for tests. Defaults to Date.now()/1000. */
  now?: number;
  maxSkewSeconds?: number;
};

export type VerifyResult =
  | { ok: true; kind: "manifest"; secretIndex: number }
  | { ok: true; kind: "trigger"; secretIndex: number; ctx: JobContext; run: () => Promise<HandlerResult> }
  | { ok: false; status: number; code: string; message: string };

/**
 * Returned by `createCron()`. The complete public surface.
 *
 * The SDK is framework-agnostic. Wire it into your own routes:
 *
 * ```ts
 * // GET /.well-known/cron-manifest
 * const result = await cron.verify({ kind: 'manifest', method, path, body, headers });
 * if (!result.ok) return errorResponse(result);
 * return jsonResponse(cron.manifest());
 *
 * // POST /api/v1/scheduled/<name>
 * const result = await cron.verify({ kind: 'trigger', method, path, body, headers });
 * if (!result.ok) return errorResponse(result);
 * const out = await result.run();
 * return responseFromHandlerResult(out);
 * ```
 */
export type CronInstance = {
  app: string;
  register: (def: JobDefinition) => void;
  manifest: () => NormalizedManifest;
  verify: (req: VerifyRequest) => Promise<VerifyResult>;
};
