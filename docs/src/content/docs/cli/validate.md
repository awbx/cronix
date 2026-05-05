---
title: cronix validate
description: Lint a manifest from a file path or signed URL — parse, validate, and normalize without side effects.
---

`validate` parses, validates, and normalizes a manifest, reporting any errors with the same path-and-message format used by the conformance vectors. It performs no backend writes, no spec-file emissions, no network calls beyond fetching the manifest itself when the source is HTTPS.

Use it as the first step in a CI pipeline that will later run [`apply`](/cronix/cli/apply/): catch schema and semantic errors before any backend reconciliation begins. It also accepts the same source forms `apply` does, so validation results match what reconciliation will see.

## Synopsis

```
cronix validate <source> [flags]
```

`<source>` is one of:

- `./relative/path.json` or `/absolute/path.json` — local file
- `file://path` — local file (URL form)
- `https://app/.well-known/cron-manifest` — signed HTTPS fetch (requires `--secret-ref`)
- `http://localhost/...` or `http://127.0.0.1/...` — dev only

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--secret-ref` (repeatable) | (none) | `env:NAME`, `file:/path`, or `raw:literal`. Required for `https://` sources |
| `-o, --output` | `table` | `table`, `json`, or `yaml` |

## Examples

A passing local manifest:

```bash
cronix validate ./billing.cronix.json
# OK  app=billing-service jobs=2
#   - reconcile-payments  schedules=[*/15 * * * *]  policy.timeout=60s  retries=3
#   - send-invoices       schedules=[0 9 * * MON]    policy.timeout=120s retries=3
```

A failing manifest — issues print with their JSON path:

```bash
cronix validate ./broken.cronix.json
# INVALID
#   jobs/0/schedules: at least one schedule is required (manifest/jobs/schedules/empty)
#   jobs/1/request/url: url must use https or http://localhost (manifest/jobs/request/url/scheme)
```

Validate a signed HTTPS manifest:

```bash
cronix validate https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET -o json
```

## Notes

- **Pure read-only.** No backend is constructed, no spec files are written, no schedule entries are touched. Safe to run from any context against any source.
- **Exit code reflects validity.** Returns zero on `OK`, non-zero on `INVALID`. The error stream carries the issue list; stdout stays clean for piping the report.
- **HTTPS sources need `--secret-ref`.** Same rules as [`apply`](/cronix/cli/apply/) — the first resolved secret signs the GET. Local sources need no secret-ref.
- **YAML output** is supported here even though `apply`/`plan` only emit `table` and `json`. Useful for review tooling that wants a stable serialized snapshot of the normalized manifest.
