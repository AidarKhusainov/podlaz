# Shell completion

This document defines the user-visible shell completion contract for the `podlaz` CLI.

## CLI contract

podlaz exposes completion generation commands:

```bash
podlaz completion bash
podlaz completion zsh
podlaz completion fish
```

Each command writes the generated completion definition to stdout.

Completion script generation is read-only. It must not:

- contact `podlazd`;
- start Xray;
- read profile, subscription, runtime, or transaction state;
- read or print secrets;
- mutate TUN devices, routes, DNS, nftables, firewall rules, or generated runtime files;
- require root.

## Completion scope

Generated completions cover:

- top-level commands;
- implemented nested `profile` and `subscription` subcommands;
- implemented static flags;
- static enum values such as connection mode values `proxy-only` and `tun`;
- static protocol names accepted by `profile add --protocol`.

Bash, zsh, and fish completion call the hidden runtime command `podlaz __complete` while the user is completing a command line. The runtime command is internal CLI plumbing, not a public workflow. It uses the shared Go completion registry for command and argument completion decisions.

Fish completion must register static flags and static option values with fish-native `complete` options such as `--long-option`, `--short-option`, and `--arguments`. The runtime argument completer remains responsible for dynamic IDs and file-completion directives.

Dynamic shell completion suggests:

- profile IDs for `connect`, `plan`, `profile show`, and `profile delete`;
- subscription IDs for `subscription show`, `subscription update`, and `subscription delete`.

Dynamic completion candidates keep the command argument value as the stable ID. When the shell adapter supports candidate descriptions, the description is the sanitized display name from the local profile or subscription store. Completion must not make display names a second command identity.

Dynamic completion reads only the local user-owned profile and subscription stores needed for those ID suggestions. Missing, unreadable, or invalid local state must produce no dynamic candidates and no completion-time error output.

If the hidden runtime completion command is executed with effective UID `0` and `SUDO_USER` set, dynamic profile/subscription ID completion must fail before opening local stores. Shell adapters redirect runtime completion errors away from the interactive prompt, so users should see no dynamic ID candidates from accidental `sudo` completion paths. Running the hidden command directly must produce the same actionable non-sudo guidance as other user-state commands.

Dynamic completion must not contact `podlazd`, open the daemon socket, start Xray, fetch subscription URLs, inspect runtime transaction state, read generated core configs, mutate local state, mutate Linux networking, or require root.

File-path positions keep shell default file completion. This includes local path positions for `podlaz import`.

## Packaged install contract

The Debian package installs completion files under conventional distro completion directories:

```text
/usr/share/bash-completion/completions/podlaz
/usr/share/zsh/vendor-completions/_podlaz
/usr/share/fish/vendor_completions.d/podlaz.fish
```

Package-managed completion should work in normal shell sessions where the distribution shell completion support is enabled. Users should not need to edit shell startup files for a standard packaged install.

## Validation

The package gate must verify that the generated `.deb` contains completion files for bash, zsh, and fish. CI should also check that the generated scripts contain shell-specific entrypoints, call the hidden runtime completion command, include at least one static enum completion value such as `proxy-only` and `tun`, and exercise fish option and option-value contexts.
