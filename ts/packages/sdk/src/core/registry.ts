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
  JobContext,
  JobDefinition,
  JobHandler,
  VerifyRequest,
  VerifyResult,
} from "./types.js";

export const MANIFEST_PATH = "/.well-known/cron-manifest";
export const TRIGGER_PATH_PREFIX = "/api/v1/scheduled/";

const HTTP_BAD_REQUEST = 400;
const HTTP_UNAUTHORIZED = 401;
const HTTP_NOT_FOUND = 404;

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
 * The instance is framework-agnostic. Wire it into your routes:
 *
 * ```ts
 * const cron = createCron({ app, baseUrl, secret });
 * cron.register({ name, schedule, handler });
 *
 * // GET /.well-known/cron-manifest
 * app.get('/.well-known/cron-manifest', async (req, res) => {
 *   const r = await cron.verify({ kind: 'manifest', method: req.method,
 *     path: req.path, body: new Uint8Array(0), headers: req.headers });
 *   if (!r.ok) return res.status(r.status).json({ code: r.code, message: r.message });
 *   res.json(cron.manifest());
 * });
 *
 * // POST /api/v1/scheduled/<name>
 * app.post('/api/v1/scheduled/:name', readRawBody, async (req, res) => {
 *   const r = await cron.verify({ kind: 'trigger', method: req.method,
 *     path: req.path, body: req.body, headers: req.headers });
 *   if (!r.ok) return res.status(r.status).json({ code: r.code, message: r.message });
 *   const out = await r.run();
 *   res.status(out.status ?? (out.ok ? 200 : 500)).end(out.body);
 * });
 * ```
 */
export function createCron(options: CreateCronOptions): CronInstance {
  const jobs = new Map<string, { def: JobDefinition; handler: JobHandler }>();

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
    const entry = jobs.get(ctx.name);
    if (!entry) throw new Error(`cronix: dispatch on unknown job: ${ctx.name}`);
    return entry.handler(ctx);
  };

  return {
    app: options.app,

    register(def) {
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
      jobs.set(def.name, { def, handler: def.handler });
    },

    manifest() {
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
    },

    async verify(req: VerifyRequest): Promise<VerifyResult> {
      const sig = pickHeader(req.headers, HeaderSignature);
      if (sig === undefined) {
        return {
          ok: false,
          status: HTTP_UNAUTHORIZED,
          code: "MissingSignature",
          message: `missing ${HeaderSignature} header`,
        };
      }
      const secrets = resolveSecrets();
      const sigResult = await verifySignature({
        secrets,
        method: req.method,
        path: req.path,
        body: req.body,
        header: sig,
        ...(req.now !== undefined ? { now: req.now } : {}),
        ...(req.maxSkewSeconds !== undefined ? { maxSkewSeconds: req.maxSkewSeconds } : {}),
      });
      if (!sigResult.ok) {
        return {
          ok: false,
          status: HTTP_UNAUTHORIZED,
          code: sigResult.error.code,
          message: sigResult.error.message,
        };
      }

      if (req.kind === "manifest") {
        if (req.method.toUpperCase() !== "GET") {
          return {
            ok: false,
            status: HTTP_BAD_REQUEST,
            code: "BadMethod",
            message: `manifest fetches must be GET, got ${req.method}`,
          };
        }
        if (req.path !== MANIFEST_PATH) {
          return {
            ok: false,
            status: HTTP_NOT_FOUND,
            code: "BadPath",
            message: `manifest path must be ${MANIFEST_PATH}, got ${req.path}`,
          };
        }
        return { ok: true, kind: "manifest", secretIndex: sigResult.value.secretIndex };
      }

      const name = triggerNameFromPath(req.path);
      if (!name) {
        return {
          ok: false,
          status: HTTP_NOT_FOUND,
          code: "BadPath",
          message: `trigger path must start with ${TRIGGER_PATH_PREFIX}, got ${req.path}`,
        };
      }
      const entry = jobs.get(name);
      if (!entry) {
        return {
          ok: false,
          status: HTTP_NOT_FOUND,
          code: "UnknownJob",
          message: `no registered job named ${name}`,
        };
      }
      const ctx = buildContext(options.app, name, req);
      return {
        ok: true,
        kind: "trigger",
        secretIndex: sigResult.value.secretIndex,
        ctx,
        run: () => dispatch(ctx),
      };
    },
  };
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

function buildContext(app: string, name: string, req: VerifyRequest): JobContext {
  const runId = pickHeader(req.headers, HeaderRunId) ?? "";
  const attemptStr = pickHeader(req.headers, HeaderAttempt) ?? "1";
  const attempt = Number.parseInt(attemptStr, 10);
  return {
    app,
    name,
    runId,
    attempt: Number.isFinite(attempt) && attempt > 0 ? attempt : 1,
    fireTime: parseUnix(pickHeader(req.headers, HeaderFireTime)),
    fireTimeActual: parseUnix(pickHeader(req.headers, HeaderFireTimeActual)),
    previousSuccessTime: parseUnix(pickHeader(req.headers, HeaderPreviousSuccessTime)),
    body: req.body,
    text: () => new TextDecoder("utf-8", { fatal: true }).decode(req.body),
    json: <T>() => JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(req.body)) as T,
  };
}

function parseUnix(v: string | undefined): Date | null {
  if (v === undefined) return null;
  const n = Number(v);
  if (!Number.isFinite(n)) return null;
  return new Date(n * 1000);
}
