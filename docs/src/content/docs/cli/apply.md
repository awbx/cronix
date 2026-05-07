---
title: cronix apply
description: Reconcile a manifest against the configured host scheduler — create, update, or delete entries to match the desired state.
---

`apply` reads a manifest, compares it to what the configured backend currently has installed, and executes the difference. It is the only mutating top-level command apart from [`prune`](/cronix/cli/prune/) — every other read-shaped command (`list`, `show`, `drift`, `history`, `global-status`) is side-effect-free.

`apply` is idempotent: a second `apply` against an unchanged manifest is a complete no-op (zero writes to the backend, zero spec-file rewrites). That makes it safe to run on every CI deploy. To preview without writing, use `--dry-run` or the [`plan`](/cronix/cli/plan/) alias.

## Synopsis

```
cronix apply [BACKEND] --manifest <source> [flags]
```

`BACKEND` is optional and selects one of `crontab`, `systemd-timer`, `kubernetes`, `aws-scheduler`, or `vercel` as a sub-subcommand. When omitted, `--backend` is used (legacy form). See [backend flags](/cronix/cli/backend-flags/) for the two equivalent shapes.

`<source>` is one of:

- `./relative/path.json` or `/absolute/path.json` — local file
- `file://path` — local file (URL form)
- `https://app/.well-known/cron-manifest` — signed HTTPS fetch (requires `--secret-ref`)
- `http://localhost/...` or `http://127.0.0.1/...` — dev only

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--manifest` | (required) | Manifest source — see above |
| `--secret-ref` (repeatable) | (none) | `env:NAME`, `file:/path`, or `raw:literal`. Required for `https://` sources; forwarded into per-job spec files for the trigger shim |
| `--spec-dir` | `/etc/cronix/jobs` | Where to write the per-job spec files the trigger shim reads at fire time. Ignored for `kubernetes` (specs live in a sibling ConfigMap) |
| `--dry-run` | `false` | Compute and print the Plan but do not execute it (equivalent to `cronix plan`) |
| `-o, --output` | `table` | Output format: `table` or `json` |

Plus the [backend flags](/cronix/cli/backend-flags/) for the chosen backend. The sub-subcommand form (`cronix apply kubernetes ...`) only exposes that backend's flags in `--help` and shell completion; the legacy form (`cronix apply --backend kubernetes ...`) exposes the union for backwards compatibility.

## Examples

Reconcile a local manifest into the system crontab:

```bash
cronix apply crontab \
  --manifest ./billing.cronix.json \
  --crontab-path /tmp/cronix.crontab
# Apply: backend=crontab created=2 updated=0 deleted=0 skipped=0
```

Re-run with no changes — note the noop result:

```bash
cronix apply crontab \
  --manifest ./billing.cronix.json \
  --crontab-path /tmp/cronix.crontab
# Apply: noop (nothing to change)
```

Fetch from HTTPS and reconcile into Kubernetes:

```bash
cronix apply kubernetes \
  --manifest https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET \
  --in-cluster --k8s-namespace billing \
  --output json
```

The legacy form is still accepted:

```bash
cronix apply --backend kubernetes \
  --manifest https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET \
  --in-cluster --k8s-namespace billing
```

## Notes

- **Idempotent.** Re-applying an unchanged manifest writes nothing — no rewritten crontab, no `daemon-reload`, no Kubernetes API calls beyond the read. Safe to run on every CI deploy.
- **HTTPS requires `--secret-ref`.** The first resolved secret signs the GET request. Local file sources (`./`, `/abs`, `file://`) work without a secret-ref.
- **Spec-dir cleanup is automatic.** Every job in the manifest gets a `<app>.<job>.json` written; orphan specs from removed jobs are deleted in the same step. The trigger shim can never fire a stale spec.
- **Non-zero on apply errors.** Backend write failures bubble up as a non-zero exit; partial progress (e.g. created some, failed on one) is preserved on whatever the backend committed.
- **Want a preview?** Use [`cronix plan`](/cronix/cli/plan/) (alias `diff`) or `apply --dry-run`. To check whether the installed state matches the manifest without touching it, use [`cronix drift`](/cronix/cli/drift/).
