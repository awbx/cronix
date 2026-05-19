---
title: Production runbook
description: Everything on-call needs — failure modes, incident playbooks, dashboards, alerts, capacity guidance.
---

This page is for the engineer on-call when cronix stops behaving. It assumes you've read [trigger lifecycle](/cronix/concepts/trigger-lifecycle/), [drift detection](/cronix/concepts/drift/), and [state management](/cronix/concepts/state/). If you're here because something is broken right now, skip to [incident playbooks](#incident-playbooks).

## Mental model recap (60-second version)

cronix has three moving parts at runtime:

- **The host scheduler** — `crontab`, `systemd-timer`, `CronJob`, EventBridge Scheduler, or Vercel Cron. Fires `cronix trigger <app>.<job>` at the configured time. **This is the thing that can fail to fire.**
- **The trigger shim** — `cronix trigger`, the small binary the host invokes. Signs the request, acquires the concurrency lock, applies the timeout, retries, writes structured logs to stderr. **This is the thing that runs in between.**
- **The application** — your service. Receives the HTTP request, verifies the HMAC, runs the handler. **This is the thing that actually does the work.**

When something breaks, the question is always: **which of those three?** The diagnostic tools below answer that.

## Health check at-a-glance

These commands should all succeed on a healthy host:

```sh
# 1. Is the reconciler in sync?
cronix drift --backend <crontab|systemd-timer|kubernetes|aws-scheduler|vercel> \
  --manifest https://app.example.com/.well-known/cron-manifest \
  --exit-on-drift
# exit 0 = in sync; exit 5 = drift detected

# 2. Are all expected entries actually installed?
cronix list --backend <name>

# 3. Did the last few fires succeed?
cronix history --backend <name> --app <app> --limit 20

# 4. Cross-backend operator view
cronix global-status      # reads ~/.cronix/cronix.yaml
```

If any return non-zero or surprising output, jump to the matching [failure mode](#failure-modes).

## Failure modes

The five most common production failure modes, each diagnosable from the artifacts on the host. Per the [state management](/cronix/concepts/state/) page, all state lives in the backend itself — so "what happened" is always reconstructable from native log sources without consulting any cronix-side state.

### App down at fire time

**Symptom:** the host scheduler fired, the trigger shim ran, the HTTP request was attempted, the app didn't answer (connection refused, timeout, 5xx).

**Diagnose:**

```sh
# systemd backend
journalctl -t cronix -S "10 minutes ago" --grep="<job-name>"

# K8s backend
kubectl logs -l cronix.dev/job=<job-name> -n <ns> --tail=200

# What the shim saw
cronix history --app <app> --job <job> --limit 5 --output json | jq '.[] | {fire_time, attempts, last_status, last_error}'
```

The shim writes one structured log line per attempt. Look for `http_status: 0` (connection refused), non-2xx status codes, and the cumulative attempt count from the retry policy.

**Remediate:**

- If the app is down because of a deploy in progress: nothing to do. The fire is logged as failed; next scheduled run is the only retry (this is the documented at-least-once-but-no-buffering contract).
- If the app is down because of a real outage: the [retry policy](/cronix/concepts/retries/) on the job spec gave the shim N attempts at increasing backoff. Those have already happened. The next scheduled fire is the next chance.
- If you want a one-shot retry now: `cronix trigger <app>.<job>` runs the same logic the host scheduler runs. Same idempotency guarantees, same run-id behavior.

### Manifest fetch fails

**Symptom:** `cronix apply` or `cronix drift` exits with `manifest fetch failed` and a non-2xx HTTP status, TLS error, or DNS failure.

**Diagnose:**

```sh
# Reproduce the fetch with curl
curl -fsSL -H 'Accept: application/json' \
  https://app.example.com/.well-known/cron-manifest \
  | jq '.version, .app, (.jobs | length)'
```

**Common causes**, ordered by likelihood:

| Symptom | Likely cause | Fix |
|---|---|---|
| 404 | Manifest endpoint not registered in the app | Add `app.all("/.well-known/cron-manifest", handle((req) => cron.handle(req)))` (or the framework-adapter equivalent) |
| 401 / 403 | Auth middleware in front of the endpoint | The manifest endpoint must be public for cronix to fetch. Exclude it from your auth middleware, or use HMAC-signed fetch (see [authentication](/cronix/concepts/auth/)) |
| 5xx | App is down or the manifest handler crashed | Same as "app down at fire time" — fix the app |
| TLS error | Cert chain issue | Verify with `curl -v ...` and `openssl s_client -connect ...:443` |
| DNS | Hostname not resolving | Standard DNS triage |

**The reconciler does not modify the backend when the manifest fetch fails.** Owned entries stay as they were. This is the explicit "never touch unmanaged entries" guarantee extended: never touch managed entries based on an unverifiable manifest either.

### Backend write fails

**Symptom:** `cronix apply` exits non-zero with a backend-specific error (permission denied on crontab, K8s API forbidden, EventBridge throttling, Vercel API error).

**Diagnose by backend:**

| Backend | What to check first |
|---|---|
| `crontab` | Is the user running `cronix apply` the owner of the crontab file? `cronix apply` needs write to `/etc/crontab` or `/etc/cron.d/cronix` |
| `systemd-timer` | Is cronix running as root or with `CAP_SYS_ADMIN`? `systemctl daemon-reload` requires it |
| `kubernetes` | Does the ServiceAccount have the Role bound from the Helm chart? `kubectl auth can-i create cronjobs -n <ns> --as=system:serviceaccount:<ns>:<sa>` |
| `aws-scheduler` | Does the role have `scheduler:CreateSchedule`, `:UpdateSchedule`, `:DeleteSchedule`, `:GetSchedule`, `:ListSchedules`? IAM policy first, throttling second |
| `vercel` | Is `VERCEL_TOKEN` scoped to the right project? Is the project on a plan that supports cron? |

**Remediate:** fix the underlying permission/quota issue; re-run `cronix apply`. cronix is idempotent — re-running after a partial failure converges to the desired state without duplicating entries.

### Lock acquisition fails

**Symptom:** trigger shim logs `lock_acquire_failed` and exits 9. The fire is logged as not-attempted-due-to-lock.

This is **a feature, not a bug** — it means the job's concurrency policy did its job.

**Diagnose:**

```sh
# Was another fire already running?
cronix history --app <app> --job <job> --limit 5 --output json \
  | jq '.[] | select(.lock_state != null) | {fire_time, lock_state, run_id}'

# For host-scope locks (the default), check the lock file
ls -la /var/lib/cronix/locks/<app>__<job>.lock

# For global-scope locks, check Redis
redis-cli -h $REDIS_HOST KEYS "cronix:lock:<app>:<job>:*"
```

**Concurrency policy** controls the behavior:

| Policy | Lock acquisition behavior |
|---|---|
| `Allow` (default in some legacy contexts) | No lock; multiple fires can overlap. The shim doesn't even try. |
| `Forbid` | If lock is held, skip this fire entirely. Logged as `policy_skipped`. |
| `Replace` | If lock is held, cancel the previous fire and acquire. Previous fire gets `SIGTERM` then `SIGKILL` after `grace_period_seconds`. |

If you see frequent `Forbid` skips, the previous fire is running longer than the schedule interval. Either:
- Increase `timeout_seconds` on the job, OR
- Switch to `Replace` if you can afford to interrupt the previous run, OR
- Spread the schedule out (less frequent), OR
- Split the work into smaller pieces

### Retry exhausted

**Symptom:** trigger shim attempted the configured `max_attempts`, all failed, exited 1. The fire is logged as exhausted-retries.

**Diagnose:**

```sh
cronix history --app <app> --job <job> --limit 5 --output json \
  | jq '.[] | {fire_time, attempts, attempt_errors}'
```

Each entry shows the per-attempt error message. Common patterns:

| Pattern | What it means |
|---|---|
| All attempts return the same HTTP status (e.g., 502 repeated × 3) | App is consistently failing — fix the app, not the retry policy |
| Each attempt returns a different status | App is flapping — likely a deploy in progress or a dependency outage |
| `attempt_errors: ["context deadline exceeded", ...]` | Handler is running longer than `timeout_seconds`. Either increase the timeout or speed up the handler |
| Errors interleaved with `lock_acquire_failed` | The job's `Forbid` policy is preventing retries. Switch to `Replace` if appropriate |

**Remediate:** the next scheduled fire is the next opportunity. The shim does **not** carry retries across fires — every fire is independent. This is the documented "no central state" tradeoff.

## Incident playbooks

The "what to actually do at 2am" templates. Each starts from a symptom you'd see in PagerDuty.

### "The job stopped firing"

**Symptom:** alert says fire-count for `<app>.<job>` dropped to zero over the last hour.

```
1. Check the host scheduler itself
   - crontab:     `crontab -l` on the host. Is the line still there?
   - systemd:     `systemctl list-timers | grep cronix-<app>-<job>`
   - k8s:         `kubectl get cronjob -n <ns> <name> -o yaml | grep -E 'suspend|schedule'`
   - aws:         `aws scheduler get-schedule --name cronix-<app>-<job>`
   - vercel:      check vercel.json crons[] for the entry

2. If the entry is gone:
   - Did someone hand-delete it? Check the backend's audit log
     (CloudTrail for aws, K8s audit log, git history for vercel.json)
   - Did `cronix apply` accidentally prune it? Check the manifest:
     `curl https://app.example.com/.well-known/cron-manifest | jq '.jobs[].name'`
   - Restore by re-applying: `cronix apply --backend <name> --manifest <url>`

3. If the entry is there but not firing:
   - Check the host scheduler is running:
     - crontab:   `systemctl status cron`  (or `crond` on RHEL)
     - systemd:   `systemctl status cronix-<app>-<job>.timer`
     - k8s:       `kubectl get cronjob -n <ns> <name> -o yaml | grep suspend`
                  (suspend: true means it won't fire)
     - aws:       check the schedule state in EventBridge — could be DISABLED
     - vercel:    check the project's cron tab in the Vercel dashboard

4. If the host scheduler IS firing but no trigger logs appear:
   - The trigger binary may be missing or unexecutable
   - Run it manually: `cronix trigger <app>.<job>` and observe stderr
   - Check the trigger spec exists: `ls /etc/cronix/jobs/`
```

### "Drift detected and I don't know what changed"

**Symptom:** `cronix drift --exit-on-drift` returns 5 in CI.

```
1. See what diverges
   cronix plan --backend <name> --manifest <url>

2. Identify per-entry:
   - "would create"   → manifest has a new job; safe to apply
   - "would delete"   → manifest removed a job; safe to apply
   - "would update"   → manifest's hash != backend's hash (either
                        the manifest changed OR the backend entry
                        was hand-edited)

3. For "would update" entries, the trick is figuring out which
   side changed:
   - git log on the application repo: did the schedule change recently?
   - backend audit log: was the entry edited out-of-band?

4. If you trust the manifest: cronix apply
   If you trust the backend: revert the manifest and re-apply
   If you don't trust either: bisect the manifest's git log
```

### "Run-id collisions are happening"

**Symptom:** application logs show the same `run_id` invoking the handler twice within seconds.

This is the **at-least-once delivery** behavior documented in the RFC. The shim retries on connection errors; if the handler completed but the connection died before the 2xx response was received, the shim retries with **the same run-id**. The app must dedupe.

```
1. Confirm the handler dedupes on run_id
   - SQL: INSERT ... ON CONFLICT (run_id) DO NOTHING
   - Redis: SET cronix:run:<run_id> 1 NX EX 86400
   - Idempotency-by-design: the handler is naturally idempotent

2. If not: this is an app-side fix, not a cronix-side fix
   The shim guarantees stable run-ids across retries; it does not
   guarantee single-delivery.
```

## Dashboards

The metrics worth graphing, with example queries. All assume structured logs are being scraped by your usual pipeline (Loki, CloudWatch, Datadog, etc.).

### Fire-rate per job

Expected count of fires over a window. Drops to zero on a single-job problem; drops universally on a broader outage.

```promql
# Prometheus (if using OTel → Prometheus exporter)
sum by (app, job) (
  rate(cronix_trigger_fires_total[5m])
)

# Loki / journald
{unit="cronix.service"} |= "cronix.trigger.fire" | json
  | rate[5m] by (app, job)
```

### Error-rate per job

```promql
sum by (app, job) (
  rate(cronix_trigger_fires_total{outcome="failed"}[5m])
) /
sum by (app, job) (
  rate(cronix_trigger_fires_total[5m])
)
```

### Drift status

If `cronix drift --watch` is deployed ([roadmap, v1.1](https://github.com/awbx/cronix/issues/15)), expose its check result as a gauge:

```promql
# 0 = clean, 1 = drift
max by (app, backend) (cronix_drift_status)
```

Until `drift --watch` exists, run `cronix drift --exit-on-drift` from CI on every commit to the manifest; the CI failure is the alert.

### Lock contention rate

How often are fires getting `Forbid`-skipped or `Replace`-cancelled?

```promql
sum by (app, job, lock_state) (
  rate(cronix_trigger_fires_total{lock_state!="acquired"}[15m])
)
```

A nonzero value means the previous fire is consistently running longer than the schedule interval. See the [retry exhausted](#retry-exhausted) section.

## Alert recipes

Three alerts cover the 80% case. Tune thresholds to your fire frequency.

### Fire-rate dropped to zero

```promql
ALERT CronixJobNotFiring
IF sum by (app, job) (rate(cronix_trigger_fires_total[15m])) == 0
   AND on(app, job) cronix_expected_fire_rate > 0
FOR 30m
LABELS  { severity = "page" }
ANNOTATIONS {
  summary = "{{ $labels.app }}.{{ $labels.job }} has not fired in 15m",
  playbook = "https://awbx.github.io/cronix/operations/runbook/#the-job-stopped-firing",
}
```

`cronix_expected_fire_rate` is a recording rule you produce from the manifest's schedules (e.g., `*/5 * * * *` → 12/hour). Suppresses the alert for jobs whose schedule is "long enough that 30m of silence is normal" (`@daily`, `@weekly`).

### Error-rate spike

```promql
ALERT CronixJobErrorRateHigh
IF (
  sum by (app, job) (rate(cronix_trigger_fires_total{outcome="failed"}[15m]))
  /
  sum by (app, job) (rate(cronix_trigger_fires_total[15m]))
) > 0.5
FOR 30m
LABELS  { severity = "page" }
ANNOTATIONS {
  summary = "{{ $labels.app }}.{{ $labels.job }} failing >50% of attempts",
  playbook = "https://awbx.github.io/cronix/operations/runbook/#app-down-at-fire-time",
}
```

### Drift detected

```promql
ALERT CronixDriftDetected
IF max by (app, backend) (cronix_drift_status) > 0
FOR 1h
LABELS  { severity = "ticket" }
ANNOTATIONS {
  summary = "{{ $labels.app }} on {{ $labels.backend }} has drifted from its manifest",
  playbook = "https://awbx.github.io/cronix/operations/runbook/#drift-detected-and-i-dont-know-what-changed",
}
```

Drift is a ticket-level alert, not a page — it usually means a manifest PR is mid-merge or someone is debugging. The 1-hour `FOR` window is generous.

## Capacity and scaling

cronix is a thin layer over the host scheduler. Capacity questions reduce to **the host scheduler's capacity** + **the shim's per-fire overhead**.

### How many jobs per host?

| Backend | Practical limit | Bottleneck |
|---|---|---|
| `crontab` | ~1000 lines per crontab file before `cron(8)` parsing becomes noticeable | Cron's parser is single-threaded; large crontabs slow every fire |
| `systemd-timer` | ~10,000 units per systemd instance | `systemctl list-units` performance degrades; daemon-reload becomes slow |
| `kubernetes` | ~500 `CronJob` resources per cluster before the controller's reconcile loop noticeably slows | API server etcd-write rate; the CronJob controller's loop is global |
| `aws-scheduler` | 1,000,000 schedules per account (AWS quota) | EventBridge Scheduler limits — not a cronix concern at any realistic scale |
| `vercel` | Plan-dependent (5-100 crons per project on Pro; more on Enterprise) | Vercel-side, not cronix |

For practical deployments, the bottleneck is almost always **the application** (handler latency × fire frequency), not cronix itself.

### Lock store sizing (when `concurrency_scope: global`)

A single Redis instance comfortably handles 10,000+ jobs at 1-per-second fire rate. Each job holds one Redis key with TTL = the job's `timeout_seconds`. Memory footprint is ~100 bytes per active fire.

Use a dedicated Redis instance (not shared with application cache) so cronix lock acquisition isn't affected by application cache eviction patterns.

### Trigger shim overhead

Each `cronix trigger` invocation does, in order: load operator config + job spec (~5ms cold, ~0ms warm), resolve secrets (~1-10ms depending on `secret_refs:` source), generate run-id (negligible), acquire lock (~1-50ms depending on `host` vs `global`), sign HMAC (~1ms), execute HTTP (handler-dependent), write logs (negligible).

End-to-end shim overhead is ~10-50ms in the typical case. If your fire frequency is 1/second per job and you have 100 jobs, expect ~5% of one CPU dedicated to cronix on a typical host.

## Going deeper

- [State management](/cronix/concepts/state/) — why everything in this page is reconstructable from backend-native artifacts
- [Trigger lifecycle](/cronix/concepts/trigger-lifecycle/) — what runs at every fire, in order
- [Drift detection](/cronix/concepts/drift/) — hash algorithm, exit codes
- [Concurrency policies](/cronix/concepts/concurrency/) — Allow/Forbid/Replace, host vs global scope
- [Retries & timeouts](/cronix/concepts/retries/) — the per-job retry policy
- [Backends overview](/cronix/backends/overview/) — per-backend pages with operator-specific commands
