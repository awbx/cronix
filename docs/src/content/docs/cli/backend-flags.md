---
title: Backend flags reference
description: Shared flags that select and configure a host scheduler backend across apply, plan, drift, list, show, prune, and history.
---

The seven mutating-or-reading commands that talk to a single backend — [`apply`](/cronix/cli/apply/), [`plan`](/cronix/cli/plan/), [`drift`](/cronix/cli/drift/), [`list`](/cronix/cli/list/), [`show`](/cronix/cli/show/), [`prune`](/cronix/cli/prune/), and [`history`](/cronix/cli/history/) — all share the same backend-selection flag set. This page is the canonical reference for that set; the per-command pages link here instead of repeating it.

[`global-status`](/cronix/cli/global-status/) does not use these flags. It loads backends from `~/.cronix/cronix.yaml` so it can query several at once.

## Two ways to pick a backend

Each of the seven commands accepts the backend in two equivalent shapes:

```bash
# A. Legacy: --backend on the top-level command (everything-shown).
cronix apply --backend kubernetes --k8s-namespace billing --manifest ./m.json

# B. Sub-subcommand: kubectl-style. --help and shell completion only
#    list the flags relevant to the chosen backend.
cronix apply kubernetes --k8s-namespace billing --manifest ./m.json
```

Both produce identical behavior. **B is the recommended form** — the focused flag set makes `--help` short and shell completion (Bash, Zsh, fish, PowerShell) suggest only relevant flags. **A is preserved for backwards compatibility** so existing scripts and CI configurations keep working.

The pattern applies to every backend-aware command:

```bash
cronix plan vercel --manifest ./m.json --vercel-json-path ./vercel.json
cronix drift systemd-timer --manifest ./m.json --systemd-unit-dir /etc/systemd/system
cronix list crontab --crontab-path /etc/crontab
cronix show kubernetes billing.reconcile --k8s-namespace billing
cronix prune aws-scheduler --aws-region us-east-1 --yes
cronix history systemd-timer billing.reconcile --since 24h
```

## Picking a backend (legacy form)

| Flag | Default | Purpose |
|---|---|---|
| `--backend` | `crontab` | Which host scheduler to drive: `crontab`, `systemd-timer`, `kubernetes`, `aws-scheduler`, or `vercel`. Required only on the legacy form (A); the sub-subcommand form (B) hardcodes it |
| `--trigger-bin` | `/usr/local/bin/cronix` | Absolute path to the cronix binary on the host (the schedule entry runs `<trigger-bin> trigger <app>.<job>`) |

`--trigger-bin` is read by every host-side backend (crontab, systemd-timer). Kubernetes, aws-scheduler, and vercel ignore it — Kubernetes uses the in-cluster image entrypoint; aws-scheduler points at the cronix-trigger Lambda; Vercel fires routes directly.

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

## vercel

| Flag | Default | Purpose |
|---|---|---|
| `--vercel-json-path` | `./vercel.json` | Path to the `vercel.json` file the backend rewrites |
| `--vercel-trigger-prefix` | `/api/v1/scheduled/` | Path prefix that identifies cronix-owned entries inside `crons[]`. Override only when you've changed the SDK's default trigger mount path |

Cronix-owned entries in `crons[]` round-trip cleanly; non-cronix entries and every other top-level key in `vercel.json` are preserved byte-for-byte.

## Auth and secrets

| Flag | Default | Purpose |
|---|---|---|
| `--secret-ref` (repeatable) | (none) | One or more `secret_ref` strings: `env:NAME`, `file:/path`, or `raw:literal`. Used to sign HTTPS manifest fetches and forwarded into the per-job spec for the trigger shim |

`--secret-ref` is required when `--manifest` is an `https://` URL; the first resolved secret signs the GET. It is not required when the manifest is a local file.

## Examples

Sub-subcommand form (recommended):

```bash
# Crontab on a laptop, non-system path
cronix apply crontab \
  --manifest ./billing.cronix.json \
  --crontab-path /var/at/tabs/me

# Kubernetes via in-cluster service account
cronix apply kubernetes \
  --manifest https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET \
  --in-cluster --k8s-namespace billing

# EventBridge Scheduler
cronix apply aws-scheduler \
  --manifest ./billing.cronix.json \
  --aws-region us-east-1 \
  --aws-schedule-group cronix \
  --aws-target-arn arn:aws:lambda:us-east-1:123:function:cronix-trigger \
  --aws-role-arn arn:aws:iam::123:role/cronix-eventbridge

# Vercel — rewrites vercel.json crons[] in place
cronix apply vercel \
  --manifest ./billing.cronix.json \
  --vercel-json-path ./vercel.json
```

Legacy form (still supported):

```bash
cronix apply --manifest ./billing.cronix.json \
  --backend kubernetes --in-cluster --k8s-namespace billing
```
