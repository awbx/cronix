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
| `image.repository` | `awbx/cronix` | Docker Hub image |
| `image.tag` | `""` | Falls back to `.Chart.AppVersion` |
| `manifestUrl` | `""` | When set, a CronJob runs `cronix apply` on `applySchedule` |
| `secretRefs` | `[]` | Each entry is `env:NAME` / `file:/path` / `raw:literal` for HMAC + manifest fetch |
| `applySchedule` | `*/5 * * * *` | Reconcile cadence |
| `env` | `[]` | Extra env vars on the reconciler container |
| `rbac.create` | `true` | Set false if you provision your own `Role`/`RoleBinding` |
| `serviceAccount.create` | `true` | Set false to bind to a pre-existing SA |
| `serviceAccount.name` | `""` | When `create=false`, the SA name to bind in the RoleBinding |

## What it does NOT do

- It does **not** declare individual cron jobs — those live in the application's manifest (served at `/.well-known/cron-manifest` by `@awbx/cronix-sdk`). The chart just runs the reconciler that turns that manifest into K8s resources.
- It does **not** create the HMAC `Secret`. Provide one (`kubectl create secret generic cron-secret --from-literal=CRON_SECRET=...`) and reference it via `env.<name>.valueFrom.secretKeyRef` in `env`, then point `secretRefs` at `env:CRON_SECRET`.
- It does **not** install the trigger image into a per-app namespace — `cronix apply` does that itself when reconciling.

## Uninstall

```bash
helm uninstall billing-cronix -n billing
```

Helm removes the SA/RBAC/CronJob; cronix-managed `CronJob`s and `ConfigMap`s in the namespace remain. Run `cronix prune --backend kubernetes --k8s-namespace billing --yes` first if you want them gone too.
