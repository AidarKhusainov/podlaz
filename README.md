# TunWarden

TunWarden is a Linux-first CLI VPN/proxy client for Xray-compatible profiles and subscriptions.

It is built for technical users who want networking changes to be explicit, inspectable, reversible, and recoverable. The `tunwarden` CLI owns profile and subscription intent. The `tunwardend` daemon owns runtime lifecycle and daemon state.

## Scope

TunWarden targets Linux systems with standard networking and service-management tools:

- systemd and journald;
- NetworkManager;
- systemd-resolved;
- nftables and iproute2;
- Linux TUN;
- Xray-compatible profile and subscription inputs.

The core product path is CLI and daemon first. GUI clients, mobile platforms, router distributions, provider account management, and broad non-Xray protocol expansion are outside the core path until the Linux networking foundation is reliable.

## Safety model

TunWarden treats VPN activation as a recoverable transaction:

```text
plan -> snapshot -> apply -> verify -> commit
                             |
                             v
                          rollback
```

Safety rules:

- the CLI must not directly change privileged Linux networking state;
- proxy-only mode must not change TUN devices, routes, DNS, nftables, or firewall state;
- TUN/full-tunnel work must be daemon-owned, planned before mutation, verified before commit, and recoverable after failure;
- generated core configs are runtime output, not profile source of truth;
- status, diagnostics, logs, plans, and recovery output must redact secrets consistently.

## Build

```bash
go test ./...
go run ./cmd/tunwarden version
go run ./cmd/tunwardend
```

Build a local Debian package:

```bash
bash scripts/build-deb.sh
sudo apt install ./dist/tunwarden_0.0.0~dev_amd64.deb
```

The package installs binaries, systemd/sysusers files, manual pages, and project documentation under packaged filesystem locations. It must not ship runtime state, generated core configs, user state, `/run/tunwarden`, or `/usr/local` files.

## Basic workflow

```bash
tunwarden import <uri-or-file-or-url>
tunwarden profile list
tunwarden plan --mode proxy-only <profile-id>
tunwarden connect --mode proxy-only <profile-id>
tunwarden status
tunwarden logs
tunwarden disconnect
tunwarden recover
```

Use `tunwarden plan --mode tun <profile-id>` to inspect full-tunnel network intent before host networking work. Use `tunwarden doctor` for diagnostics and `tunwarden recover` to inspect TunWarden-owned stale state.

The canonical command contract is [CLI contract](docs/cli.md).

## Documentation

Start with [Documentation](docs/README.md).

Primary references:

- [CLI contract](docs/cli.md) for command names, flags, exit codes, and output contracts;
- [Architecture](docs/architecture.md) for the CLI/daemon split and transaction model;
- [State and security requirements](docs/state-and-security.md) for state layout, redaction, confirmation, systemd hardening, and core process safety;
- [Networking and reliability requirements](docs/networking-reliability.md) for TUN, routing, DNS, firewall, recovery, and reliability invariants;
- [Debian package contract](docs/debian-package.md) for package layout and lifecycle;
- [Development guide](docs/development.md) for local checks and contribution rules;
- [tunwarden(1)](docs/man/tunwarden.1) and [tunwardend(8)](docs/man/tunwardend.8) for local manual pages.

## License

TunWarden is licensed under the MIT License. See [LICENSE](LICENSE).
