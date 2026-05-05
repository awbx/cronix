---
title: crontab backend
description: Reconcile against /etc/crontab — the simplest possible deployment.
---

The crontab backend writes 2-line owned blocks to a single crontab file:

```cron
*/15 * * * * /usr/local/bin/cronix trigger billing.reconcile-payments
# cronix:owned app=billing job=reconcile-payments hash=abc123def idx=0
```

cronix never touches lines that lack the ownership comment. You can run cronix alongside hand-edited cron entries without conflict.

## Prerequisites

- `cron(8)` installed and running.
- The `cronix` binary at a stable absolute path readable by the cron user (typically `/usr/local/bin/cronix`).
- A directory the cronix process can write to for per-job spec files (default `/etc/cronix/jobs`).
- A directory for flock files (default `/var/lock/cronix`).
- One or more secrets (env var, file path, or — dev only — inline literal) shared between the reconciler and the app.

## Install the binary

```bash
go install github.com/awbx/cronix/go/cmd/cronix@latest
sudo cp "$(go env GOPATH)/bin/cronix" /usr/local/bin/cronix
sudo chmod 755 /usr/local/bin/cronix
sudo mkdir -p /etc/cronix/jobs /var/lock/cronix
```

## Reconcile from CI

The typical flow is one `cronix apply` per deploy:

```bash
cronix apply \
  --manifest https://billing.internal/.well-known/cron-manifest \
  --backend crontab \
  --crontab-path /etc/crontab \
  --trigger-bin /usr/local/bin/cronix \
  --spec-dir /etc/cronix/jobs \
  --secret-ref env:CRON_SECRET_V2 \
  --secret-ref env:CRON_SECRET_V1
```

`apply` is idempotent — when the manifest already matches what's installed, it's a complete no-op (no logs at INFO+, no file mtime change). Safe to run on every CI build.

## Inspect

```bash
cronix list --backend crontab --crontab-path /etc/crontab
# APP      JOB                  IDX  HASH
# billing  reconcile-payments   0    abc123def4567890

cat /etc/crontab | grep cronix
```

## Drift check

```bash
cronix drift --manifest ... --backend crontab --crontab-path /etc/crontab --exit-on-drift
```

Returns exit 5 (and a description of the divergent ops) when anything has changed since the last apply.

## Run history

`cronix history` against the crontab backend reads from `syslog`/`journalctl -u cron` and `MAILTO=` outputs. Setup tips:

- Set `MAILTO=ops@example.com` (or a local mbox) at the top of your crontab to capture stderr and non-zero exits.
- For systemd-cron deployments, `journalctl _COMM=cron` is the most reliable source.

## Limitations

- **Per-job timezone is not honored.** crontab uses the system timezone for all entries. `cronix apply` flags this in `Validate` if your job specifies a non-UTC timezone — fix by either changing the job's timezone to UTC or running on a host whose system timezone is the desired one.
- **Sub-minute scheduling is rejected.** `@every 30s` cannot be expressed in 5-field cron. Use systemd-timer if you need it.
- **`Replace` concurrency policy** is implemented as `Forbid` in v1 (the SIGTERM-the-previous-holder path is deferred). The shim logs the intent.

## macOS notes

On macOS the conventional crontab path is the per-user crontab (`/var/at/tabs/<user>`), which is root-owned and only writable via `crontab(1)`. Pattern:

```bash
# render to a tracked file
cronix apply --crontab-path /tmp/cronix.crontab ...

# install via the standard crontab tool
crontab /tmp/cronix.crontab

# verify
crontab -l
```

`/etc/crontab` doesn't exist by default on macOS. macOS `cron(8)` also requires Full Disk Access in System Settings → Privacy & Security to read user crontabs.
