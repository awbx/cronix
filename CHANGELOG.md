# Changelog

All notable changes to cronix are documented here. Generated from `git log`; see scripts/generate-changelog.mjs.

## [0.12.0] - 2026-05-22

### Features

- feat(reconciler): cronix adopt for crontab backend (#11) (#44) (`a07f405`)

## [0.11.0] - 2026-05-20

### Features

- feat(trigger): --otel flag emits D-037 trace shape (#37) (#39) (`f0fa501`)
- feat(helm): PSS-restricted, NetworkPolicy, RBAC trim (#9) (#36) (`d69b043`)

### Docs

- docs: production runbook (#8) (#35) (`089fc04`)
- docs: state management + backend coverage strategy (#19, #22) (#34) (`21abc36`)

### Other

- spec: OpenTelemetry trace shape for cronix trigger (D-037, #7) (#38) (`e44ca3c`)

## [0.10.3] - 2026-05-19

### Features

- feat(supply-chain): verify multi-arch image manifests post-release (#5) (#33) (`0cf5293`)

## [0.10.2] - 2026-05-19

### Features

- feat(supply-chain): SPDX SBOM per archive + image SBOM attestation (#4) (#31) (`386c51c`)

## [0.10.1] - 2026-05-19

### Bug Fixes

- fix(supply-chain): SLSA smoke shouldn't verify checksums.txt; image tag has no v prefix (#30) (`6d7f626`)

## [0.10.0] - 2026-05-19

### Features

- feat(supply-chain): SLSA Build L3 provenance + npm provenance (#3) (#29) (`5f19835`)
- feat(supply-chain): cosign-sign release artifacts (#2) (#28) (`a7f59bd`)

### Docs

- docs: governance, roadmap, and CNCF Sandbox kit (`d0fd596`)

### Chores

- chore: relicense to Apache 2.0 (D-036) (#18) (`f99fe65`)

## [0.9.1] - 2026-05-07

### Features

- feat(cli): kubectl-style per-backend sub-subcommands for apply/plan/drift/list/show/prune/history (`9cba44f`)

### Bug Fixes

- fix(ci): unblock Windows release build + biome import sort (`341dc37`)

## [0.9.0] - 2026-05-07

### Features

- feat(vercel): declarative Vercel Cron backend — fifth v1 backend (`858436f`)

## [0.8.0] - 2026-05-07

### Features

- feat(trigger): Replace concurrency policy actually replaces (`700c40e`)

### Docs

- docs(spec): align RFC with shipped backend reality (`624102b`)

## [0.7.4] - 2026-05-07

### Features

- feat(sdk): extension points — skipVerify, hooks, custom error response, standalone verify utils (`d4bdb48`)

### Refactors

- Update README with user attachments link (`1a098b4`)

### Docs

- docs(landing): swap video URL + minor copy polish (`3cfe5ee`)
- docs: full CLI / SDK / Concepts coverage + scrub internal references (`dd90df5`)

### Chores

- chore: remove internal planning doc (`cdd20ee`)
- chore: update docs (`fe278c7`)

### Other

- spec: scrub planning-phase references, rewrite changelog (`96c03cf`)
- Change asset link in README (`1ab33ca`)
- ci: opt every workflow into Node 24 for JS-based actions (`8f5b193`)
- ci: cancel in-progress run on same ref to save runner minutes (`df68c60`)

## [0.7.3] - 2026-05-05

### Docs

- docs(landing): code-first hero, feature grid, install tabs, backend cards (`1513b58`)
- docs: scaffold Astro Starlight site under docs/ + GitHub Pages deploy (`943d580`)
- docs(readme): refocus on OSS positioning, install paths, and examples (`a420c9b`)

### Chores

- chore(changelog): regenerate after history rewrite (`bd339c9`)

### Other

- ci: skip CI on docs-only changes (`1bec357`)
- ci(docs): pin pnpm@10.33.0 in docs/package.json (`6770af2`)

## [0.7.2] - 2026-05-05

### Other

- ci(release): generate Homebrew formula and push to awbx/homebrew-cronix tap (`5e7f467`)

## [0.7.1] - 2026-05-05

### Refactors

- refactor(release): move bump-version + changelog tooling to repo root scripts/ (`97f7fae`)

### Other

- ci(release): authenticate GHCR push with GHCR_PAT (`f6a8cfb`)

## [0.7.0] - 2026-05-05

### Features

- feat(cli): global-status aggregates owned entries across configured backends (`46c9e3d`)

### Chores

- chore(example): hono-app declares three scheduled jobs (`0f93905`)

## [0.6.1] - 2026-05-05

### Refactors

- refactor(go): split aws + crontab into per-concern files (`c67822f`)
- refactor(go): extract internal/policy + split systemd into per-concern files (`84fa422`)

### Chores

- chore(release): pnpm version:bump + auto-generated CHANGELOG.md (`785f3bc`)

## [0.6.0] - 2026-05-05

### Features

- feat(sdk): split framework adapters into sibling packages — v0.6.0 (`0e3f912`)
- feat(aws): cronix-trigger Lambda shim + inline-spec EventBridge Input (`a9d11d9`)
- feat(aws): EventBridge Scheduler backend (cloud abstraction starts here) (`9d27b27`)

## [0.5.1] - 2026-05-04

### Features

- feat(release): goreleaser nfpms — deb / rpm / apk packages on every tag (`5b48dc0`)

### Bug Fixes

- fix(backends): drift detection catches manual schedule edits (k8s + systemd) (`cfaf220`)

### Docs

- docs: README status + install snippet → v0.5.0 (`990e228`)

## [0.5.0] - 2026-05-04

### Features

- feat(crontab,init): crontab History via journalctl + cronix init scaffold (`22595e3`)

### Docs

- docs(cli): drop TODO marker from history --help — both backends wired (`d51a156`)
- docs: bump version examples to v0.4.0 in README + install.sh (`c026109`)

### Chores

- chore(helm): refresh chart for v0.4.0 — image, in-cluster wiring, README (`a8878ec`)

## [0.4.0] - 2026-05-04

### Features

- feat(k8s,history): cronix history --backend kubernetes via pod logs (`377c5fe`)
- feat(systemd,cli): cronix history backed by journalctl (`2082cde`)
- feat(cli): cronix show <app>.<job> for backend inspection + drift (`e14fec7`)

### Bug Fixes

- fix: gofmt internal/cli/commands/show.go (struct field alignment) (`163303e`)

### Docs

- docs: refresh kubernetes/systemd guides for live backends + README status (`3360afc`)

## [0.3.0] - 2026-05-04

### Features

- feat(cli): cronix prune — operator uninstall command (`8d18974`)
- feat(systemd): full timer/service lifecycle with daemon-reload + enable (`c750238`)
- feat(k8s): wire client-go for full apply/drift/list lifecycle (`9d5d4d9`)

### Bug Fixes

- fix(k8s): satisfy staticcheck QF1001 + QF1008 in backend (`692aa7a`)

## [0.2.0] - 2026-05-04

### Features

- feat(sdk): nest and koa adapters (`b205c3c`)
- feat(sdk): framework adapters as subpath exports (express, fastify, vercel) (`795f4cc`)
- feat(sdk): merge default vars from createCron with per-fire vars (`691fa38`)
- feat(sdk): typed env (Bindings) and var (Variables), Hono-style generic (`d7c3bcc`)
- feat(sdk): three-tier integration API; keep registry/dispatch, add ergonomics (`4b49f72`)
- feat: install.sh — one-liner installer for the cronix CLI (`534ffdb`)

### Bug Fixes

- fix(examples): drop stray verify import + biome format on hono index (`adb2d80`)
- fix(apply): remove orphan spec files when a job is deleted from the manifest (`8c7eb78`)
- fix(examples): declare auth.secret_refs in registered jobs (`bcd308d`)

### Refactors

- refactor(sdk): adapters expose handle(fn) instead of mount(app, cron) (`1c3ac8d`)

### Docs

- docs(examples): use cron.handle() in express + fastify too (`87955d4`)

## [0.1.1] - 2026-05-04

### Other

- ci(release): remove temporary docker secret diagnostic (`0aabc7d`)

## [0.1.0] - 2026-05-04

### Features

- feat(polish, sdk-go, docs, helm): phase 7 — release candidate (`ada561f`)
- feat(cli): phase 6 — validate, plan, apply, list, drift, completion (`5815db0`)
- feat(go): phase 5 — trigger shim, crontab backend, reconciler, systemd/k8s skeletons (`60475c9`)
- feat(go): phase 4 — Backend/Lock interfaces, flock + redis impls, config (`87ae57a`)
- feat(sdk): phase 3 — framework-agnostic TypeScript SDK runtime (`ffc602d`)
- feat(auth, headers): phase 2 — HMAC-SHA256 sign/verify (`06a6c4e`)
- feat(spec, sdk, manifest): phase 1 — manifest schema, parsers, conformance (`0298815`)

### Bug Fixes

- fix(lint): move exclude-dirs under linters.exclusions.paths (v2 schema) (`dded941`)
- fix(ci): bump golangci-lint-action to v7; close keep-alive conns in express test (`d2fadb8`)
- fix(ci): redis lock deadline race, goreleaser hook syntax, pnpm + go versions (`4461759`)

### Docs

- docs(rfc): populate §CLI section (missed in phase 6 commit) (`5a811ab`)

### Chores

- chore: rename npm package @cronix/sdk → @awbx/cronix-sdk (`607721e`)
- chore(lint): apply gofmt -s, pare linter set to v1-equivalent for pre-alpha (`af57628`)
- chore: restructure into polyglot monorepo (spec/ + ts/ + go/) (`4a060e9`)
- chore: phase 0 — repository scaffolding (`c758489`)

### Other

- ci(release): temporary diagnostic for docker secret shape (`ed6b48c`)
- ci(release): publish to Docker Hub + npm on tag (`2c913b8`)
