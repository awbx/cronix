# cronix Helm chart

Runs `cronix apply --backend kubernetes --in-cluster` on a schedule against an HTTPS manifest endpoint, reconciling its cron jobs as namespace-scoped `CronJob` + `ConfigMap` pairs.

The chart provisions a `ServiceAccount` + `Role` + `RoleBinding` granting the SA the verbs cronix needs (`cronjobs`/`jobs`/`configmaps`/`pods`/`pods/log` CRUD in its namespace), plus an optional `CronJob` that runs the reconciler itself.

## Install

```bash
helm install billing-cronix ./deploy/helm/cronix \
  --namespace billing --create-namespace \
  --set manifestUrl=https://billing.example.com/.well-known/cron-manifest \
  --set 'secretRefs={env:CRON_SECRET}' \
  --set applySchedule='*/5 * * * *'
```

The reconciler `CronJob` is created only when `manifestUrl` is set; without it, the chart provisions just the SA + RBAC and you can run `cronix apply` from your CI pipeline against the cluster.

## Values

| Key | Default | Notes |
|---|---|---|
| `image.repository` | `awbx/cronix` | Multi-arch image — pulls the right variant per node |
| `image.tag` | `""` | Falls back to `.Chart.AppVersion` |
| `image.pullPolicy` | `IfNotPresent` | |
| `manifestUrl` | `""` | When set, a CronJob runs `cronix apply` on `applySchedule` |
| `secretRefs` | `[]` | Each entry is `env:NAME` / `file:/path` / `raw:literal` for HMAC + manifest fetch |
| `applySchedule` | `*/5 * * * *` | Reconcile cadence |
| `env` | `[]` | Extra env vars on the reconciler container |
| `rbac.create` | `true` | Set false if you provision your own `Role`/`RoleBinding` |
| `serviceAccount.create` | `true` | Set false to bind to a pre-existing SA |
| `serviceAccount.name` | `""` | When `create=false`, the SA name to bind in the RoleBinding |
| `podSecurityContext` | PSS-restricted | runAsNonRoot + 65532 + RuntimeDefault seccomp — see §Hardening |
| `securityContext` | PSS-restricted | allowPrivilegeEscalation: false + drop ALL caps + readOnlyRootFilesystem |
| `networkPolicy.enabled` | `false` | When true, restricts egress to DNS + K8s API + `egress.extra` |
| `networkPolicy.egress.extra` | `[]` | Additional NetworkPolicy egress rules — typically the manifest URL host |
| `resources.requests.cpu/memory` | `50m / 64Mi` | |
| `resources.limits.cpu/memory` | `200m / 256Mi` | |

## Hardening

The chart deploys cleanly on clusters with [PodSecurityStandards](https://kubernetes.io/docs/concepts/security/pod-security-standards/) `restricted` enforcement — no values overrides required. Each safeguard can be loosened individually, but you should have a verified reason before doing so.

### What's enforced by default

| Safeguard | Where | Disable by |
|---|---|---|
| Pod runs as UID 65532 (`nonroot` from distroless) | `podSecurityContext.runAsUser` | overriding to a different UID |
| `runAsNonRoot: true` | `podSecurityContext` | setting to `false` (PSS will reject) |
| `seccompProfile: RuntimeDefault` | `podSecurityContext.seccompProfile` | setting `type: Unconfined` |
| `allowPrivilegeEscalation: false` | container `securityContext` | setting to `true` |
| All capabilities dropped | `securityContext.capabilities.drop: [ALL]` | adding to a `securityContext.capabilities.add` list |
| Read-only root filesystem (with `/tmp` emptyDir) | `securityContext.readOnlyRootFilesystem` | setting to `false` |

The container needs almost none of the Linux capabilities to do its job: it dials TLS to the manifest URL, calls the K8s API, writes CronJobs. The capability drop is safe; the read-only rootfs is safe because the only writable surface a Go binary typically needs is `/tmp`, which the chart mounts as an `emptyDir`.

### NetworkPolicy

Off by default (a NetworkPolicy without a controller silently does nothing — enabling it on a cluster without Calico/Cilium/Antrea/etc. gives false confidence). When enabled, the policy:

- Denies all ingress (the apply CronJob accepts no incoming traffic).
- Allows egress to DNS (UDP/TCP 53, cluster-wide).
- Allows egress to the K8s API server (port 443 in `default/kubernetes`).
- Allows whatever else you put in `networkPolicy.egress.extra`. Add a rule for the namespace and port that serves your manifest URL.

Example for an in-cluster manifest endpoint:

```yaml
networkPolicy:
  enabled: true
  egress:
    extra:
      - to:
          - namespaceSelector:
              matchLabels:
                kubernetes.io/metadata.name: billing
        ports:
          - port: 443
            protocol: TCP
```

For an external manifest endpoint (e.g., `https://billing.example.com/.well-known/cron-manifest`), you typically allow egress to `0.0.0.0/0` on 443 since IP-based egress rules are fragile against DNS rotation. Reach for service-mesh / egress-gateway tooling if you need stricter outbound control.

### RBAC

Tightened from earlier chart versions:

| Resource | Verbs |
|---|---|
| `batch/cronjobs` | get, list, watch, create, update, patch, delete |
| `batch/jobs` | get, list, watch, delete *(read + prune only — Jobs are created by the CronJob controller, not by cronix)* |
| `configmaps` | get, list, watch, create, update, patch, delete |
| `pods`, `pods/log`, `events` | get, list, watch *(read-only — for `cronix history`)* |
| `secrets` | **none** — cronix resolves secret refs locally via env/file/raw, not via the API |

If you want to use cronix to manage Secrets too (out of scope today, but plausible in a future release), grant the verbs explicitly via your own additional Role.



## Architecture

The chart pulls `awbx/cronix:<AppVersion>` from Docker Hub (or `ghcr.io/awbx/cronix:<AppVersion>` if you override `image.repository`). Both registries serve a **multi-arch manifest list** covering `linux/amd64` and `linux/arm64`; the cluster's container runtime picks the right variant per node. Mixed-arch clusters (an x86_64 control plane with arm64 workers, for example) require no chart changes — every Pod that lands on an arm64 node pulls the arm64 image transparently.

If you need to pin a specific arch (rare), override `image.repository` to the per-arch tag explicitly, e.g. `awbx/cronix` + `image.tag: 0.10.2-arm64`. Per-arch tags carry the same cosign signature and SBOM attestation as the manifest-list pointer.

## What it does NOT do

- It does **not** declare individual cron jobs — those live in the application's manifest (served at `/.well-known/cron-manifest` by `@awbx/cronix-sdk`). The chart just runs the reconciler that turns that manifest into K8s resources.
- It does **not** create the HMAC `Secret`. Provide one (`kubectl create secret generic cron-secret --from-literal=CRON_SECRET=...`) and reference it via `env.<name>.valueFrom.secretKeyRef` in `env`, then point `secretRefs` at `env:CRON_SECRET`.
- It does **not** install the trigger image into a per-app namespace — `cronix apply` does that itself when reconciling.

## Uninstall

```bash
helm uninstall billing-cronix -n billing
```

Helm removes the SA/RBAC/CronJob; cronix-managed `CronJob`s and `ConfigMap`s in the namespace remain. Run `cronix prune --backend kubernetes --k8s-namespace billing --yes` first if you want them gone too.
