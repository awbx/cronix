import { err, ok, type Result } from "./result.js";

/**
 * HMAC-SHA256 signing and verification for cronix manifest fetches and
 * trigger requests. Stripe-shaped header (D-014..D-019).
 *
 * Web Crypto API only — no `node:crypto`. The TypeScript SDK runs unchanged
 * on Node 20+, Bun, Deno, Workers, and Edge.
 */

export const SIG_VERSION = "v1" as const;
export const REPLAY_WINDOW_DEFAULT_SECONDS = 300;

export type SignOptions = {
  secret: string;
  method: string;
  path: string;
  body: Uint8Array;
  timestamp?: number;
};

export type SignResult = {
  header: string;
  timestamp: number;
};

export type VerifyOptions = {
  secrets: readonly string[];
  method: string;
  path: string;
  body: Uint8Array;
  header: string;
  now?: number;
  maxSkewSeconds?: number;
};

export type VerifyError =
  | { code: "MalformedHeader"; message: string }
  | { code: "StaleTimestamp"; message: string; timestamp: number }
  | { code: "SignatureMismatch"; message: string };

export async function sign(opts: SignOptions): Promise<SignResult> {
  if (opts.secret.length === 0) {
    throw new Error("sign: secret must be non-empty");
  }
  const ts = Math.floor(opts.timestamp ?? Date.now() / 1000);
  const payload = canonicalSignedString(ts, opts.method, opts.path, opts.body);
  const hex = await hmacSha256Hex(opts.secret, payload);
  return { header: `t=${ts},${SIG_VERSION}=${hex}`, timestamp: ts };
}

export async function verify(opts: VerifyOptions): Promise<Result<{ secretIndex: number }, VerifyError>> {
  if (opts.secrets.length === 0) {
    return err({ code: "MalformedHeader", message: "verify: at least one secret is required" });
  }
  const parsed = parseHeader(opts.header);
  if (!parsed.ok) return parsed;
  const { ts, sigHex } = parsed.value;

  const now = Math.floor(opts.now ?? Date.now() / 1000);
  const maxSkew = opts.maxSkewSeconds ?? REPLAY_WINDOW_DEFAULT_SECONDS;
  if (Math.abs(now - ts) > maxSkew) {
    return err({
      code: "StaleTimestamp",
      message: `timestamp ${ts} outside skew window ${maxSkew}s (now=${now})`,
      timestamp: ts,
    });
  }
  const expectedSig = hexToBytes(sigHex);
  if (!expectedSig) {
    return err({ code: "MalformedHeader", message: "signature is not valid hex" });
  }

  const payload = canonicalSignedString(ts, opts.method, opts.path, opts.body);
  for (let i = 0; i < opts.secrets.length; i++) {
    const secret = opts.secrets[i];
    if (secret === undefined || secret.length === 0) continue;
    const computed = await hmacSha256Bytes(secret, payload);
    if (constantTimeEqual(computed, expectedSig)) {
      return ok({ secretIndex: i });
    }
  }
  return err({ code: "SignatureMismatch", message: "no acceptable secret produced a matching signature" });
}

export function canonicalSignedString(ts: number, method: string, path: string, body: Uint8Array): Uint8Array {
  const tsStr = String(ts);
  const m = method.toUpperCase();
  const prefix = `${tsStr}.${m}.${path}.`;
  const prefixBytes = new TextEncoder().encode(prefix);
  const out = new Uint8Array(prefixBytes.length + body.length);
  out.set(prefixBytes, 0);
  out.set(body, prefixBytes.length);
  return out;
}

function parseHeader(header: string): Result<{ ts: number; sigHex: string }, VerifyError> {
  if (typeof header !== "string" || header.length === 0) {
    return err({ code: "MalformedHeader", message: "header is empty" });
  }
  const parts = header.split(",");
  let ts: number | undefined;
  let sigHex: string | undefined;
  for (const part of parts) {
    const eq = part.indexOf("=");
    if (eq < 0) return err({ code: "MalformedHeader", message: `malformed segment: ${part}` });
    const k = part.slice(0, eq);
    const v = part.slice(eq + 1);
    if (k === "t") {
      if (!/^[0-9]+$/.test(v)) {
        return err({ code: "MalformedHeader", message: `t must be a non-negative integer: ${v}` });
      }
      ts = Number(v);
      if (!Number.isSafeInteger(ts)) {
        return err({ code: "MalformedHeader", message: `t out of safe integer range: ${v}` });
      }
    } else if (k === SIG_VERSION) {
      if (!/^[0-9a-fA-F]+$/.test(v) || v.length !== 64) {
        return err({ code: "MalformedHeader", message: `${SIG_VERSION} must be 64 hex chars` });
      }
      sigHex = v.toLowerCase();
    }
    // Unknown segments are ignored (forward-compat).
  }
  if (ts === undefined) return err({ code: "MalformedHeader", message: "missing `t=`" });
  if (sigHex === undefined) return err({ code: "MalformedHeader", message: `missing \`${SIG_VERSION}=\`` });
  return ok({ ts, sigHex });
}

async function hmacSha256Bytes(secret: string, payload: Uint8Array): Promise<Uint8Array> {
  const subtle = (globalThis as { crypto?: { subtle?: SubtleCrypto } }).crypto?.subtle;
  if (!subtle) throw new Error("Web Crypto subtle is not available in this runtime");
  const keyBytes = new TextEncoder().encode(secret);
  const key = await subtle.importKey("raw", keyBytes as BufferSource, { name: "HMAC", hash: "SHA-256" }, false, [
    "sign",
  ]);
  const sig = await subtle.sign("HMAC", key, payload as BufferSource);
  return new Uint8Array(sig);
}

async function hmacSha256Hex(secret: string, payload: Uint8Array): Promise<string> {
  const bytes = await hmacSha256Bytes(secret, payload);
  return bytesToHex(bytes);
}

function bytesToHex(b: Uint8Array): string {
  const hex = new Array<string>(b.length);
  for (let i = 0; i < b.length; i++) {
    hex[i] = (b[i] as number).toString(16).padStart(2, "0");
  }
  return hex.join("");
}

function hexToBytes(hex: string): Uint8Array | null {
  if (hex.length % 2 !== 0) return null;
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < hex.length; i += 2) {
    const byte = Number.parseInt(hex.slice(i, i + 2), 16);
    if (Number.isNaN(byte)) return null;
    out[i / 2] = byte;
  }
  return out;
}

/**
 * Constant-time byte comparison. Manual XOR loop — does not depend on
 * `crypto.timingSafeEqual` being available in the runtime.
 */
export function constantTimeEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) {
    diff |= (a[i] as number) ^ (b[i] as number);
  }
  return diff === 0;
}
