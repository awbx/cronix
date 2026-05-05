---
title: cronix version
description: Print the cronix version, commit, build date, Go runtime, and target platform.
---

`version` prints build identification — the cronix release version, the commit it was built from, the build timestamp, the Go runtime version, and the target OS/architecture. There are no flags and no positional arguments.

Use it as the first sanity check on any host: `cronix version` confirms the binary on `$PATH` is the one you expect, on the architecture you expect, before you start reconciling against real backends.

## Synopsis

```
cronix version
```

## Flags

None.

## Example

```bash
cronix version
# cronix v0.6.0
#   commit: be7ed23f8a4c
#   built:  2026-05-04T14:22:09Z
#   go:     go1.23.4
#   target: linux/amd64
```

## Notes

- **`version`, `commit`, and `built` are baked at build time** by the release tooling (`-ldflags "-X ..."`). A binary built outside that pipeline reports `dev` / `none` / `unknown` for those fields — visible at a glance.
- **`target` reports the binary's compile target**, not the running host. They are usually the same; if they aren't, you have a binary running under translation (Rosetta, qemu) and that's worth knowing before reconciling production schedules.
- **For runtime ownership and per-host status**, see [`cronix global-status`](/cronix/cli/global-status/).
