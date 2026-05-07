/**
 * Standalone verify utilities — D-035.
 *
 * Drop-in helpers for routes that don't fit the high-level `cron.handle`
 * shape: custom middleware, polyglot routing, testing flows that want to
 * sign or verify without booting an instance, etc.
 *
 * The verbose name (`verifyTriggerRequest` / `verifyManifestRequest`)
 * mirrors the per-instance methods (`cron.verifyTrigger` /
 * `cron.verifyManifest`) so apps can swap one for the other without
 * reading the docs.
 */
import { REPLAY_WINDOW_DEFAULT_SECONDS, sign as signImpl, verify as verifyImpl } from "./auth.js";
import { HeaderSignature } from "./headers.js";

const HTTP_OK = 200;
const HTTP_BAD_REQUEST = 400;
const HTTP_UNAUTHORIZED = 401;
const HTTP_NOT_FOUND = 404;

const MANIFEST_PATH_LITERAL = "/.well-known/cron-manifest";
const TRIGGER_PATH_PREFIX_LITERAL = "/api/v1/scheduled/";

export type StandaloneVerifyOptions = {
  /** One or more secrets. Function form is re-evaluated on every call. */
  secret: string | string[] | (() => string | string[]);
  /** Override "now" for tests; unix seconds. */
  now?: number;
  /** Replay window in seconds. Defaults to 300 (per §Replay window). */
  replayWindowSeconds?: number;
};

export type StandaloneVerifyFailure = {
  ok: false;
  status: number;
  code: string;
  message: string;
};

export type VerifyManifestSuccess = { ok: true; secretIndex: number };

export type VerifyTriggerSuccess = {
  ok: true;
  secretIndex: number;
  /** Job name parsed from the URL path. */
  name: string;
  /** Lower-cased headers. */
  headers: Record<string, string>;
  body: Uint8Array;
};

/**
 * Verify a signed manifest fetch (`GET /.well-known/cron-manifest`).
 * Returns a structured verdict; never throws.
 *
 * ```ts
 * const r = await verifyManifestRequest(req, { secret });
 * if (!r.ok) return new Response(r.message, { status: r.status });
 * return Response.json(myManifest);
 * ```
 */
export async function verifyManifestRequest(
  req: Request,
  opts: StandaloneVerifyOptions,
): Promise<VerifyManifestSuccess | StandaloneVerifyFailure> {
  const url = new URL(req.url);
  if (req.method.toUpperCase() !== "GET") {
    return fail(HTTP_BAD_REQUEST, "BadMethod", `manifest fetches must be GET, got ${req.method}`);
  }
  if (url.pathname !== MANIFEST_PATH_LITERAL) {
    return fail(HTTP_NOT_FOUND, "BadPath", `manifest path must be ${MANIFEST_PATH_LITERAL}, got ${url.pathname}`);
  }
  return verifyHmac(req, url.pathname, opts);
}

/**
 * Verify a signed trigger request (`POST /api/v1/scheduled/<name>`).
 * Returns the parsed job name, headers, and body — does NOT dispatch a
 * handler. Wire your own routing to `cron.dispatch` (or call a registered
 * handler) yourself.
 *
 * ```ts
 * const r = await verifyTriggerRequest(req, { secret });
 * if (!r.ok) return new Response(r.message, { status: r.status });
 * await myHandlers[r.name](r.body, r.headers);
 * return new Response("ok");
 * ```
 */
export async function verifyTriggerRequest(
  req: Request,
  opts: StandaloneVerifyOptions,
): Promise<VerifyTriggerSuccess | StandaloneVerifyFailure> {
  const url = new URL(req.url);
  if (!url.pathname.startsWith(TRIGGER_PATH_PREFIX_LITERAL)) {
    return fail(
      HTTP_NOT_FOUND,
      "BadPath",
      `trigger path must start with ${TRIGGER_PATH_PREFIX_LITERAL}, got ${url.pathname}`,
    );
  }
  const name = url.pathname.slice(TRIGGER_PATH_PREFIX_LITERAL.length);
  if (name.length === 0 || name.includes("/")) {
    return fail(HTTP_NOT_FOUND, "BadPath", `trigger path has no job name: ${url.pathname}`);
  }

  const verdict = await verifyHmac(req, url.pathname, opts);
  if (!verdict.ok) return verdict;

  const headers: Record<string, string> = {};
  req.headers.forEach((v, k) => {
    headers[k.toLowerCase()] = v;
  });
  const body = new Uint8Array(await req.clone().arrayBuffer());
  return { ok: true, secretIndex: verdict.secretIndex, name, headers, body };
}

/**
 * Sign an outbound request — useful for tests, custom triggers, or any
 * caller that wants to mint a `cronix-signature` header without the full
 * `cronix trigger` shim.
 */
export async function signRequest(opts: {
  method: string;
  path: string;
  body: Uint8Array;
  secret: string;
  /** Override the timestamp; unix seconds. Defaults to now. */
  timestamp?: number;
}): Promise<{ header: string; timestamp: number }> {
  return signImpl(opts);
}

/* ----- internal ----- */

async function verifyHmac(
  req: Request,
  path: string,
  opts: StandaloneVerifyOptions,
): Promise<VerifyManifestSuccess | StandaloneVerifyFailure> {
  const sig = req.headers.get(HeaderSignature);
  if (sig === null) {
    return fail(HTTP_UNAUTHORIZED, "MissingSignature", `missing ${HeaderSignature} header`);
  }
  const secrets = resolveSecrets(opts.secret);
  if (secrets.length === 0) {
    return fail(HTTP_UNAUTHORIZED, "MalformedHeader", "verify: at least one secret is required");
  }
  const body = new Uint8Array(await req.clone().arrayBuffer());
  const result = await verifyImpl({
    secrets,
    method: req.method,
    path,
    body,
    header: sig,
    ...(opts.now !== undefined ? { now: opts.now } : {}),
    maxSkewSeconds: opts.replayWindowSeconds ?? REPLAY_WINDOW_DEFAULT_SECONDS,
  });
  if (!result.ok) {
    return fail(HTTP_UNAUTHORIZED, result.error.code, result.error.message);
  }
  return { ok: true, secretIndex: result.value.secretIndex };
}

function resolveSecrets(s: StandaloneVerifyOptions["secret"]): string[] {
  const raw = typeof s === "function" ? s() : s;
  return Array.isArray(raw) ? raw : [raw];
}

function fail(status: number, code: string, message: string): StandaloneVerifyFailure {
  return { ok: false, status, code, message };
}

// Marker so consumers know `HTTP_OK` is intentionally unused (keeping it as
// reference for future expansion of the success surface).
void HTTP_OK;
