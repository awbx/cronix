---
title: Backends
description: cronix reconciles against four host schedulers — crontab, systemd-timer, Kubernetes, and AWS EventBridge Scheduler.
---

cronix supports four host schedulers in v1. The backend is selected at apply time via `--backend <name>`; everything else (manifest, signing, retries) is identical across backends.

| Backend | What cronix writes | When to use |
|---|---|---|
| [`crontab`](/cronix/backends/crontab/) | `/etc/crontab` lines with `# cronix:owned` markers | Bare-metal Linux/macOS, the simplest possible deployment |
| [`systemd-timer`](/cronix/backends/systemd/) | `.timer` + `.service` units in `/etc/systemd/system` | Systemd-based Linux hosts; gives you `journalctl -u`, `systemctl status`, and graceful Replace semantics for free |
| [`kubernetes`](/cronix/backends/kubernetes/) | `CronJob` + `ConfigMap` per (app, job, schedule-index) | Anywhere you already run on Kubernetes |
| [`aws-scheduler`](/cronix/backends/aws/) | EventBridge Schedules → cronix-trigger Lambda | Serverless / multi-account AWS deployments |

## Ownership tracking

Every backend records ownership inside the resource it manages — never in a side-channel state file. cronix never modifies entries it didn't create.

| Backend | Marker location |
|---|---|
| `crontab` | A `# cronix:owned app=… job=… hash=…` comment line immediately following the schedule line |
| `systemd-timer` | `X-Cronix-{App,Job,Index,Hash}=` annotations inside the unit files |
| `kubernetes` | `cronix.dev/managed: "true"` + `cronix.dev/{app,job,index,hash}` labels on the `CronJob` and `ConfigMap` |
| `aws-scheduler` | A `cronix-` name prefix and a structured `Description` field on the EventBridge Schedule |

## Idempotency

`cronix apply` with no manifest changes is a complete no-op: no file mtime change, no API call, no log churn at INFO. Safe to run on every CI deploy. Hash-based change detection — the `hash=` field carries an FNV-1a fold over the canonical normalized job, so unchanged jobs are skipped.

## Drift detection

`cronix drift --exit-on-drift` returns exit 5 when anything in the backend has diverged from the manifest. The drift command re-reads the backend on every run; nothing is cached. Hand-edits to a `cronix:owned` line, a deleted unit file, or a manually-changed CronJob are all caught.

## Multi-host operator view

If you manage multiple backends from one operator host, [`cronix global-status`](/cronix/cli/global-status/) reads all of them from a single config file (`~/.cronix/config.yaml`) and prints a unified table.
