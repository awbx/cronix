# Backend: kubernetes

> **Status (v1):** YAML rendering and `Validate` ship in this release. Live `client-go` reconciliation (`List`/`Create`/`Update`/`Delete` against the K8s API) lands in a follow-up phase — see PLAN.md §5d. Operators using K8s today can render YAML via the SDK and apply with `kubectl apply -f`.

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

cronix uses these labels to enumerate owned resources. Resources without `cronix.dev/managed=true` are never modified.

## Rendering by hand (v1 fallback)

```go
import "github.com/awbx/cronix/go/internal/backends/kubernetes"

yaml, err := kubernetes.RenderManifest(
    "ghcr.io/awbx/cronix:v0.1.0",
    "billing",          // namespace
    "billing",          // app
    job,                // a manifest.NormalizedJob
    0,                  // schedule index
    "abc123def4567890", // hash
    specJSON,           // the trigger spec, embedded as ConfigMap data
)
```

Then:

```bash
echo "$yaml" | kubectl apply -f -
```

## Image

The cronix image (`ghcr.io/awbx/cronix:<version>`) is `FROM gcr.io/distroless/static`, ~20 MB, multi-arch (amd64/arm64). Built and pushed by the GoReleaser release workflow.

## Concurrency

The CronJob spec sets `concurrencyPolicy: Forbid` and `backoffLimit: 0` — defense in depth. The shim is still the authoritative concurrency enforcer (D-028), but K8s preventing duplicate Pods catches misconfigurations early.

## Run history

`kubectl get jobs -l cronix.dev/job=<job> --sort-by=.status.startTime` lists Job objects owned by the CronJob; `kubectl logs -l cronix.dev/job=<job>` aggregates Pod logs. K8s `Events` carry skip records when `concurrencyPolicy: Forbid` triggers.

## Helm

A pre-alpha Helm chart lives at `deploy/helm/cronix/`. It does **not** model individual jobs — operators apply rendered CronJob YAML separately. The chart provisions the cronix image, ServiceAccount, RBAC rules, and (optionally) a Job that runs `cronix apply` against an in-cluster manifest URL on a schedule.

## Limitations

- K8s does not support `@every <duration>` natively; cronix translates the supported shortcuts (`@hourly`, `@daily`, etc.) but rejects `@every` at validate time. Use a 5-field cron expression instead.
- K8s name length: `cronix-<app>-<job>-<idx>` must be ≤ 63 characters total. cronix Validates the (app, job) name combination at apply time.
