# Backend: systemd-timer

> **Status (v1):** unit-file rendering and `Validate` ship in this release. The full reconciliation cycle (`List`/`Create`/`Update`/`Delete` shelling out to `systemctl`) lands in a follow-up phase — see PLAN.md §5c. Operators using systemd today can render the units via the SDK and apply them manually with `systemctl daemon-reload && systemctl enable --now`.

## Layout

Per (app, job, schedule-index), cronix manages a pair of unit files:

```
/etc/systemd/system/cronix-<app>-<job>-<idx>.timer
/etc/systemd/system/cronix-<app>-<job>-<idx>.service
```

The `.timer` carries `OnCalendar=`; the `.service` invokes `cronix trigger <app>.<job>` once. Both files include `X-Cronix-App=`, `X-Cronix-Job=`, `X-Cronix-Index=` annotations cronix uses for ownership detection.

## Rendering by hand (v1 fallback)

```go
import "github.com/awbx/cronix/go/internal/backends/systemd"

timerFile, serviceFile, err := systemd.RenderUnits(
    "/usr/local/bin/cronix",
    "billing",
    job,         // a manifest.NormalizedJob
    0,           // schedule index
)
// Write to /etc/systemd/system/cronix-billing-<jobname>-0.timer / .service
```

Then:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now cronix-billing-<jobname>-0.timer
```

## OnCalendar translation

| Manifest schedule | Rendered `OnCalendar=` |
|---|---|
| `@hourly` | `hourly` |
| `@daily`/`@midnight` | `daily` |
| `@weekly` | `weekly` |
| `@monthly` | `monthly` |
| `@yearly`/`@annually` | `yearly` |
| `0 2 * * *` | `*-*-* 2:0:00` |
| `0 9 * * 1-5` | `Mon..Fri *-*-* 9:0:00` |
| `*/15 * * * *` | `*-*-* *:*/15:00` (when supported) |

Run `systemd-analyze calendar "<expr>"` to verify a translated expression in your systemd version.

## Run history

`journalctl -u cronix-billing-<jobname>-0.service --output=json --since=24h` is the canonical source. The shim emits structured JSON to stdout, so each fire is a single self-describing record.

## Limitations

- Sub-minute scheduling is supported by systemd but rejected at v1's manifest validation layer for fidelity with crontab. v1.1 may relax this for systemd-timer-only deployments.
- `Replace` concurrency policy is enforced by the shim, not by `RuntimeMaxSec=` (which only kills the previous run if the new fire is the same unit; we use distinct-per-fire transient units instead).
