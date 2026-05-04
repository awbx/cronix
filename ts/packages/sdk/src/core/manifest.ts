import { z } from "zod";
import { validateSchedule } from "./cron.js";
import { err, ok, type Result } from "./result.js";

const JOB_NAME = /^[a-z][a-z0-9-]{0,62}$/;
const APP_ID = /^[a-z][a-z0-9-]{0,62}$/;
const SECRET_REF = /^[a-zA-Z][a-zA-Z0-9_:./-]{0,127}$/;

const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE"] as const;
const CONCURRENCY = ["Allow", "Forbid", "Replace"] as const;
const CONCURRENCY_SCOPE = ["host", "global"] as const;

export const TIMEOUT_MIN = 1;
export const TIMEOUT_MAX = 600;
export const TIMEOUT_DEFAULT = 60;
export const RETRY_ATTEMPTS_MIN = 1;
export const RETRY_ATTEMPTS_MAX = 10;
export const RETRY_ATTEMPTS_DEFAULT = 3;
export const RETRY_BACKOFF_MIN_DEFAULT = 1;
export const RETRY_BACKOFF_MAX_DEFAULT = 60;
export const REPLAY_WINDOW_DEFAULT_SECONDS = 300;

const scheduleString = z
  .string()
  .min(1)
  .superRefine((value, ctx) => {
    const e = validateSchedule(value);
    if (e) ctx.addIssue({ code: "custom", message: e });
  });

const requestSchema = z.object({
  method: z.enum(HTTP_METHODS).optional(),
  url: z
    .string()
    .url()
    .refine((u) => /^https?:\/\//i.test(u), { message: "url must be http(s)" }),
  headers: z.record(z.string(), z.string()).optional(),
  body: z.string().optional(),
});

const retrySchema = z
  .object({
    max_attempts: z.number().int().min(RETRY_ATTEMPTS_MIN).max(RETRY_ATTEMPTS_MAX).optional(),
    min_seconds: z.number().int().min(0).optional(),
    max_seconds: z.number().int().min(1).optional(),
  })
  .refine((r) => r.min_seconds === undefined || r.max_seconds === undefined || r.min_seconds <= r.max_seconds, {
    message: "retries.min_seconds must be <= retries.max_seconds",
  });

const policySchema = z.object({
  concurrency: z.enum(CONCURRENCY).optional(),
  concurrency_scope: z.enum(CONCURRENCY_SCOPE).optional(),
  timeout_seconds: z.number().int().min(TIMEOUT_MIN).max(TIMEOUT_MAX).optional(),
  retries: retrySchema.optional(),
});

const authSchema = z.object({
  secret_refs: z.array(z.string().regex(SECRET_REF)).min(1).max(8).optional(),
});

const jobSchemaBase = z.object({
  name: z.string().regex(JOB_NAME, "job name must match ^[a-z][a-z0-9-]{0,62}$"),
  schedule: scheduleString.optional(),
  schedules: z.array(scheduleString).min(1).max(64).optional(),
  timezone: z.string().optional(),
  request: requestSchema,
  policy: policySchema.optional(),
  auth: authSchema.optional(),
});

const jobSchema = jobSchemaBase.refine((j) => (j.schedule === undefined) !== (j.schedules === undefined), {
  message: "exactly one of `schedule` or `schedules` must be set",
});

export const manifestSchema = z
  .object({
    version: z.literal(1),
    app: z.string().regex(APP_ID, "app id must match ^[a-z][a-z0-9-]{0,62}$"),
    jobs: z.array(jobSchema).min(1).max(256),
  })
  .superRefine((m, ctx) => {
    const seen = new Set<string>();
    m.jobs.forEach((j, i) => {
      if (seen.has(j.name)) {
        ctx.addIssue({ code: "custom", path: ["jobs", i, "name"], message: `duplicate job name: ${j.name}` });
      }
      seen.add(j.name);
    });
  });

export type Manifest = z.infer<typeof manifestSchema>;
export type Job = z.infer<typeof jobSchema>;

export type NormalizedRetry = {
  max_attempts: number;
  min_seconds: number;
  max_seconds: number;
};

export type NormalizedPolicy = {
  concurrency: (typeof CONCURRENCY)[number];
  concurrency_scope: (typeof CONCURRENCY_SCOPE)[number];
  timeout_seconds: number;
  retries: NormalizedRetry;
};

export type NormalizedRequest = {
  method: (typeof HTTP_METHODS)[number];
  url: string;
  headers: Record<string, string>;
  body: string;
};

export type NormalizedJob = {
  name: string;
  schedules: string[];
  timezone: string;
  request: NormalizedRequest;
  policy: NormalizedPolicy;
  auth: { secret_refs: string[] };
};

export type NormalizedManifest = {
  version: 1;
  app: string;
  jobs: NormalizedJob[];
};

export type ManifestError = {
  code: "ManifestInvalid";
  issues: { path: (string | number)[]; message: string; code: string }[];
};

const toPathSegment = (k: PropertyKey): string | number => (typeof k === "number" ? k : String(k));

export function parseManifest(input: unknown): Result<Manifest, ManifestError> {
  const result = manifestSchema.safeParse(input);
  if (result.success) return ok(result.data);
  return err({
    code: "ManifestInvalid",
    issues: result.error.issues.map((i) => ({
      path: i.path.map(toPathSegment),
      message: i.message,
      code: i.code,
    })),
  });
}

const sortObjectKeys = (h: Record<string, string>): Record<string, string> => {
  const out: Record<string, string> = {};
  for (const k of Object.keys(h).sort()) out[k] = h[k] as string;
  return out;
};

export function applyDefaults(manifest: Manifest): NormalizedManifest {
  const jobs: NormalizedJob[] = manifest.jobs
    .map((j): NormalizedJob => {
      const schedules = j.schedules ?? (j.schedule !== undefined ? [j.schedule] : []);
      const policy = j.policy ?? {};
      const retries = policy.retries ?? {};
      return {
        name: j.name,
        schedules: schedules.map((s) => s.trim()),
        timezone: j.timezone ?? "UTC",
        request: {
          method: j.request.method ?? "POST",
          url: j.request.url,
          headers: sortObjectKeys(j.request.headers ?? {}),
          body: j.request.body ?? "",
        },
        policy: {
          concurrency: policy.concurrency ?? "Forbid",
          concurrency_scope: policy.concurrency_scope ?? "host",
          timeout_seconds: policy.timeout_seconds ?? TIMEOUT_DEFAULT,
          retries: {
            max_attempts: retries.max_attempts ?? RETRY_ATTEMPTS_DEFAULT,
            min_seconds: retries.min_seconds ?? RETRY_BACKOFF_MIN_DEFAULT,
            max_seconds: retries.max_seconds ?? RETRY_BACKOFF_MAX_DEFAULT,
          },
        },
        auth: {
          secret_refs: j.auth?.secret_refs ?? [],
        },
      };
    })
    .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));

  return {
    version: 1,
    app: manifest.app,
    jobs,
  };
}

/**
 * Canonical JSON serialization for a normalized manifest.
 *
 * Two implementations (TS and Go) must produce byte-identical output for the
 * same NormalizedManifest. The contract: object keys are emitted in a fixed
 * order (the order in which fields are defined on the NormalizedManifest /
 * NormalizedJob types), arrays preserve their order (jobs already sorted by
 * applyDefaults), and there is no whitespace.
 */
export function canonicalize(m: NormalizedManifest): string {
  const job = (j: NormalizedJob) =>
    JSON.stringify({
      name: j.name,
      schedules: j.schedules,
      timezone: j.timezone,
      request: {
        method: j.request.method,
        url: j.request.url,
        headers: j.request.headers,
        body: j.request.body,
      },
      policy: {
        concurrency: j.policy.concurrency,
        concurrency_scope: j.policy.concurrency_scope,
        timeout_seconds: j.policy.timeout_seconds,
        retries: {
          max_attempts: j.policy.retries.max_attempts,
          min_seconds: j.policy.retries.min_seconds,
          max_seconds: j.policy.retries.max_seconds,
        },
      },
      auth: {
        secret_refs: j.auth.secret_refs,
      },
    });
  const head = `{"version":1,"app":${JSON.stringify(m.app)},"jobs":[`;
  const body = m.jobs.map(job).join(",");
  return `${head}${body}]}`;
}
