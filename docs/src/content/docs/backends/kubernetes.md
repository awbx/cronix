---
title: Kubernetes backend
description: Reconcile against CronJob + ConfigMap pairs in any Kubernetes cluster.
---

:::note[Status]
Stable as of v0.3.0. `cronix apply --backend kubernetes` reconciles directly against the API server via `client-go`. Render-only YAML output is still available for operators who prefer `kubectl apply -f`.
:::

## Layout

Per (app, job, schedule-index), cronix manages two resources:

- a `ConfigMap` named `cronix-<app>-<job>-<idx>-spec` containing the per-job spec JSON the trigger shim reads at fire time.
- a `CronJob` named `cronix-<app>-<job>-<idx>` that mounts the ConfigMap and invokes `cronix trigger <app>.<job>`.

Both resources carry the labels:

```yaml
cronix.dev/managed: "true"
cronix.dev/app: <app>
cronix.dev/job: <job>
cronix.dev/index: "<idx>"
cronix.dev/hash: <hash>
```

cronix uses these labels to enumerate owned resources. Resources without `cronix.dev/managed=true` are never modified or deleted.

## Reconciling a manifest

```bash
cronix apply \
  --manifest ./billing.cronix.json \
  --backend kubernetes \
  --k8s-namespace billing \
  --k8s-image awbx/cronix:v0.7.2
```

Out-of-cluster runs use `--kubeconfig <path>` (or `KUBECONFIG` env / `~/.kube/config`). When running cronix itself inside a pod, pass `--in-cluster`.

`cronix list`, `cronix plan`, `cronix drift`, `cronix prune`, and `cronix show` all accept the same backend flags.

## Rendering by hand

Operators preferring GitOps-style YAML can still render the resources without instantiating a Backend:

```go
import "github.com/awbx/cronix/go/internal/backends/kubernetes"

yaml, err := kubernetes.RenderManifest(
    "awbx/cronix:v0.7.2",
    "billing",          // namespace
    "billing",          // app
    job,                // a manifest.NormalizedJob
    0,                  // schedule index
    "abc123def4567890", // hash
    specJSON,           // the trigger spec, embedded as ConfigMap data
)
```

```bash
echo "$yaml" | kubectl apply -f -
```

## Image

`awbx/cronix:<version>` is `FROM gcr.io/distroless/static`, ~20 MB, multi-arch (amd64/arm64). Built and pushed by goreleaser to both DockerHub and GHCR on every `v*` tag.

## Concurrency

The CronJob spec sets `concurrencyPolicy: Forbid` and `backoffLimit: 0` — defense in depth. The shim is still the authoritative concurrency enforcer (D-028); K8s preventing duplicate Pods catches misconfigurations early.

## Run history

```bash
kubectl -n billing get jobs -l cronix.dev/job=reconcile --sort-by=.status.startTime
kubectl -n billing logs -l cronix.dev/job=reconcile --max-log-requests 50
```

K8s `Events` on the CronJob carry skip records when `concurrencyPolicy: Forbid` fires.

## Limitations

- K8s does not support `@every <duration>` natively; cronix translates the supported shortcuts (`@hourly`, `@daily`, etc.) but rejects `@every` at validate time. Use a 5-field cron expression instead.
- K8s name length: `cronix-<app>-<job>-<idx>` must be ≤ 63 characters total. cronix Validates the (app, job) name combination at apply time.
- `Update` is implemented as Delete+Create rather than Server-Side Apply, so there's a sub-second gap where a given (CronJob, ConfigMap) pair does not exist. Acceptable in CI deploy contexts.
