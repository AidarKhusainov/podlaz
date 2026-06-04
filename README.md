# TunWarden

TunWarden is a Linux-first, CLI-first VPN/proxy client foundation for Xray-compatible configurations.

The product goal is to provide a safe, lightweight, and predictable Linux VPN client for technical users. TunWarden should make route, DNS, firewall, TUN, and process lifecycle changes explicit, inspectable, reversible, and recoverable across Wi-Fi changes, suspend/resume, daemon crashes, and failed connection attempts.

## Current status

This repository is at foundation stage.

What exists now:

- Go module and CI skeleton.
- `tunwarden` CLI skeleton.
- `tunwardend` daemon skeleton with read-only local Unix socket status and doctor APIs.
- `packaging/systemd/tunwardend.service` for manual systemd service startup with journald logging.
- Manual `profile add`, VLESS `profile import`, `profile list`, `profile show`, and `profile delete --yes` commands backed by local user state.
- Read-only `plan --mode proxy-only` dry-run for stored VLESS profiles with deterministic generated Xray config validation.
- Read-only `status` command with daemon-backed status and local runtime fallback.
- Read-only `doctor` command with daemon-backed diagnostics, local Linux host fallback, and explicit `doctor --core --xray <path>` local Xray binary validation.
- Read-only `logs` command for recent `tunwardend` journald logs.
- Read-only `recover` dry-run scan for clearly TunWarden-owned recovery candidates.
- Initial internal models for transactions, profiles, and subscriptions.
- Product, CLI, architecture, state/security, networking, subscription, roadmap, and development documentation.

What does not exist yet:

- No real VPN tunnel is established yet.
- No Xray process is started yet.
- No generated runtime config is written yet.
- No TUN interface, route, DNS, or firewall mutation is applied yet.
- No GUI is planned for the early product.

## Product principles

1. **Linux-first:** Ubuntu LTS and Debian stable are Tier 1. Fedora and Arch should be supported through explicit platform adapters.
2. **CLI-first:** the first-class interface is a deterministic command line.
3. **Daemon-owned privilege:** privileged networking belongs in a supervised daemon.
4. **Transactional networking:** every privileged network mutation must have a plan, snapshot, verification path, and rollback path.
5. **Observable behavior:** `status`, `doctor`, `plan`, logs, and scoped diagnostics must make route, DNS, firewall, and core state understandable.
6. **Recoverability over feature count:** disconnect, rollback, and recovery are core product capabilities, not maintenance helpers.
7. **Lightweight by default:** avoid unnecessary background components, hidden global mutation, and broad protocol expansion before reliability is proven.

## Commands available in the foundation build

- `go test ./...`
- `go run ./cmd/tunwarden version`
- `go run ./cmd/tunwarden profile add --name test --server example.com --port 443 --protocol vless`
- `go run ./cmd/tunwarden profile import '<vless-share-uri>'`
- `go run ./cmd/tunwarden profile list`
- `go run ./cmd/tunwarden profile show test`
- `go run ./cmd/tunwarden profile delete test --yes`
- `go run ./cmd/tunwarden plan --mode proxy-only <profile-id>`
- `go run ./cmd/tunwarden status`
- `go run ./cmd/tunwarden doctor`
- `go run ./cmd/tunwarden doctor --core --xray /usr/local/bin/xray`
- `go run ./cmd/tunwarden logs`
- `go run ./cmd/tunwarden recover`
- `go run ./cmd/tunwardend`
- `sudo systemctl start tunwardend` after manually installing `packaging/systemd/tunwardend.service` and the daemon binary.

Canonical command names are defined in [CLI contract](docs/cli.md). The implemented manual and VLESS-import profile behavior is defined in [Profile management](docs/profile-management.md). The implemented v0.1 proxy-only plan behavior is defined in [Proxy-only plan](docs/proxy-only-plan.md). The implemented v0.1 daemon transport is defined in [Daemon local API](docs/daemon-api.md). The implemented v0.1 `status` behavior is defined in [Status command](docs/status.md). The implemented v0.1 `doctor` checks are defined in [Doctor diagnostics](docs/doctor-diagnostics.md). The implemented v0.1 `logs` behavior is defined in [Logs command](docs/logs.md). The implemented v0.1 `recover` scan is defined in [Recovery dry-run](docs/recovery-dry-run.md).

## Intended lifecycle model

`plan -> snapshot -> apply -> verify -> commit`, with rollback on failure.

`recover` exists as the recovery path. In early builds it is dry-run only and must not change host networking state.

## Documentation

Start with the documentation index:

- [Documentation map](docs/README.md)

Primary documents:

- [Product requirements](docs/product-requirements.md)
- [CLI contract](docs/cli.md)
- [Profile management](docs/profile-management.md)
- [Proxy-only plan](docs/proxy-only-plan.md)
- [Daemon local API](docs/daemon-api.md)
- [Status command](docs/status.md)
- [Doctor diagnostics](docs/doctor-diagnostics.md)
- [Logs command](docs/logs.md)
- [Recovery dry-run](docs/recovery-dry-run.md)
- [Architecture](docs/architecture.md)
- [State and security requirements](docs/state-and-security.md)
- [Package boundaries](docs/package-boundaries.md)
- [Networking and reliability requirements](docs/networking-reliability.md)
- [Subscriptions and profiles](docs/subscriptions-and-profiles.md)
- [Roadmap](docs/roadmap.md)
- [Development guide](docs/development.md)
- [References](docs/references.md)

## License

TunWarden is licensed under the MIT License. See [LICENSE](LICENSE).
