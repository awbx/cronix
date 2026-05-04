# Backend: systemd-timer

> **Status:** stable as of v0.3.0. `cronix apply --backend systemd-timer` writes unit files and drives `systemctl daemon-reload` / `enable --now` / `disable --now` via the `SystemctlExecutor` interface. Render-only output (`RenderUnits`) is still available for operators who prefer to install units by hand.

## Layout

Per (app, job, schedule-index), cronix manages a pair of unit files:

```
/etc/systemd/system/cronix-<app>-<job>-<idx>.timer
/etc/systemd/system/cronix-<app>-<job>-<idx>.service
```

The `.timer` carries `OnCalendar=`; the `.service` invokes `cronix trigger <app>.<job>` once. Both files include `X-Cronix-{App,Job,Index,Hash}=` annotations cronix uses for ownership detection (systemd ignores `X-` prefixed fields, so they double as our marker).

## Reconciling a manifest

```bash
sudo cronix apply \
  --manifest /etc/cronix/manifest.json \
  --backend systemd-timer \
  --trigger-bin /usr/local/bin/cronix
```

Add `--systemd-unit-dir <path>` to install into a non-default directory (useful for user-scoped units under `~/.config/systemd/user/`). cronix will refuse to run if `/run/systemd/system` doesn't exist (i.e. systemd isn't the init system).

`cronix list`, `cronix plan`, `cronix drift`, `cronix prune`, and `cronix show` all accept the same backend flags.

## Rendering by hand

```go
import "github.com/awbx/cronix/go/internal/backends/systemd"

timerFile, serviceFile, err := systemd.RenderUnits(
    "/usr/local/bin/cronix",
    "billing",
    job,         // a manifest.NormalizedJob
    0,           // schedule index
)
// Write to /etc/systemd/system/cronix-billing-<jobname>-0.{timer,service}
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now cronix-billing-<jobname>-0.timer
```

## OnCalendar translation

| Manifest schedule | Rendered `OnCalendar=` |
|---|---|
| `@hourly` | `hourly` |
| `@daily` / `@midnight` | `daily` |
| `@weekly` | `weekly` |
| `@monthly` | `monthly` |
| `@yearly` / `@annually` | `yearly` |
| `0 2 * * *` | `*-*-* 2:0:00` |
| `0 9 * * 1-5` | `Mon..Fri *-*-* 9:0:00` |

Run `systemd-analyze calendar "<expr>"` to verify a translated expression in your systemd version.

## Run history

Until `cronix history` ships, use:

```bash
journalctl -u cronix-billing-<jobname>-0.service --output=json --since=24h
```

The shim emits one structured JSON line per fire to stdout, so each entry's `MESSAGE` field is a self-describing record.

## Limitations

- Sub-minute scheduling is supported by systemd but rejected at v1's manifest validation layer for fidelity with crontab. May relax in a later version for systemd-timer-only deployments.
- `Replace` concurrency policy is enforced by the shim, not by `RuntimeMaxSec=`.
