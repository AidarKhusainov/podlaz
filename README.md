# TunWarden

TunWarden is a Linux-first, CLI-first VPN/proxy client foundation for Xray-compatible configurations.

The product goal is to provide a safe, lightweight, and predictable Linux VPN client for technical users. TunWarden should make route, DNS, firewall, TUN, and process lifecycle changes explicit, inspectable, reversible, and recoverable across Wi-Fi changes, suspend/resume, daemon crashes, and failed connection attempts.

## Current status

This repository is at foundation stage.

What exists now:

- Go module and CI skeleton.
- `tunwarden` CLI skeleton.
- `tunwardend` daemon skeleton.
- Read-only `status` command with local runtime fallback status.
- Read-only `doctor` command with local Linux host diagnostics.
- Read-only `recover` dry-run scan for clearly TunWarden-owned recovery candidates.
- Initial internal models for transactions, profiles, and subscriptions.
- Product, CLI, architecture, state/security, networking, subscription, roadmap, and development documentation.

What does not exist yet:

- No real VPN tunnel is established yet.
- No Xray process is started yet.
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
- `go run ./cmd/tunwarden status`
- `go run ./cmd/tunwarden doctor`
- `go run ./cmd/tunwarden recover`
- `go run ./cmd/tunwardend`

Canonical command names are defined in [CLI contract](docs/cli.md). The implemented v0.1 `status` fallback is defined in [Status command](docs/status.md). The implemented v0.1 `doctor` checks are defined in [Doctor diagnostics](docs/doctor-diagnostics.md). The implemented v0.1 `recover` scan is defined in [Recovery dry-run](docs/recovery-dry-run.md).

## Intended lifecycle model

`plan -> snapshot -> apply -> verify -> commit`, with rollback on failure.

`recover` exists as the recovery path. In early builds it is dry-run only and must not change host networking state.

## Documentation

Start with the documentation index:

- [Documentation map](docs/README.md)

Primary documents:

- [Product requirements](docs/product-requirements.md)
- [CLI contract](docs/cli.md)
- [Status command](docs/status.md)
- [Doctor diagnostics](docs/doctor-diagnostics.md)
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
