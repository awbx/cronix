---
title: cronix list
description: List every cronix-owned entry currently installed in the configured backend.
---

`list` queries the configured backend and prints every entry it reports as cronix-owned — one row per `(app, job, schedule index)` triple. It is read-only and stateless: each invocation is a fresh `Backend.List()` call, with no manifest involved.

For the same view across multiple backends in one shot — host crontab, in-cluster Kubernetes, AWS EventBridge — use [`global-status`](/cronix/cli/global-status/) instead. `list` targets exactly one backend, configured via flags.

## Synopsis

```
cronix list [flags]
```

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `-o, --output` | `table` | `table` or `json` |

Plus all [backend flags](/cronix/cli/backend-flags/) — `--backend`, `--crontab-path`, `--trigger-bin`, `--systemd-unit-dir`, `--k8s-namespace`, etc.

## Examples

List entries in a local crontab:

```bash
cronix list --crontab-path /tmp/cronix.crontab
# APP              JOB                 IDX  HASH
# billing-service  reconcile-payments  0    eefe2dd0dcf563e2
# billing-service  send-invoices       0    0afcd05672500c2a
```

List entries in a Kubernetes namespace:

```bash
cronix list --backend kubernetes --in-cluster --k8s-namespace billing
# APP              JOB              IDX  HASH
# billing-service  nightly-rollup   0    a1b2c3d4e5f60718
```

JSON output for piping:

```bash
cronix list --crontab-path /tmp/cronix.crontab -o json
# {
#   "backend": "crontab",
#   "entries": [
#     { "app": "billing-service", "job": "reconcile-payments", "index": 0, "hash": "eefe2dd0dcf563e2" }
#   ]
# }
```

## Notes

- **Hash column is the per-schedule fingerprint.** It's the same FNV-1a-over-canonicalized-spec value that drives reconcile and drift detection. Matching hashes across `list` and a manifest mean the entry is in sync.
- **`IDX` is the schedule index** for jobs with multiple `schedules`. A job with three cron expressions appears as three rows, indexed `0`, `1`, `2`.
- **Empty output is normal.** A backend with no cronix-owned entries returns zero rows and exits `0`. To check ownership of a single job specifically, use [`cronix show`](/cronix/cli/show/).
- **Need to compare against a manifest?** Use [`drift`](/cronix/cli/drift/) for the full reconcile-style diff, or [`show <app>.<job> --manifest`](/cronix/cli/show/) for one job.
