---
title: cronix plan / diff
description: Show the operations apply would execute against the configured backend, without writing anything.
---

`plan` reads the manifest, computes the reconciliation against the configured backend, and prints what [`apply`](/cronix/cli/apply/) would do — without writing anything. It is exactly equivalent to `cronix apply --dry-run`, exposed as a top-level subcommand for ergonomics.

`diff` is an alias for `plan`. Use whichever name reads better in your scripts; both behave identically.

## Synopsis

```
cronix plan --manifest <source> [flags]
cronix diff --manifest <source> [flags]
```

## Flags

`plan` accepts the exact same flag set as [`apply`](/cronix/cli/apply/). `--dry-run` is forced on internally, so passing it has no effect.

| Flag | Default | Purpose |
|---|---|---|
| `--manifest` | (required) | Manifest source — `./path`, `/abs`, `file://`, `https://`, or `http://localhost` |
| `--secret-ref` (repeatable) | (none) | Required for `https://` sources |
| `--spec-dir` | `/etc/cronix/jobs` | Reported in JSON output but not written to (read-only command) |
| `-o, --output` | `table` | `table` or `json` |

Plus all [backend flags](/cronix/cli/backend-flags/).

## Examples

A clean plan against an empty crontab:

```bash
cronix plan --manifest ./billing.cronix.json \
  --crontab-path /tmp/cronix.crontab
# Plan: backend=crontab noop=false ops=2
#   + create  billing.reconcile-payments  ((none) → eefe2dd0dcf563e2)
#   + create  billing.send-invoices       ((none) → 0afcd05672500c2a)
```

A noop plan after an `apply`:

```bash
cronix plan --manifest ./billing.cronix.json \
  --crontab-path /tmp/cronix.crontab
# Plan: backend=crontab noop=true ops=0
```

JSON output for piping into a CI gate:

```bash
cronix diff --manifest ./billing.cronix.json -o json
# {
#   "backend": "crontab",
#   "ops": [
#     { "action": "update", "app": "billing", "job": "reconcile-payments",
#       "old_hash": "eefe2dd0dcf563e2", "new_hash": "a1b2c3d4e5f60718" }
#   ],
#   "noop": false
# }
```

## Notes

- **Read-only.** Never writes to the backend, never writes spec files, never calls `daemon-reload`. Safe to run on production from a read-only checkout.
- **Same exit semantics as `apply`.** A failed manifest fetch or backend `List` returns non-zero. A noop plan returns zero. To exit non-zero specifically when the plan is non-empty, use [`cronix drift --exit-on-drift`](/cronix/cli/drift/) instead.
- **Action markers in table output.** `+` = create, `~` = update, `-` = delete, `·` = skip (rare; in-sync entry traversed).
- **For ownership audits across multiple backends**, see [`global-status`](/cronix/cli/global-status/), which reads many backends in parallel without needing a manifest at all.
