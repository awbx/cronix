---
title: cronix prune
description: Remove every cronix-owned entry from the configured backend — destructive, with interactive confirmation by default.
---

`prune` lists every entry the configured backend reports as cronix-owned and deletes them. With `--app`, only entries belonging to that app are pruned. It is the canonical way to uninstall cronix-managed schedules from a host without touching foreign entries — the crontab block fence, systemd `X-Cronix=true` directive, Kubernetes `app.kubernetes.io/managed-by` label, and AWS `cronix:owner` tag all gate what `prune` will touch.

It is destructive. By default `prune` prompts for confirmation; pass `--yes` to skip the prompt for non-interactive uninstalls (CI teardown, host decommissioning).

## Synopsis

```
cronix prune [flags]
```

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--app` | (none) | Limit pruning to entries belonging to this app. Without it, every cronix-owned entry across every app is removed |
| `--yes` | `false` | Skip the interactive confirmation prompt |
| `-o, --output` | `table` | `table` or `json` |

Plus all [backend flags](/cronix/cli/backend-flags/).

## Examples

Interactive prune — prompts before deleting:

```bash
cronix prune --crontab-path /tmp/cronix.crontab
# This will delete 2 cronix-owned entries from backend "crontab". Continue? [y/N]: y
# Prune: backend=crontab removed 2 entries across 2 jobs
#   - billing.reconcile-payments
#   - billing.send-invoices
```

Non-interactive prune of one app's entries:

```bash
cronix prune --app billing --yes \
  --crontab-path /tmp/cronix.crontab
# Prune: backend=crontab removed 2 entries across 2 jobs
#   - billing.reconcile-payments
#   - billing.send-invoices
```

Nothing to remove — exits zero:

```bash
cronix prune --crontab-path /tmp/cronix.crontab --yes
# Prune: backend=crontab nothing to remove
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Successful prune (including "nothing to remove") |
| Non-zero | Confirmation declined, backend Delete failed, or backend construction failed |

If you answer anything other than `y` / `yes` to the prompt, `prune` aborts with a non-zero exit and an `aborted` error. Stdin not being a terminal counts as declining.

## Notes

- **Destructive.** `Backend.Delete` is invoked for every owned `(app, job)` pair. Multi-schedule jobs are collapsed into one delete call (the backend removes all schedules for the pair atomically).
- **Foreign entries are never touched.** prune walks the same `Backend.List` view as [`list`](/cronix/cli/list/), which by construction only returns rows the backend tagged as cronix-owned.
- **Spec files are NOT cleaned automatically.** prune drives the backend; it does not sweep `/etc/cronix/jobs/`. If you want orphan-free spec files after a prune, either use [`apply`](/cronix/cli/apply/) with an empty manifest (which sweeps spec-dir) or remove the directory manually.
- **`--yes` in CI:** safe when paired with `--app` to scope the blast radius. An unscoped `--yes` removes everything cronix has on that backend — make that explicit.
