---
title: Backend coverage strategy
description: Which schedulers cronix supports today, what's planned, and how to propose your own.
---

cronix ships five reference backends in v1 — `crontab`, `systemd-timer`, `kubernetes`, `aws-scheduler`, `vercel`. They cover the schedulers most production deployments actually run on, but they're a fraction of what *exists*. Every PaaS, every cloud, every CI tool ships a cron knob; nobody could maintain in-tree adapters for all of them.

This page is the strategy. It explains **which schedulers fit the cronix model**, **which we plan to ship in core**, and **how the community will fill the rest** via the plugin protocol coming in v2.0.

## What "fits cronix"

The cronix model is narrow on purpose:

1. **Declarative configuration surface.** cronix needs somewhere to write the scheduler entry — a file, an API, a CRD, a JSON blob. Hand-edited UIs don't qualify.
2. **5-field cron expression support.** Or a clean mapping to one. Sub-minute schedulers can be supported but the lowest common denominator is enforced by the validator (`@every 30s` is rejected).
3. **HTTP fire.** The host scheduler invokes `cronix trigger`, which signs and POSTs to the application. In v2.0 this expands to queue publishes, K8s Jobs, exec, and other strategies — but in v1 it's HTTP only.
4. **Ownership annotation surface.** Per [D-026](/cronix/concepts/state/), cronix must be able to mark an entry as "owned by app X, job Y" — via a comment, label, tag, dedicated directory, or whatever the scheduler offers.
5. **Idempotent write semantics.** `cronix apply` runs on every deploy. The backend has to handle "write this entry; if it already exists with the same hash, do nothing" without rate-limiting or producing audit-log noise.

A scheduler that satisfies 4 of 5 is a strong candidate — the missing fifth becomes a [`Q-NNN`](https://github.com/awbx/cronix/blob/main/spec/OPEN_QUESTIONS.md) discussion. Satisfying 0 or 1 is a fundamental incompatibility.

## Tier 1 — natural fit

Schedulers that fit all five criteria cleanly. v1 ships five; v1.1 queues two more.

| Backend | Status | Notes |
|---|---|---|
| `crontab` | **v1** | Universal Unix fallback. Block-marker scheme handles ownership in a text file with no native annotation. |
| `systemd-timer` | **v1** | Linux-only. `.timer` + `.service` units; gives operators `journalctl -u` and `systemctl status` for free. |
| `kubernetes` | **v1** | `CronJob` + `ConfigMap` per (app, job, schedule-index). Anywhere you already run Kubernetes. |
| `aws-scheduler` | **v1** | EventBridge Scheduler (the newer API) with HTTP target → cronix-trigger Lambda. AWS-shop deployments. |
| `vercel` | **v1** | `vercel.json` `crons[]` array, owned wholesale by cronix. JAMstack and edge-deployed apps. |
| `google-cloud-scheduler` | **v1.1 candidate** ([#23](https://github.com/awbx/cronix/issues/23)) | GCP counterpart to `aws-scheduler`. API maps almost line-for-line. |
| `cloudflare-workers-cron` | **v1.1 candidate** ([#24](https://github.com/awbx/cronix/issues/24)) | `wrangler.toml` `[triggers] crons`. Same "cronix owns the array" pattern as `vercel`. |
| `azure-logic-apps` / `functions-timer` | Adopter-driven | NCRONTAB syntax has quirks; ships when an Azure-running adopter shows up. |
| `render` / `fly.io` / `railway` / `netlify-scheduled-functions` / `digital-ocean-app-platform` | Adopter-driven | All technically fit Tier 1. Order of arrival is set by [ADOPTERS.md](https://github.com/awbx/cronix/blob/main/ADOPTERS.md). |

**Adopter-driven** means: if your org runs on Azure and you put yourself in `ADOPTERS.md` with a note saying "we'd use the Azure backend if it existed," that backend jumps the queue. Speculative roadmap entries get bumped behind real demand.

## Tier 2 — doable but awkward

Schedulers where the firing model is workflow-shaped rather than HTTP-shaped. The native primitive runs a *workflow* (a build, a pipeline, a CI job) — and that workflow then has to `curl` your app, which is two hops where one would do.

| Scheduler | Why awkward |
|---|---|
| GitHub Actions `schedule:` | Cron fires the workflow run; the workflow's first step has to call your HTTP handler. Users already in GitHub Actions land usually want native, not cronix-managed. |
| GitLab CI scheduled pipelines | Same pattern. |
| Jenkins / TeamCity / CircleCI / Buildkite scheduled triggers | Same. |

These are **plugin-protocol territory** ([#26](https://github.com/awbx/cronix/issues/26), v2.0). Out-of-tree backends can take the awkwardness — if a CI-platform shop has the maintenance bandwidth, they can ship and own the adapter.

## Tier 3 — doesn't fit

Schedulers where the cron expression and the handler live in **the same code unit** — there's no declarative configuration to reconcile against.

| Scheduler | Why it doesn't fit |
|---|---|
| Deno.cron (Deno Deploy) | `Deno.cron("name", "*/5 * * * *", handler)` is a function call in your code; no separate config to reconcile. |
| Convex cron | Same — defined in `convex/crons.ts`. |
| `node-cron` / BullMQ / Agenda | These are *what cronix replaces*, not what it reconciles. The whole point of cronix is to move the schedule out of the application process. |

Tier 3 schedulers are out of scope permanently. If you're using Tier 3 today and want what cronix offers, the migration path is documented in the [import](/cronix/cli/import/) page (planned for v1.1, see [#16](https://github.com/awbx/cronix/issues/16)).

## Tier 4 — different model entirely

Schedulers that fire something other than an HTTP request, where the cronix v1 contract doesn't apply at all.

| Scheduler | Why out of scope |
|---|---|
| `pg_cron` (PostgreSQL) | Fires SQL, not HTTP. |
| MariaDB Event Scheduler | Same. |
| Apache Airflow | Workflow engine with DAGs and dependencies — explicit RFC non-goal. cronix is single-job, single-fire by design. |

These are out of scope for cronix entirely. Multi-strategy fire ([#25](https://github.com/awbx/cronix/issues/25), v2.0) will *partially* address Tier 4 by adding queue/K8s-Job/exec/SQL fire strategies alongside HTTP — but Airflow's DAG-shaped workflow primitive remains out of scope forever.

## How to propose a new backend

The mechanical version of "fits cronix" criteria:

1. **Open a [Spec question](https://github.com/awbx/cronix/issues/new?template=spec-question.yml)** describing the scheduler, where it sits in the tier table, and how each of the five criteria is satisfied (or which one isn't).
2. **Cite an adopter signal** — link to your `ADOPTERS.md` PR or describe the production deployment. Adopter-driven proposals are weighted heavily.
3. **Confirm conformance willingness** — the new backend must pass `spec/conformance/` vectors and the backend matrix CI. If you can't commit to that, the proposal lands as a [#26](https://github.com/awbx/cronix/issues/26) plugin candidate, not a core addition.

The first maintainer to triage the issue assigns it a `Q-NNN` number; the discussion lives in the issue. When the design is settled, the proposal is promoted to a `D-NNN` decision and implementation work begins.

## The plugin protocol (v2.0)

Core cronix is fundamentally constrained by what one or two maintainers can review and keep current. A backend's API drift, auth quirks, rate limits, error messages, and edge cases are all maintenance work — every in-tree backend adds permanent load.

[Issue #26](https://github.com/awbx/cronix/issues/26) is the v2.0 RFC for an **out-of-tree plugin protocol**: a stable gRPC contract that lets community-maintained backends ship without sitting in this repo. Terraform-shape: discovery via `cronix-backend-<name>` binaries on `PATH`, per-session subprocess lifecycle, conformance-vector validation at install time, an optional `plugins:` checksum block in the manifest for trust pinning.

After v2.0 lands, the in-tree list (this page's Tier 1 column) will be the **reference set** that the protocol is shaped against — not the *only* set. Community backends for Azure, the various PaaS providers, the Tier 2 CI shapes, and anything else with a willing maintainer will live out-of-tree against the same protocol the core backends implement.

## Going deeper

- [Backends overview](/cronix/backends/overview/) — per-backend pages and ownership marker schemes
- [State management](/cronix/concepts/state/) — why "fits cronix" demands an ownership annotation surface
- [ROADMAP.md](https://github.com/awbx/cronix/blob/main/ROADMAP.md) — milestones, dates, what ships when
- [Issue #26](https://github.com/awbx/cronix/issues/26) — the plugin protocol RFC
- [Issue #25](https://github.com/awbx/cronix/issues/25) — multi-strategy fire (the answer to Tier 4 partially)
