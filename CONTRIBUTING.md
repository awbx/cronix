# Contributing to cronix

Thanks for taking the time. cronix is pre-alpha; the surface is moving and contributions land best when they preempt churn rather than fight it.

## Ground rules

- **The RFC is the product.** `spec/RFC.md` is authoritative. Code follows the RFC, not the other way around. New behavior starts as a `## Q-NNN:` entry in `spec/OPEN_QUESTIONS.md`, gets discussed, then promotes to a `## D-NNN:` decision before code lands.
- **Conformance vectors are sacred.** `spec/manifest-vectors.json` and `spec/auth-vectors.json` are the cross-language correctness contract. Adding or modifying a vector is a spec change and requires an RFC update.
- **Both languages stay in lock-step.** Any change to manifest shape, header format, or signing scheme must land in TypeScript (`@cronix/sdk`) and Go (`internal/manifest`, `internal/auth`) in the same PR, with both passing the shared vectors.

## Repo layout

```
spec/    language-neutral RFC, decisions, JSON Schema, conformance vectors
ts/      TypeScript pnpm workspace (@cronix/sdk + examples)
go/      Go module github.com/awbx/cronix/go (cmd, internal, pkg)
deploy/  Dockerfile, Helm chart
```

## Local dev

```bash
# TypeScript
cd ts
pnpm install
pnpm build && pnpm test && pnpm lint && pnpm typecheck

# Go
cd go
go build ./...
go test ./...
go vet ./...

# Multi-platform binaries (snapshot only)
goreleaser build --snapshot --clean
```

## Pull requests

- Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, …).
- Add a changeset for any TS-package surface change: `cd ts && pnpm changeset`.
- Tests with the change. Coverage is not allowed to drop.
- For a new backend: implement the `Backend` interface, pass the reconciler integration tests, and submit a fidelity-matrix update for §Backend Fidelity Matrix in RFC.
- For a new SDK in another language: pass `spec/manifest-vectors.json` and `spec/auth-vectors.json` byte-for-byte, plus the §SDK Contract scenario set.

## Reporting bugs

File an issue with:

- The minimal repro (manifest snippet, command line, expected vs. actual).
- Your platform (`cronix version`).
- For backend bugs, the relevant fragment of the host scheduler (crontab line, `systemctl status` output, `kubectl get cronjob -o yaml`).

## Reporting security vulnerabilities

See [SECURITY.md](./SECURITY.md). Don't file a public issue.
