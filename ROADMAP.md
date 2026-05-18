# Roadmap

cronix is "Terraform for cron." The application's manifest at
`/.well-known/cron-manifest` is the source of truth. `cronix plan` shows the
diff against whatever scheduler the host runs (`crontab`, `systemd-timer`,
Kubernetes, AWS EventBridge Scheduler, Vercel Cron). `cronix apply` reconciles
it. The host scheduler does the firing. There is no central daemon and no
state file — the backend's own native entries are the state, marked as owned
by cronix (D-026).

## Mental model

| Terraform | cronix |
|---|---|
| HCL config | manifest at `/.well-known/cron-manifest` |
| Providers | backends (compiled-in for v1) |
| `terraform plan` | `cronix plan` (per-backend diff) |
| `terraform apply` | `cronix apply` |
| `terraform state` | distributed — backend-native entries marked as owned |
| `terraform import` | drift detection + adopt-existing flow |
| `terraform destroy` | `cronix prune` |
| Workspaces | per-app, per-backend manifests |
| Registry | — *(plugin protocol is a v2 topic)* |

The protocol is the product. The Go reconciler, the `cronix trigger` shim,
and the TypeScript SDK are reference implementations. Conformance vectors at
`spec/manifest-vectors.json` and `spec/auth-vectors.json` are the
cross-implementation correctness contract.

## Milestones

### v1.0.0-rc.1 — release candidate (target 2026-06-16)

Gate to v1.0.0. The on-the-wire contract is already frozen; this milestone
hardens the operator and supply-chain story.

- Signed releases: cosign signatures + SLSA provenance on every GoReleaser
  artifact
- SBOM (syft) attached to every release
- Multi-arch container images verified (`linux/amd64`, `linux/arm64`)
- Conformance suite extracted to `spec/conformance/` with a language-neutral
  runner, so a future Rust/Python/Java SDK passes the same bytes
- OpenTelemetry trace spec for `cronix trigger` — one span per fire, child
  spans per HTTP attempt
- Production runbook in `docs-site/` — failure modes, on-call playbook,
  recommended dashboards
- Helm chart hardening — PodSecurityStandards restricted, NetworkPolicy,
  RBAC audit
- Backend matrix in CI — real `k3d` for `kubernetes`, real `systemd`-in-
  container for `systemd-timer`, LocalStack for `aws-scheduler`
- `cronix adopt` — first-class flow to take ownership of an existing
  crontab line, systemd unit, or `CronJob`
- All RFC `D-NNN` decisions reflected in code; CI fails on Zod ↔ JSON Schema
  drift (already enforced; expand to header constants and decision tables)

### v1.0.0 — GA (target 2026-07-14)

- Two weeks of rc.1 in the wild without contract-breaking issues
- At least three named adopters in `ADOPTERS.md`
- License decision finalized: stay MIT or relicense to Apache 2.0 for CNCF
  patent-grant alignment (see [governance](./GOVERNANCE.md))
- All five v1 backends pass the conformance + integration matrix
- 1.0 release notes call out the on-the-wire stability guarantee (D-002:
  `version: 1` is the contract; v2 will introduce `version: 2` alongside
  it, not replace it)

### v1.1 — first post-GA (target 2026-10-06)

- Drift watch (`cronix drift --watch`) — long-running drift monitor for
  GitOps and on-call dashboards
- `cronix import` from competing tools — sucrose imports for serverless
  framework, SAM, Pulumi CronJob resources
- Go SDK promoted from signature-verify-only to "full" — manifest builder
  parity with TypeScript, conformance-vector tested
- Sixth backend candidate (Cloudflare Cron Triggers or GitHub Actions
  `schedule`)
- Web dashboard *out of scope* — see Non-goals in `spec/RFC.md`

### v2.0 — plugin protocol (no firm date)

- Out-of-tree backend protocol (gRPC over local socket, Terraform-plugin-
  shaped). Compiled-in backends remain the supported core.
- Manifest schema `version: 2` — additive only; v1 manifests continue to
  reconcile.
- HA reconciler mode (leader election; useful for organizations that don't
  want to serialize through CI).
- One-shot `run-at` job type for fan-out batch workflows.

## How to influence the roadmap

- File an issue with the `kind/v1-readiness` label for v1 gap items.
- Spec-shaped questions start as `## Q-NNN:` in
  [`spec/OPEN_QUESTIONS.md`](./spec/OPEN_QUESTIONS.md) and promote to
  `## D-NNN:` in [`spec/DECISIONS.md`](./spec/DECISIONS.md) once locked.
  See [`CONTRIBUTING.md`](./CONTRIBUTING.md).
- Real-world adoption beats theoretical priorities. Add your org to
  [`ADOPTERS.md`](./ADOPTERS.md) and the issues you care about jump the
  queue.

## CNCF

cronix intends to submit to the [CNCF Sandbox](https://www.cncf.io/sandbox-projects/)
once v1.0.0 ships. The sandbox application narrative lives at
[`docs/cncf-sandbox-application.md`](./docs/cncf-sandbox-application.md).
