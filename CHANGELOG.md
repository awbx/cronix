# Changelog

All notable changes to cronix are documented here. Generated from `git log`; see ts/scripts/generate-changelog.mjs.

## [0.7.0] - 2026-05-05

### Features

- feat(cli): global-status aggregates owned entries across configured backends (`19fe917`)

### Chores

- chore(example): hono-app declares three scheduled jobs (`b5110aa`)

## [0.6.1] - 2026-05-05

### Refactors

- refactor(go): split aws + crontab into per-concern files (`e7ef38b`)
- refactor(go): extract internal/policy + split systemd into per-concern files (`fa954ef`)

### Chores

- chore(release): pnpm version:bump + auto-generated CHANGELOG.md (`a608642`)

## [0.6.0] - 2026-05-05

### Features

- feat(sdk): split framework adapters into sibling packages — v0.6.0 (`be7ed23`)
- feat(aws): cronix-trigger Lambda shim + inline-spec EventBridge Input (`f486e71`)
- feat(aws): EventBridge Scheduler backend (cloud abstraction starts here) (`813e66c`)

## [0.5.1] - 2026-05-04

### Features

- feat(release): goreleaser nfpms — deb / rpm / apk packages on every tag (`c5cb7ec`)

### Bug Fixes

- fix(backends): drift detection catches manual schedule edits (k8s + systemd) (`2a7bd64`)

### Docs

- docs: README status + install snippet → v0.5.0 (`a20176f`)

## [0.5.0] - 2026-05-04

### Features

- feat(crontab,init): crontab History via journalctl + cronix init scaffold (`25f157d`)

### Docs

- docs(cli): drop TODO marker from history --help — both backends wired (`bb97a9b`)
- docs: bump version examples to v0.4.0 in README + install.sh (`03adbfc`)

### Chores

- chore(helm): refresh chart for v0.4.0 — image, in-cluster wiring, README (`3589629`)

## [0.4.0] - 2026-05-04

### Features

- feat(k8s,history): cronix history --backend kubernetes via pod logs (`7f1dcd6`)
- feat(systemd,cli): cronix history backed by journalctl (`255d9c8`)
- feat(cli): cronix show <app>.<job> for backend inspection + drift (`ea53611`)

### Bug Fixes

- fix: gofmt internal/cli/commands/show.go (struct field alignment) (`cbb9323`)

### Docs

- docs: refresh kubernetes/systemd guides for live backends + README status (`b109600`)

## [0.3.0] - 2026-05-04

### Features

- feat(cli): cronix prune — operator uninstall command (`9aab364`)
- feat(systemd): full timer/service lifecycle with daemon-reload + enable (`a971a5c`)
- feat(k8s): wire client-go for full apply/drift/list lifecycle (`5e224e5`)

### Bug Fixes

- fix(k8s): satisfy staticcheck QF1001 + QF1008 in backend (`04224e8`)

## [0.2.0] - 2026-05-04

### Features

- feat(sdk): nest and koa adapters (`8d57ac6`)
- feat(sdk): framework adapters as subpath exports (express, fastify, vercel) (`9f5c0df`)
- feat(sdk): merge default vars from createCron with per-fire vars (`123130d`)
- feat(sdk): typed env (Bindings) and var (Variables), Hono-style generic (`2a749d3`)
- feat(sdk): three-tier integration API; keep registry/dispatch, add ergonomics (`ed9b97f`)
- feat: install.sh — one-liner installer for the cronix CLI (`eb394e9`)

### Bug Fixes

- fix(examples): drop stray verify import + biome format on hono index (`99c8f22`)
- fix(apply): remove orphan spec files when a job is deleted from the manifest (`a948d06`)
- fix(examples): declare auth.secret_refs in registered jobs (`9edc76d`)

### Refactors

- refactor(sdk): adapters expose handle(fn) instead of mount(app, cron) (`3ddd14a`)

### Docs

- docs(examples): use cron.handle() in express + fastify too (`e46780b`)

## [0.1.1] - 2026-05-04

### Other

- ci(release): remove temporary docker secret diagnostic (`246582e`)

## [0.1.0] - 2026-05-04

### Features

- feat(polish, sdk-go, docs, helm): phase 7 — release candidate (`bf3a498`)
- feat(cli): phase 6 — validate, plan, apply, list, drift, completion (`4df7973`)
- feat(go): phase 5 — trigger shim, crontab backend, reconciler, systemd/k8s skeletons (`186fc09`)
- feat(go): phase 4 — Backend/Lock interfaces, flock + redis impls, config (`59be6ea`)
- feat(sdk): phase 3 — framework-agnostic TypeScript SDK runtime (`8704af2`)
- feat(auth, headers): phase 2 — HMAC-SHA256 sign/verify (`6e310dd`)
- feat(spec, sdk, manifest): phase 1 — manifest schema, parsers, conformance (`181c938`)

### Bug Fixes

- fix(lint): move exclude-dirs under linters.exclusions.paths (v2 schema) (`b86a256`)
- fix(ci): bump golangci-lint-action to v7; close keep-alive conns in express test (`a294129`)
- fix(ci): redis lock deadline race, goreleaser hook syntax, pnpm + go versions (`b9a746e`)

### Docs

- docs(rfc): populate §CLI section (missed in phase 6 commit) (`5708644`)

### Chores

- chore: rename npm package @cronix/sdk → @awbx/cronix-sdk (`b24b44a`)
- chore(lint): apply gofmt -s, pare linter set to v1-equivalent for pre-alpha (`edfee97`)
- chore: restructure into polyglot monorepo (spec/ + ts/ + go/) (`8ded0d8`)
- chore: phase 0 — repository scaffolding (`9396bfd`)

### Other

- ci(release): temporary diagnostic for docker secret shape (`a5a989c`)
- ci(release): publish to Docker Hub + npm on tag (`82c04eb`)
