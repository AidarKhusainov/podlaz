# Shell completion

This document defines the user-visible shell completion contract for the `tunwarden` CLI.

## CLI contract

TunWarden exposes completion generation commands:

```bash
tunwarden completion bash
tunwarden completion zsh
tunwarden completion fish
```

Each command writes the generated completion definition to stdout.

Completion script generation is read-only. It must not:

- contact `tunwardend`;
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

Bash completion also calls the hidden runtime command `tunwarden __complete` while the user is completing a command line. The runtime command is internal CLI plumbing, not a public workflow. It uses the shared Go completion registry for command, flag, enum, and argument completion decisions.

Dynamic Bash completion suggests:

- profile IDs for `connect`, `plan`, `profile show`, and `profile delete`;
- subscription IDs for `subscription show` and `subscription update`.

Dynamic completion reads only the local user-owned profile and subscription stores needed for those ID suggestions. Missing, unreadable, or invalid local state must produce no dynamic candidates and no completion-time error output.

Dynamic completion must not contact `tunwardend`, open the daemon socket, start Xray, fetch subscription URLs, inspect runtime transaction state, read generated core configs, mutate local state, mutate Linux networking, or require root.

File-path positions keep shell default file completion. This includes local path positions for `tunwarden import`.

zsh and fish completion currently remain static and cover command, subcommand, flag, and enum values only.

## Packaged install contract

The Debian package installs completion files under conventional distro completion directories:

```text
/usr/share/bash-completion/completions/tunwarden
/usr/share/zsh/vendor-completions/_tunwarden
/usr/share/fish/vendor_completions.d/tunwarden.fish
```

Package-managed completion should work in normal shell sessions where the distribution shell completion support is enabled. Users should not need to edit shell startup files for a standard packaged install.

## Validation

The package gate must verify that the generated `.deb` contains completion files for bash, zsh, and fish. CI should also check that the generated scripts contain shell-specific entrypoints and at least one static enum completion value such as `proxy-only` and `tun`. Bash validation should also verify that the generated wrapper calls the hidden runtime completion command.
