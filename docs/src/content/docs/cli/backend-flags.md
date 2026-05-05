---
title: Backend flags reference
description: Shared flags that select and configure a host scheduler backend across apply, plan, drift, list, show, prune, and history.
---

The seven mutating-or-reading commands that talk to a single backend — [`apply`](/cronix/cli/apply/), [`plan`](/cronix/cli/plan/), [`drift`](/cronix/cli/drift/), [`list`](/cronix/cli/list/), [`show`](/cronix/cli/show/), [`prune`](/cronix/cli/prune/), and [`history`](/cronix/cli/history/) — all share the same backend-selection flag set. This page is the canonical reference for that set; the per-command pages link here instead of repeating it.

[`global-status`](/cronix/cli/global-status/) does not use these flags. It loads backends from `~/.cronix/cronix.yaml` so it can query several at once.

## Picking a backend

| Flag | Default | Purpose |
|---|---|---|
| `--backend` | `crontab` | Which host scheduler to drive: `crontab`, `systemd-timer`, `kubernetes`, or `aws-scheduler` |
| `--trigger-bin` | `/usr/local/bin/cronix` | Absolute path to the cronix binary on the host (the schedule entry runs `<trigger-bin> trigger <app>.<job>`) |

`--trigger-bin` is read by every host-side backend (crontab, systemd-timer). The Kubernetes backend ignores it — the in-cluster image entrypoint runs `cronix trigger` instead.

## crontab

| Flag | Default | Purpose |
|---|---|---|
| `--crontab-path` | `/etc/crontab` | The crontab file to reconcile. Use a non-system path like `/var/at/tabs/me` for per-user crontabs |

cronix only touches lines fenced inside its `# cronix:begin` / `# cronix:end` block; everything outside the fence is preserved byte-for-byte.

## systemd-timer

| Flag | Default | Purpose |
|---|---|---|
| `--systemd-unit-dir` | `/etc/systemd/system` | Where to write owned `.timer` and `.service` unit files |

cronix-owned units carry an `X-Cronix=true` directive in their `[Unit]` section; foreign units in the same directory are left alone.

## kubernetes

| Flag | Default | Purpose |
|---|---|---|
| `--k8s-namespace` | `default` | Namespace for owned `CronJob` and `ConfigMap` objects |
| `--k8s-image` | `awbx/cronix:latest` | Container image used by the CronJob pod (must contain a working `cronix` binary) |
| `--kubeconfig` | (env `KUBECONFIG` → `~/.kube/config`) | Path to a kubeconfig file |
| `--in-cluster` | `false` | Load API config from the in-cluster service account instead of a kubeconfig |

cronix-owned objects carry an `app.kubernetes.io/managed-by: cronix` label and an `cronix.dev/manifest-hash` annotation. Per-job specs are stored in a sibling `ConfigMap` rather than on disk.

## aws-scheduler

| Flag | Default | Purpose |
|---|---|---|
| `--aws-region` | (SDK default chain) | AWS region for the EventBridge Scheduler API |
| `--aws-schedule-group` | `default` | EventBridge Schedule group to write into |
| `--aws-target-arn` | (none) | ARN the schedule invokes — typically the cronix-trigger Lambda |
| `--aws-role-arn` | (none) | IAM role EventBridge assumes to call the target |

The schedule's `Input` field carries the full job spec inline (no S3 round-trip), and the schedule is tagged `cronix:owner=cronix` so `list` can find it again.

## Auth and secrets

| Flag | Default | Purpose |
|---|---|---|
| `--secret-ref` (repeatable) | (none) | One or more `secret_ref` strings: `env:NAME`, `file:/path`, or `raw:literal`. Used to sign HTTPS manifest fetches and forwarded into the per-job spec for the trigger shim |

`--secret-ref` is required when `--manifest` is an `https://` URL; the first resolved secret signs the GET. It is not required when the manifest is a local file.

## Examples

```bash
# Crontab on a laptop, non-system path
cronix apply --manifest ./billing.cronix.json \
  --crontab-path /var/at/tabs/me \
  --trigger-bin /usr/local/bin/cronix

# Kubernetes via in-cluster service account
cronix apply --manifest https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET \
  --backend kubernetes --in-cluster --k8s-namespace billing

# EventBridge Scheduler
cronix apply --manifest ./billing.cronix.json \
  --backend aws-scheduler \
  --aws-region us-east-1 \
  --aws-schedule-group cronix \
  --aws-target-arn arn:aws:lambda:us-east-1:123:function:cronix-trigger \
  --aws-role-arn arn:aws:iam::123:role/cronix-eventbridge
```
