# CNCF Sandbox application — narrative draft

This is a working draft of the narrative cronix will submit to the
[CNCF Sandbox](https://www.cncf.io/sandbox-projects/) process once
v1.0.0 ships. It is not the application form itself — that lives at
[github.com/cncf/sandbox](https://github.com/cncf/sandbox) and is filed
as an issue against that repository.

## Name

cronix

## Description

cronix is a declarative reconciler for scheduled jobs. An application
declares its schedules in a JSON manifest served at
`/.well-known/cron-manifest`. The cronix CLI reconciles that manifest
against the host's native scheduler (`crontab`, `systemd-timer`,
Kubernetes `CronJob`, AWS EventBridge Scheduler, or Vercel Cron). The
host scheduler does the firing. A small shim, `cronix trigger`, runs at
each fire and handles HMAC signing, concurrency locks, timeouts, and
retries on the application's behalf.

There is no central daemon, no state file, and no shared coordinator.
The backend's own entries are the state — cronix marks the ones it owns
and never touches the rest.

## Sponsor / TAG

cronix sits under [TAG App-Delivery](https://tag-app-delivery.cncf.io/).
The reconciler pattern, the GitOps fit, and the explicit "no central
state" choice are the same primitives Argo, Flux, and Crossplane bring
to other domains, applied to scheduled work.

## Statement on alignment with the CNCF mission

CNCF defines cloud-native as "scalable applications in modern, dynamic
environments using containerized, declaratively-described, and
loosely-coupled systems."

cronix is declarative (the manifest is the contract), loosely coupled
(no shared scheduler; each backend is a translation of the same
manifest), and explicitly designed for dynamic environments (Kubernetes,
serverless schedulers, ephemeral hosts). Today the cluster has Argo for
deploys, Prometheus for metrics, cert-manager for certificates — but
"when does this job run, and is it firing?" is still answered with
hand-edited YAML or an EventBridge console. cronix is the missing piece.

## Why a new project rather than a feature of an existing one

The closest CNCF projects are:

- [Argo Workflows](https://argoproj.github.io/workflows/) — workflow
  engine, Kubernetes-only, DAG-shaped, requires a controller.
- [Volcano](https://volcano.sh/) — batch scheduling on Kubernetes;
  different problem (resource-aware queueing, not cross-platform cron).

Neither addresses the cross-backend, no-daemon, no-state-file reconciler
shape that cronix takes. cronix is *complementary* — an Argo Workflows
user could schedule the *start* of a workflow via cronix, with the
manifest in the same repo as the workflow.

## License

Apache License 2.0 (see [`LICENSE`](../LICENSE)). Releases prior to
v1.0.0 were distributed under the MIT License; the historical text is
preserved at [`LICENSE-MIT`](../LICENSE-MIT). The relicensing decision
is recorded as `D-036` in [`spec/DECISIONS.md`](../spec/DECISIONS.md).

The relicense was costless: at the time of the decision the project
was sole-authored (verifiable via `git shortlog -sne`), so there was
no external copyright holder to obtain sign-off from. Apache 2.0 was
chosen for its explicit patent grant (§3), which CNCF prefers for
incubation and graduation stages.

## Source control

- Repository: https://github.com/awbx/cronix
- Default branch: `main`
- DCO sign-off enforced on every commit
- Conventional Commits for changelog generation

## Maintainership

See [`MAINTAINERS.md`](../MAINTAINERS.md). The project today has a single
active maintainer. Sandbox criteria explicitly accept single-maintainer
projects; growing to ≥2 maintainers from ≥2 organizations is a prerequisite
for the Incubation graduation step, not Sandbox entry.

The plan to add maintainers:

1. Open `kind/v1-readiness` and `good first issue` work to attract
   recurring contributors.
2. Promote consistent contributors per the criteria in `GOVERNANCE.md`.
3. Target ≥2 maintainers from ≥2 organizations within 12 months of GA.

## Adoption

See [`ADOPTERS.md`](../ADOPTERS.md). Sandbox does not require named
adopters but they are heavily weighted by the TOC. The roadmap tracks an
"≥3 named adopters" gate before v1.0.0 GA.

## Governance

See [`GOVERNANCE.md`](../GOVERNANCE.md). Lazy consensus, two-maintainer
sign-off for spec changes, `Q-NNN` → `D-NNN` decision log, public
roadmap.

## Code of Conduct

See [`CODE_OF_CONDUCT.md`](../CODE_OF_CONDUCT.md). Contributor Covenant 2.1.

## Security

See [`SECURITY.md`](../SECURITY.md). Private disclosure via GitHub
Security Advisories or email. Threat model documented in `spec/RFC.md`
under §Security.

## Differentiating attributes

1. **No central scheduler.** The host scheduler does the firing.
   Operators do not run new infrastructure.
2. **No state file.** The backend's own entries are the state.
3. **Frozen wire contract.** `version: 1` manifests will reconcile in
   every future release. Breaking changes ship as `version: 2`
   alongside, not as replacements.
4. **Conformance vectors as a portability primitive.** A new SDK in any
   language passes `spec/manifest-vectors.json` and
   `spec/auth-vectors.json` byte-for-byte before it ships.
5. **Five reference backends shipping.** crontab, systemd-timer,
   Kubernetes, AWS EventBridge Scheduler, Vercel Cron.

## Pre-submission checklist (internal — not for the application)

- [ ] v1.0.0 released
- [ ] CODE_OF_CONDUCT.md, GOVERNANCE.md, MAINTAINERS.md, ADOPTERS.md, ROADMAP.md committed
- [ ] DCO check active on every PR
- [ ] At least three named adopters in ADOPTERS.md
- [x] Apache-2.0 relicense decision made (relicensed; `D-036`)
- [ ] Public talk or written piece introducing cronix to a wider
      audience (KubeCon CFP, blog cross-post, podcast)
- [ ] TAG App-Delivery introduction call requested
- [ ] [github.com/cncf/sandbox](https://github.com/cncf/sandbox) issue
      filed with this narrative

## Application URL

The actual application is filed as an issue at:
https://github.com/cncf/sandbox

Filing format is documented in that repo's README. The narrative above
maps to the form's "Description", "Statement on alignment", and
"Differentiating attributes" fields.
