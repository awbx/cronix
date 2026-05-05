---
title: cronix history
description: Show recent terminal runs for one cronix-managed job, sourced from the backend's native log stream.
---

`history` reads run records for one `(app, job)` pair from the backend's native source — `journalctl` for systemd-timer, Pod logs for kubernetes — and prints one row per terminal run. The trigger shim emits one slog-JSON record per attempt; `History` folds those into one entry per terminal outcome (success, app-rejected, retries-exhausted, lock-contended, timeout).

The crontab backend currently returns `nil` pending a syslog reader; use `journalctl _SYSTEMD_UNIT=cron.service` or your distro's equivalent until that lands. The kubernetes and systemd-timer backends return real data.

## Synopsis

```
cronix history <app>.<job> [flags]
```

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--since` | (none) | Look back this duration. Accepts Go duration syntax: `1h`, `24h`, `168h` |
| `--until` | (now) | Stop at this duration ago — also a Go duration. Useful for windowed queries: `--since 48h --until 24h` |
| `--status` | (any) | Filter to one status: `ok`, `failed`, `lock-contended`, `timeout`, `unknown` |
| `--limit` | `50` | Max records to show. `0` means no limit |
| `-o, --output` | `table` | `table` or `json` |

Plus all [backend flags](/cronix/cli/backend-flags/).

## Examples

Last 24 hours of runs from a systemd-timer-backed job:

```bash
cronix history billing.reconcile-payments \
  --backend systemd-timer --since 24h
# WHEN                  RUN_ID        ATTEMPT  STATUS  SOURCE
# 2026-05-05T10:15:00Z  abc123def456  1        ok      journal
# 2026-05-05T10:00:00Z  abc123def012  1        ok      journal
# 2026-05-05T09:45:00Z  abc123def789  2        ok      journal
```

Failed runs only, in JSON, from a Kubernetes-backed job:

```bash
cronix history billing.reconcile-payments \
  --backend kubernetes --in-cluster --k8s-namespace billing \
  --since 7d --status failed -o json
```

A windowed query — yesterday's runs only:

```bash
cronix history billing.reconcile-payments \
  --backend systemd-timer \
  --since 48h --until 24h --limit 0
```

## Notes

- **`since` and `until` are durations, not timestamps.** Both are interpreted as "this long ago"; `--until 0s` is identical to omitting it. Go duration syntax: `30m`, `2h`, `48h`, `168h` (no `7d` shortcut — use `168h`).
- **One row per terminal run, not per attempt.** A job that succeeded after two retries appears as one `ok` row with `attempt=3`. The intermediate retry attempts are folded into that record by the `History` reader.
- **crontab returns no rows today.** The crontab backend's `History` is a `nil` stub awaiting a syslog reader. Use `cronix list` to confirm the schedule is installed and watch your distro's syslog for `cronix trigger` invocations until then.
- **For "is it installed and what's the schedule?"** use [`cronix show`](/cronix/cli/show/). For "is it diverged from my manifest?" use [`cronix drift`](/cronix/cli/drift/).
