---
title: cronix drift
description: Detect divergence between a manifest and the installed backend state — optionally fail CI on drift.
---

`drift` reads the manifest and the backend's current state, then prints the operations [`apply`](/cronix/cli/apply/) would perform to bring the backend into agreement. With `--exit-on-drift`, it returns exit code `5` when any drift is detected — the canonical way to fail a CI job whose deployed cron state has wandered from the source of truth.

It is the difference-detection counterpart to [`plan`](/cronix/cli/plan/): same plan computation, same flags, but `drift` is named for the question it answers ("has someone manually edited the crontab?") and exits non-zero on a non-empty plan when asked to.

## Synopsis

```
cronix drift --manifest <source> [flags]
```

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--manifest` | (required) | Manifest source — `./path`, `/abs`, `file://`, `https://`, or `http://localhost` |
| `--secret-ref` (repeatable) | (none) | Required for `https://` sources |
| `--exit-on-drift` | `false` | Exit `5` when any drift is detected (zero ops still exits `0`) |
| `-o, --output` | `table` | `table` or `json` |

Plus all [backend flags](/cronix/cli/backend-flags/).

## Examples

A clean check on a backend that matches the manifest:

```bash
cronix drift --manifest ./billing.cronix.json \
  --crontab-path /tmp/cronix.crontab
# Plan: backend=crontab noop=true ops=0
echo $?
# 0
```

After someone edited the crontab by hand:

```bash
cronix drift --manifest ./billing.cronix.json \
  --crontab-path /tmp/cronix.crontab \
  --exit-on-drift
# Plan: backend=crontab noop=false ops=1
#   ~ update  billing.reconcile-payments  (eefe2dd0dcf563e2 → a1b2c3d4e5f60718)
echo $?
# 5
```

JSON output for a CI gate that should fail on drift:

```bash
cronix drift --manifest ./billing.cronix.json --exit-on-drift -o json
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | No drift, or drift detected without `--exit-on-drift` |
| `5` | Drift detected and `--exit-on-drift` was passed |
| Other | Backend or manifest error (manifest fetch failed, backend `List` failed, etc.) |

## Notes

- **Drift detection covers manual edits.** The crontab and systemd-timer backends recompute the per-entry hash from the installed bytes, so editing a schedule by hand is detected even though the cronix fence/marker is intact.
- **`drift` never writes.** Run it from CI without worrying about side effects. To actually fix the drift, run [`apply`](/cronix/cli/apply/) with the same manifest.
- **Action markers in table output.** `+` = create, `~` = update, `-` = delete, `·` = skip.
- **For multi-backend ownership audits** that don't require a manifest, see [`global-status`](/cronix/cli/global-status/).
