---
title: cronix init
description: Scaffold an operator config at ~/.cronix/cronix.yaml with sensible defaults and optional pre-fill.
---

`init` writes a heavily-commented operator config to the chosen path so you have a working starting point for the operator-side configuration that [`global-status`](/cronix/cli/global-status/) and other multi-backend tooling reads. Defaults to `~/.cronix/cronix.yaml`, with the same path resolution as the rest of the CLI: `$CRONIX_CONFIG`, then `~/.cronix/cronix.yaml`, then `/etc/cronix/cronix.yaml`.

It refuses to overwrite an existing file unless `--force` is passed — running `init` twice on the same host is a safe no-op until you opt in. With `--app` / `--manifest-url` / `--secret-ref` you can pre-fill the first `manifest_sources` entry at scaffold time, skipping the manual edit.

## Synopsis

```
cronix init [flags]
```

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--config` | `$CRONIX_CONFIG` → `~/.cronix/cronix.yaml` | Destination path |
| `--app` | (none) | Pre-fill the first `manifest_sources` entry's `app` field |
| `--manifest-url` | (none) | Pre-fill the first `manifest_sources` entry's `url` field |
| `--secret-ref` (repeatable) | (none) | Pre-fill `secret_refs` for the first entry |
| `--force` | `false` | Overwrite an existing file |

## Examples

Empty scaffold at the default path:

```bash
cronix init
# wrote /Users/me/.cronix/cronix.yaml
# next: edit manifest_sources[].url and secret_refs, then run `cronix apply --config /Users/me/.cronix/cronix.yaml`.
```

Pre-fill an app entry at scaffold time:

```bash
cronix init \
  --app billing \
  --manifest-url https://billing.example.com/.well-known/cron-manifest \
  --secret-ref env:CRON_SECRET
```

Overwrite an existing config to a tmp path for testing:

```bash
cronix init --config /tmp/cronix.yaml --force
# wrote /tmp/cronix.yaml
```

## Notes

- **Refuses to overwrite by default.** If the destination exists, `init` errors out with a `pass --force to overwrite` message and exits non-zero. This protects production config from accidental scaffolding.
- **File mode is `0600`.** The scaffold may contain secret references; cronix writes it with owner-only read/write permissions. Parent directories are created with `0755`.
- **Path resolution mirrors the rest of the CLI.** The same `--config` flag, `$CRONIX_CONFIG` env, and `~/.cronix/cronix.yaml` / `/etc/cronix/cronix.yaml` precedence apply to every multi-backend command — set them once and `init` matches.
- **The scaffold is a starting point, not a complete config.** It includes `manifest_sources`, `locks`, `defaults` blocks and inline comments. Edit it before running `apply` against real hosts.
