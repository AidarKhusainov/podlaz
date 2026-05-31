# TunWarden

TunWarden is a Linux-first, CLI-first VPN/proxy client foundation for Xray-compatible configurations.

The product goal is to provide a safe, lightweight, and predictable Linux VPN client for technical users. TunWarden should make route, DNS, firewall, TUN, and process lifecycle changes explicit, inspectable, reversible, and recoverable across Wi-Fi changes, suspend/resume, daemon crashes, and failed connection attempts.

## Current status

This repository is at foundation stage.

What exists now:

- Go module and CI skeleton.
- `tunwarden` CLI skeleton.
- `tunwardend` daemon skeleton.
- Read-only `doctor` command contract.
- Dry-run `panic-reset` command contract.
- Initial internal models for transactions, profiles, and subscriptions.
- Product, architecture, networking, subscription, roadmap, and development documentation.

What does not exist yet:

- No real VPN tunnel is established yet.
- No Xray process is started yet.
- No TUN interface, route, DNS, or firewall mutation is applied yet.
- No GUI is planned for the early product.

## Product principles

1. **Linux-first:** Ubuntu LTS and Debian stable are Tier 1. Fedora and Arch should be supported through explicit platform adapters.
2. **CLI-first:** the first-class interface is a deterministic command line.
3. **Daemon-owned privilege:** privileged networking belongs in a supervised root daemon, not in a SUID frontend or GUI.
4. **Transactional networking:** every privileged network mutation must have a plan, snapshot, verification path, and rollback path.
5. **Observable behavior:** `status`, `doctor`, `plan`, logs, and explain commands must make route, DNS, firewall, and core state understandable.
6. **Recoverability over feature count:** disconnect, rollback, and panic reset are core product capabilities, not maintenance helpers.
7. **Lightweight by default:** avoid unnecessary background components, hidden global mutation, and broad protocol expansion before reliability is proven.

## Commands available in the foundation build

```bash
go test ./...
go run ./cmd/tunwarden version
go run ./cmd/tunwarden doctor
go run ./cmd/tunwarden panic-reset

go run ./cmd/tunwardend
```

## Intended lifecycle model

```text
plan -> snapshot -> apply -> verify -> commit
                             \-> rollback on failure
```

`panic-reset` exists as an emergency recovery path. In the current build it prints a dry-run cleanup plan only.

## Documentation

Start with the documentation index:

- [Documentation map](docs/README.md)

Primary documents:

- [Product requirements](docs/product-requirements.md)
- [Architecture](docs/architecture.md)
- [Networking and reliability requirements](docs/networking-reliability.md)
- [Subscriptions and profiles](docs/subscriptions-and-profiles.md)
- [Roadmap](docs/roadmap.md)
- [Development guide](docs/development.md)
- [References](docs/references.md)

## License

TunWarden is licensed under the MIT License. See [LICENSE](LICENSE).
