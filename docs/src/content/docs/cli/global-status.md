---
title: cronix global-status
description: Read-only multi-backend aggregator that lists every cronix-owned entry across configured backends.
---

`global-status` reads an operator config file (`~/.cronix/config.yaml` or `/etc/cronix/config.yaml`) and queries `Backend.List()` on every configured backend in parallel. It's a stateless aggregator — backends remain the source of truth, no registry, no cache.

This command does not compare against a manifest (that's [`cronix drift`](https://github.com/awbx/cronix/blob/main/PLAN.md)). It only answers: **"what does cronix currently own on this host?"**

## Quick example

`~/.cronix/config.yaml`:

```yaml
version: 1
backends:
  - name: laptop
    type: crontab
    crontab_path: /var/at/tabs/me
    trigger_bin: /usr/local/bin/cronix
  - name: prod-cluster
    type: kubernetes
    namespace: scheduled
    in_cluster: false
    kubeconfig: ~/.kube/prod
  - name: prod-eb
    type: aws-scheduler
    region: us-east-1
    schedule_group: cronix
```

```bash
cronix global-status
# BACKEND       TYPE             APP              JOB                 IDX  HASH              STATUS
# laptop        crontab          billing-service  reconcile-payments  0    eefe2dd0dcf563e2  OK
# laptop        crontab          billing-service  send-invoices       0    0afcd05672500c2a  OK
# prod-cluster  kubernetes       analytics        nightly-rollup      0    a1b2c3d4e5f60718  OK
# prod-eb       aws-scheduler    -                -                   -    -                 EMPTY
```

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--config` | `~/.cronix/config.yaml` → `/etc/cronix/config.yaml` | Path to the backends config |
| `--backend` (repeatable) | (all) | Limit to named entries from the config |
| `--with-history` | false | Add `LAST_FIRE` / `LAST_STATUS` columns (one `History` call per entry; slow) |
| `--history-since` | `24h` | Lookback window for `--with-history` |
| `--strict` | false | Exit non-zero if any backend errors |
| `--parallel` | `4` | Concurrent backend queries |
| `--per-backend-timeout` | `30s` | Deadline for each backend's `List` + `History` calls |
| `--secret-ref` (repeatable) | (none) | Forward to backends that need auth (kubernetes, aws-scheduler) |
| `-o, --output` | `table` | `table` or `json` |

## Status column meanings

| Status | Meaning |
|---|---|
| `OK` | At least one cronix-owned entry was found |
| `EMPTY` | Backend reachable, no cronix-owned entries |
| `ERROR: <message>` | Backend construction or `List()` failed; other backends still queried |

## Failure isolation

A broken backend never blocks the others. Even if your kubeconfig is wrong, the crontab + AWS rows still report. With `--strict`, the command exits non-zero overall when any row errored — useful for monitoring scripts.

## Why not a state file

cronix's design records ownership inside the resource it manages — a comment in the crontab, an annotation in the systemd unit, a label on the Kubernetes object, a tag on the AWS schedule. There is no side-channel state file because every backend can carry its own provenance. `global-status` simply asks each backend "what do you have?" on every invocation.

## Config file is configuration, not state

`~/.cronix/config.yaml` declares **which backends to query**. It never records what's installed. cronix never writes it. Adding a new backend to your config doesn't affect the backend itself — `apply` is what writes; `global-status` only reads.
