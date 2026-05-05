---
title: Concurrency policies
description: What happens when a fire arrives while the previous run is still going.
---

A scheduled job can fire faster than the handler completes — a 1-minute schedule with a handler that occasionally takes 90 seconds, a daily reconcile that has a slow week. The `policy.concurrency` setting on each job decides what to do when a fire arrives and the previous one is still running.

cronix borrows the vocabulary directly from Kubernetes `CronJob.concurrencyPolicy`: `Allow`, `Forbid`, `Replace`. Same names, same semantics — engineers familiar with K8s do not need to learn a new policy.

## The three policies

| Policy | Behavior | Exit code on contention | Use when |
|---|---|---|---|
| `Forbid` (default) | Skip the new fire; the previous run continues. | `4` (`ExitLockContended`), also `75` (`ExitTempfail`) | At-most-one-at-a-time guarantees matter (writes, settlements, anything non-idempotent across overlapping runs). |
| `Allow` | No locking. Both runs proceed in parallel. | n/a (no contention possible) | Reads, fan-in to an idempotent endpoint, anything where a second concurrent run is fine. |
| `Replace` | Kill the previous run, then start the new one. *In v1, behaves as `Forbid` and logs the intent.* | `4` (`ExitLockContended`) | Long-running periodic syncs where the latest fire's view of the world is the only one that matters. |

The default is `Forbid` because non-idempotent overlap is the most common bug class. If your handler is genuinely safe to run in parallel, set `concurrency: "Allow"` explicitly — the explicitness shows up in code review.

### A note on `Replace`

In v1, `Replace` is documented as best-effort host-scope only and currently behaves as `Forbid` — the trigger shim logs a warning recording the intent, then exits with `ExitLockContended`. The SIGTERM-the-previous-holder path that fully implements `Replace` is deferred. Apps that need true replace semantics should treat `Replace` as a hint and design handlers to be safe under `Forbid` semantics in v1.

## Scope: where the lock lives

The `policy.concurrency_scope` setting decides where the lock is held:

| Scope | Default | Backend | Notes |
|---|---|---|---|
| `host` | yes | `flock(2)` on a file under `/var/lock/cronix/` | Per-host lock. Two hosts can each be running the same job concurrently. Crashed shims do not leak the lock — the kernel releases the file lock on process exit. |
| `global` | no | Redis (`SET-NX-EX` with Lua-fenced refresh + release) | Cross-host lock. Requires a configured Redis lock backend (`--lock-backend redis`). Fenced release prevents a stale Refresh/Release from a previous holder from stomping on the current holder. |

If you run `cronix apply` from CI on three hosts and your job needs **exactly one** instance running anywhere in the fleet, you need `concurrency_scope: global` and a Redis backend. Without it, each host's flock is independent, and you can get up to one concurrent run per host.

## Lock TTL

The lock TTL is set automatically — operators don't tune it:

```
TTL = timeout_seconds + 30s safety margin
```

| Reason | Effect |
|---|---|
| TTL ≥ `timeout_seconds` | The shim cannot outlive its own lock. The 30s headroom covers the small wall-clock gap between "context deadline exceeded" and "process exits, kernel releases flock". |
| TTL is bounded | A crashed shim won't hold the lock forever. The next fire after `TTL` reclaims the lock automatically. |

You change the TTL by changing `timeout_seconds` in your manifest. There is no separate lock-TTL field.

## Why the shim, not the host scheduler

Concurrency enforcement lives in the trigger shim — not in `cron(8)`, not in `systemd-timer`'s `RuntimeMaxSec=`, not in K8s `CronJob.concurrencyPolicy`. There are two reasons:

1. **Portability.** The exact same `Forbid`/`Allow`/`Replace` semantics work across crontab, systemd-timer, Kubernetes, and AWS EventBridge Scheduler. Apps don't need to learn per-backend quirks; their manifest is the contract.
2. **Single source of truth.** The shim already runs at every fire to handle [retries, timeouts, signing, and structured logging](/cronix/concepts/trigger-lifecycle/). Folding concurrency into the same layer means one code path, one set of tests, one contract.

Backends MAY also enable native concurrency limits as belt-and-braces — K8s `CronJob.concurrencyPolicy: Forbid`, systemd-timer's `RuntimeMaxSec=` — but the source of truth is the shim's lock.

## Worked example

```json
{
  "version": 1,
  "app": "billing-service",
  "jobs": [
    {
      "name": "settle-invoices",
      "schedule": "*/10 * * * *",
      "request": {
        "url": "https://billing.internal/api/v1/scheduled/settle-invoices"
      },
      "policy": {
        "concurrency": "Forbid",
        "concurrency_scope": "global",
        "timeout_seconds": 300
      }
    }
  ]
}
```

This job fires every 10 minutes. With `concurrency: Forbid` and `concurrency_scope: global`, even if you `cronix apply` from three CI runners, only one shim will hold the Redis lock at a time. The other two exit `ExitLockContended` (4) immediately. Lock TTL is `300 + 30 = 330` seconds.

## Operator behavior on contention

When a `Forbid` lock is contended, the shim:

- Emits a single structured log line at INFO with `event=lock-contended`, `app`, `job`, `run_id`, and `scope`.
- Exits with code `4` (or `75`, equivalently — see the [trigger lifecycle exit code map](/cronix/concepts/trigger-lifecycle/)).

`cron(8)` honors `MAILTO` thresholds for exit code `75` specifically, which is why both `4` and `75` map to the same "transient contention" meaning. Operators can monitor either.

Contention is **not** treated as failure. It does not consume a retry attempt. The next scheduled fire is the next opportunity — there is no in-process backoff-and-retry loop for lock contention.

## See also

- [Trigger lifecycle](/cronix/concepts/trigger-lifecycle/) — where the lock acquire/release sits in the per-fire flow.
- [Retries & timeouts](/cronix/concepts/retries/) — how `timeout_seconds` interacts with the lock TTL.
- [Manifest format](/cronix/concepts/manifest/) — the `policy` block schema.
