# RFC: cronix — Cron Jobs as Code

## Status

**Draft.** Pre-alpha; v1 in active construction. The implementation phases that
build out this RFC are tracked in `/PLAN.md`.

## Summary

`cronix` puts the schedule next to the handler. An application is the source
of truth for its own scheduled work via a manifest endpoint at
`GET /.well-known/cron-manifest`. The `cronix` reconciler reads that manifest
and installs, updates, or removes entries in the host's native scheduler
(`crontab`, `systemd-timer`, Kubernetes `CronJob`). The host scheduler does
the firing. A small Go binary, `cronix trigger`, runs at every fire and
handles HMAC signing, concurrency locking, timeouts, and retries on the
application's behalf.

The protocol is the product. The reconciler and SDK are reference
implementations.

## Motivation

*(Phase 1 will expand this.)*

Today the schedule for a job lives somewhere different from the code that
handles it — a UI, an EventBridge rule, a hand-edited crontab, a separate
YAML repo. Changes require coordinating two places. Drift is invisible.
Reviewers see the handler change but miss the schedule change. `cronix`
collapses those two things into one declaration that travels with the code.

## Goals and Non-goals

*(Phase 1 will expand this.)*

### Non-goals (v1)

- Long-running scheduler daemon
- Persistent state store of any kind
- Run history database
- Workflow orchestration / job chaining / DAGs
- Built-in web UI
- Plugin system with dynamic loading (backends are compiled in)
- One-shot `run-at` (specific timestamp) jobs
- CRD-based K8s deployment (Helm chart is enough)

## Limitations

*(Phase 1 will expand this. See `PLAN.md` §3 for the full list.)*

## Terminology

*(Phase 1 will expand this.)*

## The Manifest

*(Phase 1 will populate this.)*

## Authentication

*(Phase 2 will populate this.)*

## SDK Contract

*(Phase 3 will populate this.)*

## Reconciliation Model

*(Phase 4 will populate this.)*

## Backend Adapter Contract

*(Phase 5 will populate this.)*

## Backend Fidelity Matrix

*(Phase 5 will populate this.)*

## Trigger Shim Behavior

*(Phase 5 will populate this.)*

## CLI

*(Phase 6 will populate this.)*

## Deployment

*(Phase 7 will populate this.)*

## Alternatives Considered

*(Refined throughout.)*

## Prior Art

*(Phase 1 will populate this.)*

## Changelog

- **2026-05-04 — Phase 0.** Repository scaffolding only. No product code.
  Locked decisions D-001 through D-029 captured in DECISIONS.md. Repo
  layout deviates from `PLAN.md` §6: a polyglot top-level (`spec/`,
  `ts/`, `go/`) replaces the original Go-at-root + TS-in-`packages/`
  design. The deviation is recorded as D-029.

## Open Questions

See `OPEN_QUESTIONS.md`.
