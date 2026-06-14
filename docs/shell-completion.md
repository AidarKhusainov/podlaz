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

The completion command is read-only. It must not:

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

Dynamic values such as profile IDs, subscription IDs, file paths, URLs, Xray paths, and journal time expressions are intentionally left as no-op or shell-default value positions. Completion generation must not read user state just to suggest dynamic values.

## Packaged install contract

The Debian package installs completion files under conventional distro completion directories:

```text
/usr/share/bash-completion/completions/tunwarden
/usr/share/zsh/vendor-completions/_tunwarden
/usr/share/fish/vendor_completions.d/tunwarden.fish
```

Package-managed completion should work in normal shell sessions where the distribution shell completion support is enabled. Users should not need to edit shell startup files for a standard packaged install.

## Validation

The package gate must verify that the generated `.deb` contains completion files for bash, zsh, and fish. CI should also check that the generated scripts contain shell-specific entrypoints and at least one static enum completion value such as `proxy-only` and `tun`.
