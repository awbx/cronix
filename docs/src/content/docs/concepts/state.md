---
title: State management
description: Where state lives in cronix — and why there is no state file.
---

The first question every adopter asks is: **"but where does the state live?"** Terraform has `state.tfstate`. Argo has its cluster controller. Crossplane has CRDs and a control plane. cronix has none of these — and yet `cronix apply` is reproducible, drift-detectable, and safe to run from any host. Where does the bookkeeping happen?

**Short answer: cronix distributes state to where it most naturally lives — the backend itself.** The crontab line, the systemd unit, the Kubernetes `CronJob`, the EventBridge Schedule, the `vercel.json` entry — each one is annotated with ownership information at the moment cronix creates it. The backend's own native entries *are* the state. No side-channel file, no central DB, no controller running between releases.

This page is the long version of that claim. It walks through every job a Terraform-style state file does, shows where cronix puts that responsibility instead, and is honest about the tradeoffs that result.

## The mapping

Terraform's `state.tfstate` is a single file that bundles together seven distinct responsibilities. cronix unbundles them — each one lives in the place that best knows the answer.

| Terraform state job | Where it lives in cronix |
|---|---|
| **Ownership tracking** — "did I create this?" | Backend-native ownership markers (see below) |
| **ID mapping** — logical name → cloud ID | Not needed. Manifest `app + job + index` *is* the ID. The crontab line, systemd unit, `CronJob`, and EventBridge Schedule are each named/keyed by the manifest. |
| **Drift detection** — intended vs actual | Re-read backend, diff against manifest. [`cronix plan`](/cronix/cli/plan/) and [`cronix drift`](/cronix/concepts/drift/) — both stateless, both run from any host. |
| **Dependency graph** — apply ordering | Not needed. Jobs are independent; no apply order to compute. |
| **Concurrent-write coordination** | Per-backend native primitives. File lock for crontab/systemd, K8s optimistic concurrency for `CronJob`, EventBridge last-write-wins. Documented v1 limitation: concurrent applies from two hosts are *safe* but not load-balanced. |
| **Run history** — what fired when, did it succeed | Backend-native log sources: `journalctl -u`, K8s Events + Pod logs, CloudWatch for EventBridge. [`cronix history`](/cronix/cli/history/) reads from whichever applies. |
| **Sensitive data caching** — secrets, outputs | Never. HMAC secrets are passed by reference (`secret_refs: [...]`); the SDK resolves them at fire time. cronix never persists secret material to disk. |

ID mapping and dependency-graph are non-issues *by design* — the cronix data model is flat (one schedule per (app, job, schedule-index), no dependencies between schedules). For the other five, you'll find the per-backend implementation below.

## Ownership markers (`D-026`)

Every backend has a place where cronix writes "this entry is owned by app=X, job=Y, schedule-index=Z, hash=H". The marker is read on every `plan` and `drift` to answer two questions: **is this mine?** and **is its content still in sync with the manifest?**

| Backend | Marker location |
|---|---|
| `crontab` | A `# cronix:owned app=… job=… index=N hash=…` comment line immediately following each schedule line. Block delimited by `# BEGIN cronix-managed` / `# END cronix-managed` so cronix never touches lines outside its block. |
| `systemd-timer` | `X-Cronix-App=`, `X-Cronix-Job=`, `X-Cronix-Index=`, `X-Cronix-Hash=` ini annotations inside the `.timer` unit. Units live in a dedicated cronix-prefixed directory under `/etc/systemd/system`. |
| `kubernetes` | `cronix.dev/managed: "true"` label plus `cronix.dev/app`, `cronix.dev/job`, `cronix.dev/index`, `cronix.dev/hash` labels on both the `CronJob` and its companion `ConfigMap`. Standard Kubernetes controller pattern. |
| `aws-scheduler` | A `cronix-` name prefix on the EventBridge Schedule, plus a structured `Description` field carrying app/job/index/hash. |
| `vercel` | cronix owns the `vercel.json` `crons[]` array **entirely** when cronix is enabled for the project. No per-entry metadata exists in the Vercel schema; the file-level ownership is documented loudly and verified by `cronix adopt`. |

The hash in every marker is an FNV-1a 64-bit fold over the canonical normalized job, salted by schedule index. See [drift detection](/cronix/concepts/drift/) for how the hash is computed and used.

## What happens on each cronix command

| Command | What it reads | What it writes |
|---|---|---|
| [`apply`](/cronix/cli/apply/) | The manifest (over HTTP) + every backend-native entry tagged as cronix-owned | New or updated owned entries; never anything else |
| [`plan` / `diff`](/cronix/cli/plan/) | Same as apply | Nothing |
| [`drift`](/cronix/cli/drift/) | Same as apply | Nothing; exits 5 if anything diverges |
| [`prune`](/cronix/cli/prune/) | Every owned entry; ignores the manifest entirely | Deletes owned entries that match the prune filter |
| [`history`](/cronix/cli/history/) | The backend's own log source (journald, K8s Events, CloudWatch) | Nothing |
| [`list`](/cronix/cli/list/) | Every owned entry across all configured backends | Nothing |

Notice what's missing: **there is no path that writes to a state file**. Every read goes back to the source of truth (manifest or backend); every write goes to the backend itself.

## Concurrency state

There's one category of state cronix *does* maintain — but only transiently, only at fire time, and only when the manifest opts in.

For jobs with `concurrency: Forbid` or `Replace`, the trigger shim must know whether another invocation is already running for the same job. cronix supports two scopes:

| Scope | Storage | When to use |
|---|---|---|
| `host` (default) | `flock` over a local lock file in `/var/lib/cronix/locks/` | When the job only runs on one host — crontab, systemd-timer, single-replica K8s deployments. |
| `global` | Redis (v1; pluggable later) | When the same job can fire on multiple hosts and you want at-most-one running cluster-wide. |

`global` scope **does require external state** — a Redis instance the trigger shim can reach. This is a documented, operator-controlled choice. The default is `host`, which needs nothing beyond the local filesystem.

This is the only category of state cronix touches. It's ephemeral (the lock's lifetime is one fire), it's externally managed (the operator decides), and the design is explicit about it — see the [concurrency](/cronix/concepts/concurrency/) page.

## Honest tradeoffs

Pretending these don't exist is what gets a "no state file" pitch torn apart by experienced operators. Each is intrinsic to the design:

### 1. crontab is the weakest case

crontab is a text file with no native annotation surface. The cronix-managed *block* (`# BEGIN cronix-managed` / `# END cronix-managed`) is the only thing keeping hand-edits outside the block safe. Lines *inside* the block, however, can be hand-edited and the next `cronix apply` will overwrite them. The block markers plus the per-line `hash=` are how cronix tells "this was edited" from "this is exactly what I wrote." Adopters who want to mix hand-edited crons and cronix-managed crons should keep them in separate files (`/etc/crontab` for hand work, `/etc/cron.d/cronix` for cronix's block).

### 2. Vercel ownership is opinionated

`vercel.json` `crons[]` is one flat array with no per-entry metadata. When cronix is enabled for a Vercel project, **the whole array is owned by cronix**. Mixing hand-written `crons[]` entries with cronix-managed ones in the same file is unsupported and will produce surprising results. The vercel backend page calls this out at the top.

### 3. No central run history

Each backend's history is in its own native log: `journalctl -u`, `kubectl get events`, CloudWatch, Vercel's deployment logs. [`cronix history`](/cronix/cli/history/) is a thin reader; it does *not* aggregate across backends. Cross-backend queries ("did any cronix job fail in the last hour?") require operators to glue together native log sources — there's no cronix-side aggregator. This is a deliberate v1 non-goal documented in the RFC.

### 4. `concurrency_scope: global` requires Redis

Operators who declare any job with `concurrency_scope: global` must provide a Redis instance. The cronix CLI does not run Redis, doesn't bundle it, doesn't manage it. This is the one category where the operator is on the hook for external state. The default scope is `host` (no external dependency).

### 5. Concurrent applies are safe but not load-balanced

Two operators running `cronix apply` simultaneously against the same backend will not corrupt anything — flock and K8s optimistic concurrency catch the conflict — but neither will the work be merged. One wins; the other reports an error and exits non-zero. Most adopters serialize applies through CI, which never hits this.

## "But Terraform's state file is the answer to N problems cronix has too" — does it?

Sometimes. Here's an honest breakdown:

| Problem | Terraform's answer | cronix's answer | Tradeoff |
|---|---|---|---|
| Knowing what's deployed | Read state.tfstate | Re-read the backend | cronix is slower for large estates (an API roundtrip per backend); cronix is always up-to-date (no stale state file) |
| Detecting drift | Compare config to state, then state to cloud | Compare manifest to backend directly | cronix's check is fundamentally cheaper for the small-N case (cron jobs are few; cloud resources are many) |
| Migrating between backends | `terraform state mv` | Different cronix manifest pointed at different `--backend` | Conceptually identical; cronix's migration is a re-apply |
| Audit log of who changed what | State file in version control | Git of the manifest + backend-native audit logs (CloudTrail, K8s audit, journald) | The audit story is *better* for cronix in cloud-native backends; equivalent for crontab/systemd |
| Locking concurrent operations | State file lock (S3+DynamoDB, Terraform Cloud) | Backend-native concurrency primitives | Acceptable for typical cronix deploys (CI serializes); not for multi-operator concurrent-apply scenarios |

The thing cronix gives up versus Terraform is **the speed advantage of having a single source of truth that's already cached locally**. For small N (hundreds of cron jobs at most, not thousands of cloud resources), re-reading the backend is faster than the state-file-management overhead. For large N, the equation flips — but cronix is not aimed at the large-N case.

## Going deeper

- [Drift detection](/cronix/concepts/drift/) — the hash algorithm, the diff output format, exit codes
- [Manifest format](/cronix/concepts/manifest/) — the source-of-truth document `cronix apply` reads
- [Concurrency policies](/cronix/concepts/concurrency/) — when host vs global scope, when Allow/Forbid/Replace
- [Backends](/cronix/backends/overview/) — per-backend ownership marker schemes in full detail
- [RFC §State](https://github.com/awbx/cronix/blob/main/spec/RFC.md) — the protocol-level statements about state, normative
