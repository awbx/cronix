---
title: Secrets & rotation
description: Providing HMAC secrets and rotating them with zero downtime.
---

cronix never stores secret bytes inside the manifest, the spec files, or any backend artifact. The manifest only carries opaque references — the operator-side configuration resolves them at apply time and at fire time. This page covers the reference formats, multi-secret rotation, operator configuration, and the redaction guarantees.

The threat model these mechanisms address is laid out in [Authentication](/cronix/concepts/auth/).

## Secret reference formats

Every secret is named by a `<scheme>:<value>` string. Three schemes ship in v1:

| Scheme | Example | Resolves to |
|---|---|---|
| `env:` | `env:CRON_SECRET_V2` | The value of the named environment variable. |
| `file:` | `file:/run/secrets/cron-v2` | The contents of the file at the given absolute path, with leading/trailing whitespace trimmed. |
| `raw:` | `raw:dev-only-literal-secret` | The literal value after the colon. **Development only** — shows up in process listings, config dumps, and shell history. |

Empty resolutions (e.g. an unset env var) are skipped with a warning, not a fatal error. An empty resulting list, after all references are resolved, is fatal — the trigger shim exits with `ExitInternal` (3) before signing.

Reference strings must match `^[a-zA-Z][a-zA-Z0-9_:./-]{0,127}$`. The colon-after-scheme is part of the value; subsequent colons (e.g. in a `file:` path) are allowed.

## Multiple secret refs per job

Each job's `auth.secret_refs` is an ordered array of 1..8 references. The verifier accepts a signature produced by **any** of them and reports the index of the first match.

```json
{
  "auth": {
    "secret_refs": ["env:BILLING_CRON_V2", "env:BILLING_CRON_V1"]
  }
}
```

The signer (the trigger shim) uses the first resolved secret. The verifier (the app's SDK) accepts either. That asymmetry is what makes zero-downtime rotation possible — see below.

## Rotation flow

Rotating a secret never requires a synchronous coordinated cutover. The pattern is **add → wait → remove**:

| Step | Manifest change | Operator config | Result |
|---|---|---|---|
| 1. Add new secret | `secret_refs: ["NEW_VAR", "OLD_VAR"]` | Both `NEW_VAR` and `OLD_VAR` exported to the trigger host and to the app | Signers still sign with `NEW_VAR` (the head of the list); verifiers accept either. |
| 2. Wait one deploy cycle | (no change) | (no change) | All in-flight signers and verifiers have picked up `NEW_VAR`. Old signatures stop appearing. |
| 3. Remove old secret | `secret_refs: ["NEW_VAR"]` | Drop `OLD_VAR` from the operator config | Old secret bytes can be revoked at the secret store. |

If you skip step 2 — drop `OLD_VAR` while the previous deploy is still in flight — fires signed with the old secret will fail with `SignatureMismatch` until the deploy completes.

### Worked example

`auth-rotation-cycle.cronix.json`:

```json
{
  "version": 1,
  "app": "billing-service",
  "jobs": [
    {
      "name": "settle-invoices",
      "schedule": "0 2 * * *",
      "request": {
        "url": "https://billing.internal/api/v1/scheduled/settle-invoices"
      },
      "auth": {
        "secret_refs": ["env:BILLING_CRON_V2", "env:BILLING_CRON_V1"]
      }
    }
  ]
}
```

On the operator host:

```bash
export BILLING_CRON_V2="whsec_new_..."
export BILLING_CRON_V1="whsec_old_..."

cronix apply --manifest ./auth-rotation-cycle.cronix.json \
  --backend crontab --crontab-path /etc/crontab \
  --trigger-bin /usr/local/bin/cronix \
  --secret-ref env:BILLING_CRON_V2 --secret-ref env:BILLING_CRON_V1
```

In the app environment:

```bash
export BILLING_CRON_V2="whsec_new_..."
export BILLING_CRON_V1="whsec_old_..."
```

Once both sides have picked up V2 (one deploy cycle later), drop V1 from the manifest and from both environments. The verifier now only accepts V2; the secret store can rotate V1's underlying bytes.

## Operator-side resolution

`--secret-ref` is repeatable on every CLI command that talks to a backend or a signed manifest URL: [`apply`](/cronix/quickstart/), `plan`, `drift`, `list`, `show`, `prune`, `history`, [`global-status`](/cronix/cli/global-status/).

```bash
cronix apply \
  --manifest https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET_V2 --secret-ref env:CRON_SECRET_V1 \
  --backend crontab --crontab-path /etc/crontab \
  --trigger-bin /usr/local/bin/cronix
```

Operator-side secrets serve two purposes:

1. **Signing the manifest GET.** When the manifest source is `https://`, the first resolved secret signs the GET. Local file manifests skip this.
2. **Forwarding into per-job spec files.** `apply` writes one `<app>.<job>.json` into `--spec-dir` (default `/etc/cronix/jobs`). Each spec carries the `secret_refs` list verbatim — the trigger shim resolves them at fire time, never at apply time.

Spec files contain references, not bytes. If you `cat /etc/cronix/jobs/billing-service.settle-invoices.json` you'll see `"secret_refs": ["env:BILLING_CRON_V2", "env:BILLING_CRON_V1"]` — the bytes only ever live in env vars, files, or your secret store.

## Persistent operator config

`cronix.yaml` (loaded from `--config`, `$CRONIX_CONFIG`, `~/.cronix/cronix.yaml`, or `/etc/cronix/cronix.yaml`) can carry the same `--secret-ref` list under a top-level `secret_refs:` key, so you don't pass them on every invocation:

```yaml
version: 1
secret_refs:
  - env:CRON_SECRET_V2
  - env:CRON_SECRET_V1
```

The schema is loaded with strict unknown-field rejection — typos fail loudly rather than silently being ignored.

## Logging and redaction

Secrets are **never logged**. The contract applies everywhere a signed payload component might appear:

- **Trigger shim logs** — slog-JSON to stdout — never include secret bytes, the `Authorization` header, or the `X-Cron-Signature` header value.
- **Manifest-fetch logs** redact the GET signature similarly.
- **App-side handler logs** are the app's responsibility; the SDK's `JobContext` never exposes the secret to user code in the first place.

Apps and SDKs MUST `redact()` before emitting any line that includes any signed-payload component. CI greps for raw HMAC values in log strings and fails on a hit.

## When things go wrong

| Symptom | Cause | Fix |
|---|---|---|
| `SignatureMismatch` immediately after rotation | Operator dropped the old secret before the new one finished rolling out | Re-add the old secret as the second `secret_refs` entry; wait one deploy cycle. |
| `trigger: no resolvable secrets` | All `secret_refs` resolved to empty (env var unset, file missing) | Check the operator host's environment; the spec file at `/etc/cronix/jobs/<app>.<job>.json` shows the references the shim is trying to resolve. |
| `MissingSignature` on the manifest GET | App's manifest endpoint doesn't accept signed requests, or the wrong secret is configured on the operator side | Verify `cron.handle()` is mounted at `/.well-known/cron-manifest`; check the operator-side `--secret-ref` matches the app's `CRON_SECRET`. |
| `StaleTimestamp` | Clock drift between operator and app exceeds 300s | Check NTP on both sides; see [Authentication](/cronix/concepts/auth/) for the replay window. |
