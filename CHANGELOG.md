# Changelog

All notable changes to cronix are documented here. Generated from `git log`; see scripts/generate-changelog.mjs.

## [0.8.0] - 2026-05-07

### Features

- feat(trigger): Replace concurrency policy actually replaces (`d8124d7`)

### Docs

- docs(spec): align RFC with shipped backend reality (`8c30764`)

## [0.7.4] - 2026-05-07

### Features

- feat(sdk): extension points — skipVerify, hooks, custom error response, standalone verify utils (`38828d5`)

### Refactors

- Update README with user attachments link (`a2ad111`)

### Docs

- docs(landing): swap video URL + minor copy polish (`eff97b2`)
- docs: full CLI / SDK / Concepts coverage + scrub internal references (`9702361`)

### Chores

- chore: remove internal planning doc (`668cc3e`)
- chore: update docs (`7b2469b`)

### Other

- spec: scrub planning-phase references, rewrite changelog (`c21c79c`)
- Change asset link in README (`de0b8fa`)
- ci: opt every workflow into Node 24 for JS-based actions (`b02e6dc`)
- ci: cancel in-progress run on same ref to save runner minutes (`c3825a8`)

## [0.7.3] - 2026-05-05

### Docs

- docs(landing): code-first hero, feature grid, install tabs, backend cards (`e2a526a`)
- docs: scaffold Astro Starlight site under docs/ + GitHub Pages deploy (`504c0c4`)
- docs(readme): refocus on OSS positioning, install paths, and examples (`05675ee`)

### Chores

- chore(changelog): regenerate after history rewrite (`d386751`)

### Other

- ci: skip CI on docs-only changes (`ce7095e`)
- ci(docs): pin pnpm@10.33.0 in docs/package.json (`2f2ef11`)

## [0.7.2] - 2026-05-05

### Other

- ci(release): generate Homebrew formula and push to awbx/homebrew-cronix tap (`2acee2a`)

## [0.7.1] - 2026-05-05

### Refactors

- refactor(release): move bump-version + changelog tooling to repo root scripts/ (`51bed5a`)

### Other

- ci(release): authenticate GHCR push with GHCR_PAT (`22763b9`)

## [0.7.0] - 2026-05-05

### Features

- feat(cli): global-status aggregates owned entries across configured backends (`e474d37`)

### Chores

- chore(example): hono-app declares three scheduled jobs (`f949acf`)

## [0.6.1] - 2026-05-05

### Refactors

- refactor(go): split aws + crontab into per-concern files (`06b130c`)
- refactor(go): extract internal/policy + split systemd into per-concern files (`e220a53`)

### Chores

- chore(release): pnpm version:bump + auto-generated CHANGELOG.md (`2950cec`)

## [0.6.0] - 2026-05-05

### Features

- feat(sdk): split framework adapters into sibling packages — v0.6.0 (`0e04b00`)
- feat(aws): cronix-trigger Lambda shim + inline-spec EventBridge Input (`5d57133`)
- feat(aws): EventBridge Scheduler backend (cloud abstraction starts here) (`67c16b8`)

## [0.5.1] - 2026-05-04

### Features

- feat(release): goreleaser nfpms — deb / rpm / apk packages on every tag (`366a460`)

### Bug Fixes

- fix(backends): drift detection catches manual schedule edits (k8s + systemd) (`895c881`)

### Docs

- docs: README status + install snippet → v0.5.0 (`2bf16e7`)

## [0.5.0] - 2026-05-04

### Features

- feat(crontab,init): crontab History via journalctl + cronix init scaffold (`7ec6aef`)

### Docs

- docs(cli): drop TODO marker from history --help — both backends wired (`4110f91`)
- docs: bump version examples to v0.4.0 in README + install.sh (`972a582`)

### Chores

- chore(helm): refresh chart for v0.4.0 — image, in-cluster wiring, README (`6b6f286`)

## [0.4.0] - 2026-05-04

### Features

- feat(k8s,history): cronix history --backend kubernetes via pod logs (`2fe6088`)
- feat(systemd,cli): cronix history backed by journalctl (`8fd1fe9`)
- feat(cli): cronix show <app>.<job> for backend inspection + drift (`9e4f271`)

### Bug Fixes

- fix: gofmt internal/cli/commands/show.go (struct field alignment) (`3d576f1`)

### Docs

- docs: refresh kubernetes/systemd guides for live backends + README status (`4f7b0af`)

## [0.3.0] - 2026-05-04

### Features

- feat(cli): cronix prune — operator uninstall command (`4668f9d`)
- feat(systemd): full timer/service lifecycle with daemon-reload + enable (`2bde33a`)
- feat(k8s): wire client-go for full apply/drift/list lifecycle (`9acdd50`)

### Bug Fixes

- fix(k8s): satisfy staticcheck QF1001 + QF1008 in backend (`0886e64`)

## [0.2.0] - 2026-05-04

### Features

- feat(sdk): nest and koa adapters (`3483439`)
- feat(sdk): framework adapters as subpath exports (express, fastify, vercel) (`c03ebd3`)
- feat(sdk): merge default vars from createCron with per-fire vars (`ca83131`)
- feat(sdk): typed env (Bindings) and var (Variables), Hono-style generic (`4d4ff1b`)
- feat(sdk): three-tier integration API; keep registry/dispatch, add ergonomics (`0478c12`)
- feat: install.sh — one-liner installer for the cronix CLI (`f01e312`)

### Bug Fixes

- fix(examples): drop stray verify import + biome format on hono index (`5e0a95e`)
- fix(apply): remove orphan spec files when a job is deleted from the manifest (`672ae7b`)
- fix(examples): declare auth.secret_refs in registered jobs (`6066d73`)

### Refactors

- refactor(sdk): adapters expose handle(fn) instead of mount(app, cron) (`9719ff0`)

### Docs

- docs(examples): use cron.handle() in express + fastify too (`53a11c1`)

## [0.1.1] - 2026-05-04

### Other

- ci(release): remove temporary docker secret diagnostic (`6246efb`)

## [0.1.0] - 2026-05-04

### Features

- feat(polish, sdk-go, docs, helm): phase 7 — release candidate (`d0b9059`)
- feat(cli): phase 6 — validate, plan, apply, list, drift, completion (`5815db0`)
- feat(go): phase 5 — trigger shim, crontab backend, reconciler, systemd/k8s skeletons (`60475c9`)
- feat(go): phase 4 — Backend/Lock interfaces, flock + redis impls, config (`87ae57a`)
- feat(sdk): phase 3 — framework-agnostic TypeScript SDK runtime (`ffc602d`)
- feat(auth, headers): phase 2 — HMAC-SHA256 sign/verify (`06a6c4e`)
- feat(spec, sdk, manifest): phase 1 — manifest schema, parsers, conformance (`0298815`)

### Bug Fixes

- fix(lint): move exclude-dirs under linters.exclusions.paths (v2 schema) (`0d7f761`)
- fix(ci): bump golangci-lint-action to v7; close keep-alive conns in express test (`ea3047d`)
- fix(ci): redis lock deadline race, goreleaser hook syntax, pnpm + go versions (`c2c600d`)

### Docs

- docs(rfc): populate §CLI section (missed in phase 6 commit) (`5a811ab`)

### Chores

- chore: rename npm package @cronix/sdk → @awbx/cronix-sdk (`fb99d05`)
- chore(lint): apply gofmt -s, pare linter set to v1-equivalent for pre-alpha (`30e130c`)
- chore: restructure into polyglot monorepo (spec/ + ts/ + go/) (`4a060e9`)
- chore: phase 0 — repository scaffolding (`c758489`)

### Other

- ci(release): temporary diagnostic for docker secret shape (`b60ec32`)
- ci(release): publish to Docker Hub + npm on tag (`0aca005`)
