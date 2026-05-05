---
title: Authentication
description: How cronix signs every manifest fetch and every trigger fire.
---

cronix authenticates **both** manifest fetches and trigger requests with the same HMAC-SHA256 scheme. One header carries a timestamp and one or more signature versions; the receiver recomputes the signature and compares in constant time. Stripe-shaped, well-understood, easy to audit.

There is no other authentication mechanism in v1. No mTLS, no bearer tokens, no OAuth — one HMAC for both directions, signed by [secrets the operator manages](/cronix/concepts/secrets/).

## Threat model

| Attacker | Capability | Defense |
|---|---|---|
| A1 | Passively observes traffic | HTTPS (cronix mandates `https://` for manifest URLs and trigger URLs). |
| A2 | Replays captured signed requests | Timestamp + replay window — see below. |
| A3 | Tampers with requests in flight (misissued cert, compromised intermediary) | HMAC over `<ts>.<METHOD>.<path>.<body>` — any change invalidates the signature. |
| A4 | Reads application logs | Secrets are never logged. SDKs and the trigger shim redact before emitting any line touching the signed payload. |
| A5 | Former operator whose access to *one* secret has been revoked | Multi-secret rotation lets the new secret roll out before the old one is removed — see [Secrets & rotation](/cronix/concepts/secrets/). |

cronix does **not** protect against a compromised app server, a stolen current secret, or side channels in the HMAC implementation other than timing-of-comparison (which is mitigated). Cryptographic primitives are stdlib only.

## Signed payload

The byte sequence input to HMAC is:

```
<unix_seconds>.<METHOD>.<PATH>.<BODY>
```

| Field | Notes |
|---|---|
| `<unix_seconds>` | Integer seconds since the Unix epoch, base-10, no leading zeros, no signs, no fractional part. |
| `<METHOD>` | The HTTP method uppercased (`POST`, `GET`, …). Both signer and verifier uppercase before hashing — mixed-case input round-trips. |
| `<PATH>` | The URL path-and-query as-sent. cronix does not normalize beyond the URL parsing layer. Both sides should agree on percent-encoding rules. |
| `<BODY>` | The request body verbatim. For methods conventionally without a body (e.g. `GET`), it is zero bytes. |

The three `.` characters are literal dots. They are unambiguous because the timestamp is all digits and the method is uppercase letters; no legal value of either field contains `.`.

## Header format

```
X-Cron-Signature: t=<unix_seconds>,v1=<lowercase_hex_sha256>
```

| Segment | Notes |
|---|---|
| `t=` | The timestamp from the canonicalization. MUST equal the timestamp the signer used; mutating it on the wire breaks the signature. |
| `v1=` | The lowercase hexadecimal HMAC-SHA256 of the canonical payload, exactly 64 characters. |

Comma-separated. Segment order is not significant. Unknown segments are ignored — that's the forward-compat hook for future algorithm versions (`v2=`, …) without changing the header name. At minimum, the verifier MUST find a `t=` segment and a `v1=` segment.

## Replay window

Verifiers reject signatures whose timestamp is more than `maxSkewSeconds` away from the current time, in either direction.

| Setting | Default | Notes |
|---|---|---|
| Replay window | `300` seconds | Tolerates ≤ 60s of uncorrected clock drift between sender and receiver in production. |
| Tightening | per-route | Operators may pass a smaller window for high-security endpoints. |

Receivers MUST use a monotonic-or-NTP-synced clock. cronix assumes NTP is in place; without it, signed requests will fail unpredictably as the local clock drifts past 5 minutes from real time.

## Constant-time comparison

Implementations MUST compare HMAC bytes in constant time. Loose `==` / `===` comparisons on signature bytes leak timing information that lets an attacker brute-force a signature byte at a time.

| Language | Primitive |
|---|---|
| Go | `crypto/subtle.ConstantTimeCompare` |
| TypeScript | A manual XOR loop over equal-length `Uint8Array`s. `crypto.timingSafeEqual` is not assumed to be available across runtimes (Bun, Workers). |

CI greps for loose comparison adjacent to HMAC values in both languages and fails on a hit.

## Verifying in your handler

You don't write the HMAC code yourself — the SDK does it. The TypeScript SDK exposes `cron.handle(request)` which:

1. Reads `X-Cron-Signature` from headers (case-insensitive). Missing → 401.
2. Resolves the configured secrets (string, array, or function returning either).
3. Parses the header; rejects malformed or stale-timestamp headers with 401.
4. Recomputes HMAC against each configured secret; rejects on mismatch with 401.
5. On the trigger success path, builds a `JobContext` from the `X-Cron-*` headers (run-id, attempt, fire times) and invokes the registered handler.

```ts
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";

const cron = createCron({
  app: "billing-service",
  baseUrl: "https://billing.example.com",
  secret: process.env.CRON_SECRET!,
});

// One handle() call for both endpoints — manifest and trigger.
app.all(MANIFEST_PATH, (c) => cron.handle(c.req.raw));
app.all(`${TRIGGER_PATH_PREFIX}:name`, (c) => cron.handle(c.req.raw));
```

The Go SDK at `go/pkg/cronsdk` exposes `Verify` and `VerifyHTTP` for the same purpose if your app is in Go.

## Worked example

Inputs:

| | |
|---|---|
| `secret` | `whsec_test_primary_aaaaaaaaaaaaaaaaaaaaaaaaaaa` |
| `method` | `POST` |
| `path` | `/api/v1/scheduled/reconcile-payments` |
| `body` | `{"runId":"abc","attempt":1}` |
| `timestamp` | `1730000002` |

Canonical payload (literal bytes):

```
1730000002.POST./api/v1/scheduled/reconcile-payments.{"runId":"abc","attempt":1}
```

The HMAC-SHA256 hex of that payload with the secret yields the `v1=` value. The full header is:

```
X-Cron-Signature: t=1730000002,v1=<64 lowercase hex chars>
```

The exact bytes are committed in `spec/auth-vectors.json`. Both the TypeScript and Go reference implementations produce the same header.

## Error codes

`verify()` returns a structured error rather than throwing:

```
{ ok: false, status: number, code: string, message: string }
```

| `code` | HTTP status | Meaning |
|---|---|---|
| `MissingSignature` | 401 | No `X-Cron-Signature` header. |
| `MalformedHeader` | 401 | Header present but doesn't parse — missing `t=`, missing `v1=`, wrong length, non-hex, etc. |
| `StaleTimestamp` | 401 | `\|now - t\| > maxSkewSeconds`. |
| `SignatureMismatch` | 401 | Header parses, timestamp is fresh, but no configured secret produces a matching HMAC. |
| `BadMethod` | 400 | Manifest endpoint hit with a non-GET method. |
| `BadPath` | 404 | Trigger endpoint path doesn't start with `/api/v1/scheduled/` or manifest endpoint path isn't `/.well-known/cron-manifest`. |
| `UnknownJob` | 404 | Trigger path's job name isn't registered. |

## Conformance

`spec/auth-vectors.json` is the authoritative correctness contract. The 35 vectors cover happy paths (empty body, GET with no body, JSON body, UTF-8 with emoji, 1 MiB body, embedded NUL bytes, percent-encoded paths, lowercase-method input, multi-secret rotation), malformed headers (empty, missing segments, wrong algorithm, wrong length, non-hex), replay window edges, and tampering (flipped signature byte, altered body / method / path, wrong secret, no-match-anywhere).

Adding a vector is a spec change. If your SDK passes `spec/auth-vectors.json`, it interoperates with cronix.
