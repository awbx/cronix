---
title: Drift detection
description: How cronix tells what's installed apart from what's declared.
---

cronix never trusts a side-channel state file. The host scheduler itself is the state of record. Every owned entry carries an **ownership marker** — a comment line, a unit annotation, a label, a tag — that records which app, which job, which schedule index, and a hash of the rendered job.

`cronix drift` answers one question: **does what's installed in the backend still match what the current manifest would produce?** Hand-edits, partial deploys, schema changes, and accidental deletions all surface here.

## The hash

Every owned entry stores a 16-character hex hash. The hash is an FNV-1a 64-bit fold over the canonicalized normalized job, salted by schedule index:

```
hash(job, idx) = fnv1a64(canonicalize({version:1, app:"_hash_", jobs:[job]})) ^ idx
```

| Property | Why |
|---|---|
| FNV-1a 64-bit | Fast, collision-rare for the job-spec size class, deterministic across implementations. |
| Salted by index | Two schedules of the same job differ only in the index field — without the salt they would collide. |
| Computed from the canonical form | The same NormalizedJob produces byte-identical input regardless of input ordering. |
| 16 hex chars | Short enough to fit on a crontab comment line; long enough to make collisions vanishingly rare in practice. |

The hash lives in the ownership marker; the algorithm lives in `internal/policy`. Backends and the reconciler must produce byte-identical output — otherwise every plan would surface a phantom diff.

## What `cronix drift` does

```bash
cronix drift --manifest ./billing.cronix.json \
  --backend crontab --crontab-path /etc/crontab \
  --exit-on-drift
```

For each desired (app, job, index) tuple in the manifest and each managed entry currently on the backend:

| Desired hash | Installed hash | Result |
|---|---|---|
| `abc123…` | `abc123…` | **OK** — entry is in sync. |
| `abc123…` | `def456…` | **Drift** — the manifest changed (or the entry was edited). `apply` will rewrite. |
| `abc123…` | `drift-spec-edited` | **Drift** — backends emit this sentinel when they detect the live spec (e.g. a CronJob's `schedule:`, a systemd `OnCalendar=`) was edited out-of-band even though the ownership marker was left intact. |
| `abc123…` | (missing) | **Drift** — entry missing. `apply` will create. |
| (not in manifest) | `def456…` | **Drift** — entry orphaned. `apply` will delete. |

With `--exit-on-drift`, the command exits `5` on any divergence — useful for CI gates that fail the build when production has drifted from main.

## Why hash-based?

The alternative is "always re-render and compare textually". That works, but it's brittle: a whitespace change in the rendered crontab line, a different field order in a CronJob, a non-significant systemd unit option — all would surface as false drift.

Hashing the canonical normalized job means:

- **Cosmetic differences are invisible.** Reordering JSON keys, switching `schedule` to `schedules: [<value>]`, changing input whitespace — none of these produce a different hash.
- **Semantic changes are always caught.** A different schedule, URL, header, timeout, or retry budget produces a different hash, full stop.
- **The same algorithm runs everywhere.** The Go reconciler, every Go backend, and every future SDK port produce identical hashes for the same input.

## Idempotency

`apply` with no manifest changes is a complete no-op:

| What `apply` does **not** do when nothing changed | Why it matters |
|---|---|
| No file writes that change content | No mtime churn on `/etc/crontab`. |
| No `systemctl daemon-reload` | systemd doesn't get poked on every CI run. |
| No K8s API mutation calls | No `resourceVersion` bump, no kube-apiserver audit-log noise. |
| No INFO-level log lines | CI logs stay clean — only changes are reported. |

This is the contract operators rely on when running `apply` from CI on every deploy. A noisy idempotent path makes CI expensive and gets the reconciler removed from the pipeline.

## Ownership markers — per backend

cronix records ownership inside the resource it manages. There is no side-channel state file. See [Backends overview](/cronix/backends/overview/) for the full table; the short version:

| Backend | Marker location |
|---|---|
| `crontab` | A `# cronix:owned app=<app> job=<name> hash=<sha> idx=<n>` comment line immediately following the schedule line. |
| `systemd-timer` | `X-Cronix-{App,Job,Index,Hash}=` annotations inside the `.timer` and `.service` unit files. |
| `kubernetes` | `cronix.dev/managed: "true"` plus `cronix.dev/{app,job,index,hash}` labels on the `CronJob` and `ConfigMap`. |
| `aws-scheduler` | `cronix-` name prefix and a structured `Description` field on the EventBridge Schedule. |

Two contracts every backend must honor:

1. **`Create` writes the marker.** Every artifact `Create` produces carries an unforgeable cronix marker.
2. **`Delete` refuses unmanaged.** `Delete` MUST refuse to remove artifacts cronix did not create. The co-existence guarantee is the reason operators trust cronix to run alongside hand-rolled cron entries.

## Manual edits are always detected

Manual edits to an owned entry are caught by drift detection — even when the editor was clever about it.

| Edit | What drift sees |
|---|---|
| Operator hand-edits the schedule field but leaves the marker | Backend computes `hash` from the live spec; mismatches the marker; backend reports `drift-spec-edited`. |
| Operator changes the schedule field AND replaces the marker hash | Reconciler-side: `hash` in marker doesn't match the desired hash from the manifest. **Drift.** |
| Operator deletes the marker line entirely (crontab) | Entry is no longer owned. From cronix's perspective, the desired job is missing. **Drift** (will be re-created). |
| Operator deletes the entire entry (line, unit, CronJob) | Same — desired but not installed. **Drift** (will be re-created). |
| Operator adds an unmanaged entry | Not cronix-owned, not in the manifest. **Ignored** — co-existence guarantee. |

The only way to "trick" drift is to forge an ownership marker with a hash that happens to match what the next `apply` would produce. That's a 64-bit FNV-1a collision — possible in theory, irrelevant in practice.

## Drift exit code

| Exit code | When |
|---|---|
| `0` | No drift, or `--exit-on-drift` not set. |
| `5` | Drift detected AND `--exit-on-drift` was set. |
| Other reconciler exit codes | See the [CLI reference](/cronix/cli/global-status/) for the full list. |

`5` is reserved for drift specifically. CI scripts can branch on `$?`:

```bash
cronix drift --manifest ./manifest.json --backend crontab \
  --crontab-path /etc/crontab --exit-on-drift
case $? in
  0) echo "in sync" ;;
  5) echo "drift detected — review and re-apply" ; exit 1 ;;
  *) echo "drift command itself failed" ; exit 1 ;;
esac
```

## Worked example

You start with this manifest installed:

```bash
cronix list --backend crontab --crontab-path /etc/crontab
# APP              JOB                  IDX  HASH
# billing-service  reconcile-payments   0    eefe2dd0dcf563e2
```

Someone hand-edits the crontab line to change `*/15 * * * *` to `*/5 * * * *`:

```bash
cronix drift --manifest ./manifest.json --backend crontab \
  --crontab-path /etc/crontab --exit-on-drift
# Plan: backend=crontab noop=false ops=1
#   ~ update billing-service.reconcile-payments  (eefe2dd0dcf563e2 → drift-spec-edited)
# drift detected
# exit=5
```

`cronix apply` re-renders the line back to `*/15 * * * *` with the original hash. The drift acknowledgement is `apply` itself.

## See also

- [Backends overview](/cronix/backends/overview/) — per-backend ownership markers.
- [Manifest format](/cronix/concepts/manifest/) — the normalization that produces the hash input.
- [`global-status`](/cronix/cli/global-status/) — the multi-backend read-only counterpart to drift.
