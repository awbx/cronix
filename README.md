# cronix

> **Cron jobs as code.** Apps declare scheduled work in their own code; cronix reconciles those declarations against the host's native scheduler.

> ⚠️ **Pre-alpha — under active development.** No release yet; APIs and on-the-wire shapes will change. Do not run in production.

## The pitch

Today the schedule for a job lives somewhere different from the code that handles it — in a UI, an EventBridge rule, a hand-edited crontab, a separate YAML repo. Changes require coordinating two places. Drift is invisible. Reviewers see the handler change but miss the schedule change.

`cronix` puts the schedule next to the handler. The application is the source of truth for its own schedules via a manifest endpoint. `cronix apply` reconciles that manifest against whatever scheduler the host provides — `crontab`, `systemd-timer`, Kubernetes — installing, updating, or removing entries as needed. The host's native scheduler does the firing. A small Go binary, `cronix trigger`, handles HMAC signing, concurrency locks, timeouts, and retries on every fire.

The protocol is the product. The reconciler and SDK are reference implementations.

## Architecture

```
              app (your service)
              GET /.well-known/cron-manifest
              POST /api/v1/scheduled/<name>
                       │
                       │ (1) read manifest
                       ▼
                cronix apply (Go)
                       │
                       │ (2) install/update/delete entries
                       ▼
              host scheduler (crontab / systemd-timer / k8s CronJob)
                       │
                       │ (3) invoke on schedule
                       ▼
                cronix trigger (Go)
                       │   • acquires lock
                       │   • signs HMAC
                       │   • POSTs to your handler
                       │   • applies timeout + retries
                       ▼
                app handler (verifies signature, dedupes by run-id)
```

## Status

Pre-alpha. The implementation phases that build out v1 are tracked in [PLAN.md](./PLAN.md). The authoritative on-the-wire spec lives in [packages/spec/RFC.md](./packages/spec/RFC.md).

## Repo layout

- `packages/spec/` — RFC, decisions, JSON Schema, conformance vectors (language-neutral)
- `packages/sdk/` — `@cronix/sdk` for TypeScript apps
- `cmd/cronix/` — single Go binary entrypoint
- `internal/` — Go internals (parsing, auth, backends, locks, trigger, reconciler)
- `pkg/cronsdk/` — public Go SDK (signature verification only)
- `examples/` — runnable examples per framework
- `deploy/` — Dockerfile and Helm chart

## Build

```bash
# TypeScript
pnpm install
pnpm build && pnpm test && pnpm lint && pnpm typecheck

# Go
go build ./...
go test ./...
go vet ./...

# Multi-platform binaries (snapshot — no release)
goreleaser build --snapshot --clean
```

## License

MIT © Abdelhadi Sabani
