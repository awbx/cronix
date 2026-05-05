---
title: cronix completion
description: Emit a shell completion script for bash, zsh, fish, or PowerShell.
---

`completion` writes a shell completion script for the chosen shell to stdout. cronix uses cobra's built-in completion generator — flag names, subcommand names, and enum-style values like `--backend crontab|systemd-timer|...` are all completable once the script is installed.

The script is emitted to stdout so you can pipe it into the right system path for your shell. Re-run after upgrading cronix to pick up new subcommands and flags.

## Synopsis

```
cronix completion <bash|zsh|fish|powershell>
```

The shell name is required and must be one of `bash`, `zsh`, `fish`, or `powershell`.

## Flags

None.

## Examples

Bash — system-wide install:

```bash
cronix completion bash | sudo tee /etc/bash_completion.d/cronix > /dev/null
```

Bash — per-user install:

```bash
cronix completion bash > ~/.local/share/bash-completion/completions/cronix
```

Zsh:

```bash
cronix completion zsh > "${fpath[1]}/_cronix"
# Reload your shell or `compinit` for changes to take effect.
```

Fish:

```bash
cronix completion fish > ~/.config/fish/completions/cronix.fish
```

PowerShell — append to your profile:

```powershell
cronix completion powershell | Out-String | Invoke-Expression
```

## Notes

- **stdout-only.** No files are touched by `cronix completion` itself — you choose where the script lands by redirecting. This makes it safe to run from any context, including read-only filesystems.
- **Re-run after upgrading.** Completion data is generated from the current binary's command tree. A new release that adds a subcommand or flag won't be completable until you re-emit and re-install.
- **Zsh requires `compinit`.** Most distros wire this in by default. If completion seems to no-op, run `autoload -U compinit && compinit` once after installing.
