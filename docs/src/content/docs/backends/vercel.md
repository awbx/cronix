---
title: Vercel Cron
description: Reconcile cronix manifests into the `crons[]` array of vercel.json so Vercel fires triggers itself at the configured times.
---

`cronix apply --backend vercel` syncs cronix-owned cron jobs into the `crons[]` array of your `vercel.json`. Vercel reads that file at deploy time and fires the configured paths on schedule from its own infrastructure — no `cronix trigger` shim is involved.

## Status

Stable as of v0.9.0. The backend is purely declarative: it reads `vercel.json` from disk, rewrites the cronix-owned entries, and writes the file back. Commit + redeploy as usual; Vercel picks up the changes.

## When to choose Vercel Cron

- Your app is deployed on Vercel.
- You want Vercel's scheduler to fire your scheduled HTTP routes — no separate worker, no cron host, no CLI on the runtime.
- You're happy with Vercel's [native cron auth](https://vercel.com/docs/cron-jobs/manage-cron-jobs#securing-cron-jobs) (the `Authorization: Bearer ${CRON_SECRET}` envelope) instead of cronix's HMAC scheme.

## Setup

```bash
cronix apply \
  --manifest https://your-app.vercel.app/.well-known/cron-manifest \
  --backend vercel \
  --vercel-json-path ./vercel.json \
  --secret-ref env:CRON_SECRET
```

| Flag | Default | Purpose |
|---|---|---|
| `--vercel-json-path` | `./vercel.json` | Path to your `vercel.json`. |
| `--vercel-trigger-prefix` | `/api/v1/scheduled/` | Prefix that identifies cronix-owned entries inside `crons[]`. Override only when you've changed the SDK's default trigger mount path. |

## What it writes

Each schedule of each job becomes one entry:

```json
{
  "crons": [
    { "path": "/api/v1/scheduled/reconcile-payments", "schedule": "*/15 * * * *" },
    { "path": "/api/v1/scheduled/send-invoices", "schedule": "0 * * * *" }
  ]
}
```

The backend sorts entries by `(path, schedule)` so identical input produces byte-identical `vercel.json` — `cronix apply` with no manifest changes is a complete no-op (D-027).

## What it preserves

`vercel.json` typically holds more than just crons. The backend round-trips every other top-level key (`buildCommand`, `framework`, `regions`, `headers`, `rewrites`, etc.) and any non-cronix entries inside `crons[]` (e.g. a hand-written `/api/cleanup` job). Only entries whose `path` starts with the trigger prefix are managed.

## Schedule constraints

- **5-field POSIX cron only.** `@hourly`, `@daily`, `@every 30s` are rejected — Vercel doesn't support them. `cronix validate` flags this before apply.
- **UTC timezone only.** Set `policy.timezone: "UTC"` (or omit it) — any other timezone is rejected.

## Authentication

Vercel fires the trigger directly. There's no `cronix trigger` shim signing requests, so cronix HMAC isn't applied to incoming triggers from Vercel. Two patterns work:

### 1. Vercel's native auth + cronix `skipVerify`

Use Vercel's built-in `CRON_SECRET` env var ([docs](https://vercel.com/docs/cron-jobs/manage-cron-jobs#securing-cron-jobs)). Vercel adds `Authorization: Bearer ${CRON_SECRET}` to every request; your route checks it:

```ts
import { createCron } from "@awbx/cronix-sdk";

const cron = createCron({
  app: "billing-service",
  baseUrl: "https://billing.vercel.app",
  secret: "ignored",
  skipVerify: true, // Vercel cron has no cronix HMAC; auth is the Authorization header
});

app.post("/api/v1/scheduled/:name", async (c) => {
  const got = c.req.header("authorization");
  if (got !== `Bearer ${process.env.CRON_SECRET}`) {
    return c.text("unauthorized", 401);
  }
  return cron.handle(c.req.raw);
});
```

The [`skipVerify`](/cronix/sdk/extension-points/#skipverify) extension point exists for this case. `ctx.unverified === true` inside the handler so high-stakes routes can refuse to mutate state when the surrounding auth is missing.

### 2. Mix Vercel + non-Vercel triggers on the same app

If the same app receives Vercel-fired triggers AND `cronix trigger` shim-signed triggers (e.g. you run cronix on a Linux host alongside the Vercel deploy), keep cronix HMAC on by default and add a Vercel-Authorization fallback inside the route, accepting either path.

## Drift detection

`cronix drift --backend vercel` lists cronix-owned entries in `vercel.json` and compares each against the manifest. A schedule edited by hand surfaces the same way it does on the other backends — including via `cronix global-status`.

## Limitations

- **No `cronix history`.** Vercel's run records live in the Vercel dashboard / `vercel logs`. The backend's `History` returns an empty slice with no error so `cronix history` reports gracefully rather than failing.
- **Apply is local.** `cronix apply --backend vercel` rewrites the file on the operator's host; the operator (or CI) commits + deploys. Putting `cronix apply` itself in CI is fine — point it at `--vercel-json-path $GITHUB_WORKSPACE/vercel.json` and let the same workflow that deploys also apply.
- **No timezone support.** Vercel runs all crons in UTC. Use UTC-relative schedules.
