---
title: Extension points
description: Opt-in TypeScript SDK affordances — skip-verify, hooks, custom error responses, pluggable logger, standalone verify utilities, and per-job overrides — that don't change the on-the-wire protocol.
---

The four operations on a `cron` instance — `register`, `manifest`, `verify`,
`run` — are the v1 conformance contract. Beyond those, `@awbx/cronix-sdk`
exposes a handful of opt-in affordances for cases where the defaults don't
quite fit. Turning any of them on or off does **not** change what bytes flow
between the reconciler, trigger shim, and your app.

These are language-idiomatic, non-load-bearing, and deliberately outside
the [conformance vectors](/cronix/concepts/manifest/#conformance) — see
[RFC §SDK Contract / Extension points](https://github.com/awbx/cronix/blob/main/spec/RFC.md#extension-points-non-normative)
and decisions D-030 through D-035 for the spec view.

## Standalone verify utilities

Use when you want the verdict without registering a cron instance: custom
middleware, polyglot routing, test fixtures, a service where another framework
owns `/api/v1/scheduled/*` and only the auth check needs to live in your code.

```ts
import {
  verifyTriggerRequest,
  verifyManifestRequest,
  signRequest,
  parseSignatureHeader,
  canonicalSignedString,
  constantTimeEqual,
} from "@awbx/cronix-sdk";

// Drop-in for any custom route — accepts a Web Request, never throws.
const r = await verifyTriggerRequest(req, {
  secret: process.env.CRON_SECRET!,
  // Optional: override the replay window (default 300, must be >= 30 seconds).
  // replayWindowSeconds: 60,
});
if (!r.ok) return new Response(r.message, { status: r.status });

// Hand-roll the dispatch yourself.
console.log(`fire ${r.name}, body=${new TextDecoder().decode(r.body)}`);
return new Response("ok");
```

| Function | Returns | Use it for |
|---|---|---|
| `verifyTriggerRequest(req, opts)` | `{ok, name, headers, body, secretIndex}` or `{ok:false, status, code, message}` | Verify a signed trigger without registering jobs. |
| `verifyManifestRequest(req, opts)` | `{ok, secretIndex}` or `{ok:false, status, code, message}` | Verify a signed manifest fetch from inside a custom route. |
| `signRequest({method, path, body, secret, timestamp?})` | `{header, timestamp}` | Mint a signature header for tests, custom triggers, or replays. |
| `parseSignatureHeader(value)` | `Result<{ts, sigHex}, VerifyError>` | Parse `t=…,v1=…` into its parts; useful for audit logging. |
| `canonicalSignedString(ts, method, path, body)` | `Uint8Array` | Build the canonical `t.METHOD.path.body` byte string yourself. |
| `constantTimeEqual(a, b)` | `boolean` | Constant-time `Uint8Array` comparison helper. |

## skipVerify

:::caution[Footgun]
`skipVerify: true` removes cronix's authentication. Trust must come from
elsewhere — mTLS, an internal Kubernetes service, an IP allowlist, or a
dev environment. Never expose a `skipVerify` route to the public internet.
:::

```ts
const cron = createCron({
  app: "billing-service",
  baseUrl: "...",
  secret: "ignored when skipVerify=true",
  skipVerify: true, // every incoming trigger and manifest fetch is accepted
});
```

When `skipVerify` is true:

- `cron.handle` / `cron.verifyTrigger` / `cron.verifyManifest` accept any well-formed request.
- Outgoing requests from `cronix trigger` are still signed — the wire format is unchanged.
- The SDK emits a single `logger.warn` line at instance construction so the choice shows up in your boot log.
- Each handler invocation receives `ctx.unverified === true` so handlers can branch on it (e.g. refuse to mutate billing state from an unverified request even if the surrounding network is trusted).

### Per-job override

A common pattern is "skip verify on the dev/health job, keep it on the production billing job":

```ts
const cron = createCron({
  app: "billing-service",
  baseUrl: "...",
  secret: process.env.CRON_SECRET!, // global default — verification ON
});

cron.register({
  name: "health-ping",
  schedule: "@hourly",
  skipVerify: true, // this job only
  handler: async (ctx) => {
    if (ctx.unverified) console.log("health-ping arrived unverified");
    return { ok: true };
  },
});

cron.register({
  name: "reconcile-payments",
  schedule: "*/15 * * * *",
  // No skipVerify here → falls back to the instance default (verified).
  handler: reconcilePayments,
});
```

## Hooks

Five fire-and-forget hooks for observability. Errors thrown inside any hook
are caught by the SDK and routed to `logger.error`; they **cannot** break the
request, short-circuit the verify result, or cancel the handler.

```ts
const cron = createCron({
  app: "billing-service",
  baseUrl: "...",
  secret: process.env.CRON_SECRET!,
  hooks: {
    onVerifyFailure:  (failure, req)         => audit.failedVerify(failure),
    onTriggerStart:   (ctx)                   => log.info("fire", { run: ctx.runId }),
    onTriggerSuccess: (ctx, result, ms)       => metrics.timing("cron.ok", ms),
    onTriggerError:   (ctx, errOrResult, ms)  => sentry.captureException(errOrResult),
    onManifestRequest:(req)                   => audit.log("manifest pulled"),
  },
});
```

| Hook | Fires when | Signature |
|---|---|---|
| `onVerifyFailure` | A verify call returns `ok: false` (any reason — bad sig, bad method, bad path, unknown job). | `(failure, req) => void` |
| `onTriggerStart` | Just before the handler runs for a verified trigger. | `(ctx) => void` |
| `onTriggerSuccess` | Handler returned `ok: true`. | `(ctx, result, ms) => void` |
| `onTriggerError` | Handler returned `ok: false` **or** threw. | `(ctx, errOrResult, ms) => void` |
| `onManifestRequest` | Manifest fetch verified successfully. | `(req) => void` |

Async hooks are awaited (so you can `await` a logger flush) but their errors
are still swallowed.

## Custom error response

The default `r.toResponse()` builds a plain JSON `{code, message}` body with
the appropriate status code. Apps with strict error formats can override:

```ts
const cron = createCron({
  app: "billing-service",
  baseUrl: "...",
  secret: process.env.CRON_SECRET!,
  errorResponse: (failure) =>
    Response.json(
      { ok: false, error: { code: failure.code, message: failure.message } },
      {
        status: failure.status,
        headers: { "x-error-code": failure.code },
      },
    ),
});
```

The override receives the structured `VerifyFailure` (without `toResponse`)
and must return a Web `Response`. It is used everywhere `r.toResponse()`
fires inside `cron.handle` and the per-instance verify methods.

## Pluggable logger

The SDK uses `console` by default for boot warnings (e.g. the `skipVerify`
warning) and hook errors. Pass any object with `info` / `warn` / `error`
(`debug` is optional) to route those through your stack:

```ts
import pino from "pino";

const cron = createCron({
  app: "billing-service",
  baseUrl: "...",
  secret: process.env.CRON_SECRET!,
  logger: pino({ name: "cronix" }),
});
```

The logger is for SDK-internal events. Application-level observability
should go through the [hooks](#hooks) — they get the structured context.

## Replay window override

The HMAC verifier rejects timestamps outside a ±5-minute window by default
(see [authentication](/cronix/concepts/auth/#replay-window)). Tighten it
when freshness matters more than tolerance:

```ts
const cron = createCron({
  app: "billing-service",
  baseUrl: "...",
  secret: process.env.CRON_SECRET!,
  replayWindowSeconds: 60, // tighter than the default 300
});
```

The minimum is **30 seconds**. The SDK throws at instance construction if
you go below — at smaller windows, common clock skew between an NTP-synced
host and a cloud VM rejects legitimate requests. Per-call overrides via
`maxSkewSeconds` on the `VerifyRequest` shape still work and supersede the
instance default for that call.

## Per-job overrides

The first per-job override is `skipVerify` (covered above). Other per-job
overrides — `secret`, `replayWindowSeconds`, `enabled`, `tags` — are
documented future direction in the RFC and will land in a follow-up SDK
release. `tags` and `enabled` will arrive together with an additive
manifest-schema field plus a `cronix apply --tag` flag on the reconciler.
