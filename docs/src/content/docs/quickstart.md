---
title: Quick start
description: Wire the SDK into a Hono app, then reconcile from your laptop.
---

End-to-end in five minutes: declare a job in your Hono app, serve the manifest, then reconcile it into the host scheduler.

## 1. Install the SDK and CLI

```bash
pnpm add @awbx/cronix-sdk
brew install awbx/cronix/cronix    # or curl install.sh — see Install
```

## 2. Declare a job in your app

```ts title="src/index.ts"
import { createCron, MANIFEST_PATH, TRIGGER_PATH_PREFIX } from "@awbx/cronix-sdk";
import { Hono } from "hono";

const cron = createCron({
  app: "billing-service",
  baseUrl: "https://billing.example.com",
  secret: process.env.CRON_SECRET!,
});

cron.register({
  name: "reconcile-payments",
  schedule: "*/15 * * * *",
  auth: { secret_refs: ["env:CRON_SECRET"] },
  handler: async (ctx) => {
    console.log(`fired ${ctx.name} run=${ctx.runId}`);
    return { ok: true };
  },
});

const app = new Hono();
app.all(MANIFEST_PATH, (c) => cron.handle(c.req.raw));
app.all(`${TRIGGER_PATH_PREFIX}:name`, (c) => cron.handle(c.req.raw));

export default app;
```

The app is now serving a signed manifest at `/.well-known/cron-manifest` and a signed trigger endpoint at `/api/v1/scheduled/reconcile-payments`.

## 3. Reconcile from your laptop

```bash
cronix apply \
  --manifest https://billing.example.com/.well-known/cron-manifest \
  --backend crontab \
  --crontab-path /etc/crontab \
  --trigger-bin /usr/local/bin/cronix \
  --secret-ref env:CRON_SECRET
```

The crontab now contains:

```cron
*/15 * * * * /usr/local/bin/cronix trigger billing-service.reconcile-payments
# cronix:owned app=billing-service job=reconcile-payments hash=eefe2dd0dcf563e2 idx=0
```

Every 15 minutes, `cron(8)` invokes `cronix trigger`, which:

1. Acquires the concurrency lock (Forbid/Allow/Replace).
2. Computes the HMAC signature.
3. POSTs to `https://billing.example.com/api/v1/scheduled/reconcile-payments`.
4. Retries on 5xx/network errors with exponential backoff.

Your handler verifies the signature (via `cron.handle()`) and runs.

## 4. Inspect what's installed

```bash
cronix list --backend crontab --crontab-path /etc/crontab
# APP              JOB                  IDX  HASH
# billing-service  reconcile-payments   0    eefe2dd0dcf563e2
```

## 5. Detect drift

If anyone hand-edits the crontab line:

```bash
cronix drift --manifest ... --backend crontab --crontab-path /etc/crontab --exit-on-drift
# Plan: backend=crontab noop=false ops=1
#   ~ update billing-service.reconcile-payments  (eefe2dd0dcf563e2 → fa1c2c88...)
# drift detected
# exit=5
```

`cronix apply` again brings it back to the manifest's intent.

## What next?

- Try a different backend: [systemd-timer](/cronix/backends/systemd/), [Kubernetes](/cronix/backends/kubernetes/), [AWS EventBridge Scheduler](/cronix/backends/aws/).
- Read the runnable examples: [hono](https://github.com/awbx/cronix/tree/main/ts/examples/hono-app), [express](https://github.com/awbx/cronix/tree/main/ts/examples/express-app), [fastify](https://github.com/awbx/cronix/tree/main/ts/examples/fastify-app), [hand-rolled](https://github.com/awbx/cronix/tree/main/ts/examples/hand-rolled), [go](https://github.com/awbx/cronix/tree/main/go/examples/go-app).
- Read the [RFC](https://github.com/awbx/cronix/blob/main/spec/RFC.md) — it's the source of truth for the protocol.
