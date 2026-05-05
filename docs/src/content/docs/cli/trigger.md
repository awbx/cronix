---
title: cronix trigger
description: Per-fire executor invoked by the host scheduler — loads the spec, signs the request, acquires the lock, and runs the job.
---

`cronix trigger` is the per-fire executor invoked by the host scheduler — crontab, systemd-timer, Kubernetes CronJob, AWS EventBridge Scheduler — at every fire. It loads the job spec from the operator-managed spec directory, resolves the configured secrets, acquires the concurrency lock per the job's policy, signs the HTTP request with HMAC-SHA256, sends it with the configured timeout, and retries on transient failure with exponential backoff.

You typically do not invoke `trigger` by hand — `apply` writes a schedule entry that calls it. It's documented here so you can debug a misbehaving fire by running the same command the scheduler would, on the same host.

## Synopsis

```
cronix trigger <app>.<job> [flags]
```

The positional argument must be `<app>.<job>` — e.g. `billing.reconcile-payments`. It addresses one spec file at `<spec-dir>/<app>.<job>.json`.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--spec-dir` | `$CRONIX_JOB_SPEC_DIR` → `/etc/cronix/jobs` | Directory containing the per-job `<app>.<job>.json` spec files |
| `--lock-dir` | `/var/lock/cronix` | Directory for `flock` files used by per-host concurrency policies |

`trigger` deliberately takes no `--backend` flag — it is the executor side of the system, not the reconciler.

## Examples

Reproduce a fire by hand:

```bash
sudo cronix trigger billing.reconcile-payments \
  --spec-dir /etc/cronix/jobs \
  --lock-dir /var/lock/cronix
echo $?
# 0   (success)
```

Run a dev fire pointing at a tmp spec directory:

```bash
cronix trigger billing.reconcile-payments \
  --spec-dir /tmp/cronix-jobs \
  --lock-dir /tmp/cronix-locks
```

The trigger emits one slog-JSON line per attempt to stdout. Capture it for debugging:

```bash
cronix trigger billing.reconcile-payments 2>&1 | tee /tmp/trigger.log
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success — endpoint returned 2xx |
| `1` | App rejected — endpoint returned 4xx; does not retry |
| `2` | Retries exhausted — 5xx, network error, or timeout exhausted the retry policy |
| `3` | Internal error — panic, missing or malformed spec, unresolved secret, lock-backend failure |
| `4` | Lock contended — concurrency policy is `Forbid` and another instance held the lock; transient |
| `75` | Same as `4`, exposed as POSIX `EX_TEMPFAIL` for systems (cron, init) that interpret `75` specially |

`4` and `75` are deliberately equivalent — both signal "did not run, try again next tick". Use `4` in shell scripts; `75` is what the systemd-timer service file sets via `SuccessExitStatus=` so cron-style retry tooling treats it as transient.

## Notes

- **Reads spec files written by `apply`.** Don't hand-craft spec files; let `cronix apply --spec-dir <dir>` write them. The shim's spec format is an internal contract that may evolve across versions.
- **Logging is JSON-only.** One `slog` JSON record per attempt to stdout, then `os.Exit(code)`. There is no progress reporting and no stderr noise; the exit code is the contract.
- **`--spec-dir` defaults via env first, then `/etc/cronix/jobs`.** Setting `CRONIX_JOB_SPEC_DIR` lets you override without re-writing the schedule entry — handy for the Kubernetes pod, which mounts the spec ConfigMap to a volume and points the env var at it.
- **Concurrency lock is per host.** The lock is a `flock` on `<lock-dir>/<app>.<job>`. For cluster-wide locks (across many hosts firing the same job), configure a Redis lock backend in the operator config — the trigger reads the policy from the spec and selects the backend accordingly.
