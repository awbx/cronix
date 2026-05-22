---
title: Migrating from hand-edited crontab
description: Walk an existing crontab full of cronix trigger lines into cronix management without re-creating entries.
---

If you've been using `cronix trigger` from a hand-edited crontab — maybe because you started before the reconciler was ready, maybe because you wanted to roll out trigger-side benefits (HMAC, retries, locks) before committing to declarative management — the `cronix adopt` command brings those lines under reconciliation without disrupting their firing cadence.

**What "adopt" means:** find the existing crontab line that already invokes `cronix trigger <app>.<job>` and add the cronix ownership marker after it. The original line is preserved byte-for-byte; only a comment line is added. The very next `cron(8)` fire is unaffected.

**What "adopt" does NOT do:** it does not change schedules, does not rewrite commands, and never deletes a line. If your existing line disagrees with what the manifest says, adopt refuses to modify anything and prints the divergences for you to resolve.

## The full migration in four commands

```sh
# 1. Verify your manifest is reachable + valid
cronix validate https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET

# 2. Preview what adopt would do — no changes
cronix adopt billing.reconcile-payments \
  --backend crontab \
  --crontab-path /etc/crontab \
  --trigger-bin /usr/local/bin/cronix \
  --manifest https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET \
  --dry-run

# 3. Adopt for real (writes ownership marker)
cronix adopt billing.reconcile-payments \
  --backend crontab \
  --crontab-path /etc/crontab \
  --trigger-bin /usr/local/bin/cronix \
  --manifest https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET

# 4. From this point on, just use `cronix apply` normally
cronix apply --backend crontab \
  --crontab-path /etc/crontab \
  --trigger-bin /usr/local/bin/cronix \
  --manifest https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET
```

Repeat steps 2–3 for every `(app, job)` pair you have. There's no batch mode in v1.0.0-rc.1; future versions will support adopting an entire manifest in one call.

## What adopt accepts

A crontab line is a **candidate** if its command tail is **exactly**:

```
<triggerBin> trigger <app>.<job>
```

with the `<triggerBin>` matching the `--trigger-bin` flag you passed. Anything else — a wrapper script, extra arguments, a different binary path — is not adopted. The strictness is deliberate: cronix doesn't want to guess your intent at adoption time.

If your line looks like:

```cron
0 * * * * /opt/scripts/run-cronix.sh billing reconcile-payments
```

…adopt won't claim it. Two paths forward:

1. **Rewrite the line yourself** to invoke `cronix trigger` directly, then run adopt.
2. **Let `cronix apply` overwrite it.** First `cronix prune` the existing line (or delete it manually), then `cronix apply` to create the managed version. This is non-atomic — there's a brief window where the schedule doesn't fire — but it's the only path if you want to keep the wrapper behavior in a script vs. inline.

## What adopt rejects (divergence)

Adopt refuses to claim a line whose schedule (5-field cron) doesn't match the manifest. Example:

```
crontab:    */5 * * * * /usr/local/bin/cronix trigger billing.ping
manifest:   @hourly                                                  (i.e. 0 * * * *)
```

Adopt prints:

```
DIVERGED         billing.ping  (no action taken)
  ! schedules[0] ("@hourly" → "0 * * * *"): no candidate crontab line with this 5-field cron
  ! line 1 (cron "*/5 * * * *") invokes /usr/local/bin/cronix trigger billing.ping but does not match any manifest schedule
```

Resolve by editing either side so they agree, then re-run adopt. Or if you want the manifest to win unconditionally, run `cronix apply` — it will replace the line.

## What adopt skips

A `(app, job)` that's **already cronix-managed** (has the `# cronix:owned` marker) returns `ALREADY-MANAGED` with no action. Idempotent — safe to run adopt repeatedly in CI.

A `(app, job)` with **no candidate line at all** returns `NOT-FOUND` and exits 7. You probably want `cronix apply` (which creates) rather than adopt (which claims existing).

## Multi-schedule jobs

A manifest job with multiple schedules requires a candidate line **per schedule**. All schedules must be present in the crontab; partial coverage is reported as divergent. The mapping is by 5-field cron equality, so the order of lines in your crontab doesn't matter — adopt finds the right line for each schedule.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Adopted, already-managed, or dry-run found candidates |
| 6 | Diverged — manifest and backend disagree, no action taken |
| 7 | No candidate entry found on the backend |

CI scripts can `if cronix adopt ... ; then ...` to handle each case.

## Beyond crontab

`cronix adopt` ships for the `crontab` backend in v1.0.0-rc.1. The other four backends (systemd-timer, kubernetes, aws-scheduler, vercel) implement the same `Adopter` interface in follow-up issues — track them under the `area/backend-<name>` labels.

Until those land, `cronix adopt --backend systemd-timer` returns a clear error pointing at the per-backend tracking issue.

## Going deeper

- [`cronix adopt` CLI reference](/cronix/cli/adopt/) *(pending)*
- [Production runbook §"the job stopped firing"](/cronix/operations/runbook/#the-job-stopped-firing) for what happens when a managed entry stops firing after adoption
- [D-026](https://github.com/awbx/cronix/blob/main/spec/DECISIONS.md) — ownership marker contract
