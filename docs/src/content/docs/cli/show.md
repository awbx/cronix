---
title: cronix show
description: Inspect one cronix-managed job — installed schedules, hashes, and (optionally) drift against the manifest.
---

`show` zooms in on a single `(app, job)` pair. It prints the backend's current state — schedules, per-index hashes — and, when `--manifest` is also passed, the desired spec from the manifest alongside an in-sync / drifted indicator computed from the same hash the reconciler uses.

It is the read-only inspection complement to [`drift`](/cronix/cli/drift/): `drift` reports across the whole manifest, `show` zooms in on one job. Useful for "is this one cron actually deployed and healthy?" troubleshooting where the broader [`list`](/cronix/cli/list/) output is too noisy.

## Synopsis

```
cronix show <app>.<job> [flags]
```

The positional argument must be a single string of the form `<app>.<job>` — e.g. `billing.reconcile-payments`.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--manifest` | (none) | Manifest source. When set, the desired spec is shown and drift is reported |
| `--secret-ref` (repeatable) | (none) | Required for `https://` manifest sources |
| `-o, --output` | `table` | `table` or `json` |

Plus all [backend flags](/cronix/cli/backend-flags/).

## Examples

Inspect what's installed without comparing to a manifest:

```bash
cronix show billing.reconcile-payments \
  --crontab-path /tmp/cronix.crontab
# BACKEND    crontab
# JOB        billing.reconcile-payments
#
# IDX  HASH
# 0    eefe2dd0dcf563e2
```

Compare against a manifest — drift indicator and desired spec appear:

```bash
cronix show billing.reconcile-payments \
  --crontab-path /tmp/cronix.crontab \
  --manifest ./billing.cronix.json
# BACKEND    crontab
# JOB        billing.reconcile-payments
# DRIFT      in-sync
# TIMEZONE   UTC
# URL        POST https://billing.example.com/cron/reconcile-payments
# SCHEDULES  */15 * * * *
#
# IDX  HASH
# 0    eefe2dd0dcf563e2
```

When the job is not installed:

```bash
cronix show billing.never-deployed --crontab-path /tmp/cronix.crontab
# BACKEND  crontab
# JOB      billing.never-deployed
# STATUS   not installed
```

## Notes

- **Argument format is strict.** It must be exactly `<app>.<job>` with one dot. Apps or jobs containing dots themselves are not supported in this argument; use `cronix list -o json` and grep instead.
- **The drift indicator is the same hash check `apply` uses.** "in-sync" means every schedule index present in the backend matches the desired hash byte-for-byte. Anything else — wrong count, wrong index, wrong hash — is "drifted".
- **`--manifest` mismatch is a hard error.** If the manifest's `app` does not match the requested app, or the job is missing from the manifest, `show` errors out rather than silently degrading.
- **For run-time logs** (last fire, exit status, retry counts), use [`cronix history`](/cronix/cli/history/).
