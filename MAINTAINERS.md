# Maintainers

This is the list of cronix maintainers. See [`GOVERNANCE.md`](./GOVERNANCE.md)
for what maintainership means and how to become one.

| Name | GitHub | Affiliation | Areas |
|---|---|---|---|
| Abdelhadi Sabani | [@awbx](https://github.com/awbx) | Independent | spec, Go reconciler, TS SDK, all backends |

## Emeritus

*(none yet)*

## Areas

For larger projects this list will grow to multiple specialists. Today
all areas are owned by all maintainers.

- **spec** — RFC, decisions, conformance vectors
- **reconciler** — Go binary, `cronix apply` / `plan` / `drift`
- **trigger** — `cronix trigger` shim, HMAC, locks, retries
- **backend/crontab**
- **backend/systemd-timer**
- **backend/kubernetes**
- **backend/aws-scheduler**
- **backend/vercel**
- **sdk/ts** — `@awbx/cronix-sdk` and framework adapters
- **sdk/go** — `pkg/cronsdk`
- **deploy** — Helm chart, Dockerfile, install scripts
- **docs** — `docs-site/`, README, RFC
