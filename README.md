# TunWarden

TunWarden is a Linux-first, CLI-first VPN/proxy client foundation for Xray-compatible configurations.

The product goal is to provide a safe, lightweight, and predictable Linux VPN client for technical users. TunWarden should make route, DNS, firewall, TUN, and process lifecycle changes explicit, inspectable, reversible, and recoverable across Wi-Fi changes, suspend/resume, daemon crashes, and failed connection attempts.

## Current status

This repository is at foundation stage.

What exists now:

- Go module and CI skeleton.
- `tunwarden` CLI skeleton.
- `tunwardend` daemon skeleton with local Unix socket status, doctor, connect, and disconnect APIs.
- `packaging/systemd/tunwardend.service` for packaged systemd service startup with journald logging.
- Local Debian `.deb` package contract and nFPM-based package build script.
- Top-level `tunwarden import` for VLESS share URIs and Base64 URI-list subscriptions.
- Manual `profile add`, VLESS `profile import`, `profile list`, `profile show`, and `profile delete --yes` commands backed by local user state.
- Base64 URI-list `subscription add`, `subscription list`, `subscription show`, and `subscription update` commands backed by local user state.
- Read-only `plan --mode proxy-only` dry-run for stored VLESS profiles with deterministic generated Xray config validation.
- Read-only `plan --mode tun` full-tunnel TUN/route/DNS/nftables kill-switch dry-run for stored profiles without route, policy-rule, DNS, nftables, TUN, Xray, or runtime config mutation.
- Daemon-managed `connect --mode proxy-only` and `disconnect` for starting and stopping Xray without changing system networking.
- `status` command with daemon-backed active/inactive proxy-only status and local runtime fallback.
- Read-only `doctor` command with daemon-backed diagnostics, local Linux host fallback, and explicit `doctor --core --xray <path>` local Xray binary validation.
- Read-only `logs` command for recent `tunwardend` journald logs.
- Read-only `recover` dry-run scan for clearly TunWarden-owned recovery candidates.
- Local manual pages for `tunwarden(1)` and `tunwardend(8)`.
- Initial internal models for transactions, profiles, subscriptions, read-only system snapshots, and full-tunnel TUN/route/DNS/nftables kill-switch planning.
- Product, CLI, architecture, state/security, networking, subscription, package, roadmap, development, and v0.1 acceptance documentation.

What does not exist yet:

- No TUN/full-tunnel VPN mode is established yet.
- No privileged TUN/full-tunnel execution, verified leak protection, or health-check apply/verify behavior exists yet.
- No route, policy-rule, DNS, nftables, or firewall mutation is applied yet.
- No automatic Xray download/update is implemented yet.
- No public apt repository, package signing, or tagged release automation exists yet.
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
- `go run ./cmd/tunwarden import '<vless-share-uri>'`
- `go run ./cmd/tunwarden import file:///tmp/sub.txt`
- `go run ./cmd/tunwarden profile add --name test --server example.com --port 443 --protocol vless`
- `go run ./cmd/tunwarden profile import '<vless-share-uri>'`
- `go run ./cmd/tunwarden profile list`
- `go run ./cmd/tunwarden profile show test`
- `go run ./cmd/tunwarden profile delete test --yes`
- `go run ./cmd/tunwarden subscription add --name personal --url file:///tmp/sub.txt`
- `go run ./cmd/tunwarden subscription list`
- `go run ./cmd/tunwarden subscription show personal`
- `go run ./cmd/tunwarden subscription update personal`
- `go run ./cmd/tunwarden plan --mode proxy-only <profile-id>`
- `go run ./cmd/tunwarden plan --mode tun <profile-id>`
- `go run ./cmd/tunwarden connect --mode proxy-only <profile-id>`
- `go run ./cmd/tunwarden status`
- `go run ./cmd/tunwarden disconnect`
- `go run ./cmd/tunwarden doctor`
- `go run ./cmd/tunwarden doctor --core --xray /usr/local/bin/xray`
- `go run ./cmd/tunwarden logs`
- `go run ./cmd/tunwarden recover`
- `go run ./cmd/tunwardend`
- `man ./docs/man/tunwarden.1`
- `man ./docs/man/tunwardend.8`
- `./scripts/build-deb.sh`
- `sudo apt install ./dist/tunwarden_0.0.0~dev_amd64.deb`
- `sudo systemctl start tunwardend` after package installation or manual installation of the packaged unit and daemon binary.

Canonical command names are defined in [CLI contract](docs/cli.md). The implemented top-level import behavior is covered by the CLI contract and [v0.1 acceptance checklist](docs/v0.1-acceptance.md). The implemented manual and VLESS-import profile behavior is defined in [Profile management](docs/profile-management.md). The implemented subscription behavior is defined in [Subscription management](docs/subscription-management.md). The implemented v0.1 proxy-only plan behavior is defined in [Proxy-only plan](docs/proxy-only-plan.md). The implemented TUN snapshot input behavior is defined in [System snapshot model](docs/system-snapshot.md). The implemented TUN full-tunnel dry-run behavior is defined in [TUN full-tunnel dry-run plan](docs/tun-full-tunnel-plan.md). The implemented v0.1 daemon transport and lifecycle API are defined in [Daemon local API](docs/daemon-api.md). The implemented v0.1 `status` behavior is defined in [Status command](docs/status.md). The implemented v0.1 `doctor` checks are defined in [Doctor diagnostics](docs/doctor-diagnostics.md). The implemented v0.1 `logs` behavior is defined in [Logs command](docs/logs.md). The implemented v0.1 `recover` scan is defined in [Recovery dry-run](docs/recovery-dry-run.md). The local Debian package layout and lifecycle contract are defined in [Debian package contract](docs/debian-package.md). Local user and administrator reference pages are available as [tunwarden(1)](docs/man/tunwarden.1) and [tunwardend(8)](docs/man/tunwardend.8); after package installation they are available through `man tunwarden` and `man tunwardend`.

## Intended lifecycle model

`plan -> snapshot -> apply -> verify -> commit`, with rollback on failure.

`plan --mode tun` currently performs the read-only snapshot and full-tunnel dry-run portions of that lifecycle. It produces intended TUN device, route, policy-rule, DNS, nftables/firewall chain/rule, kill-switch, server-bypass, route-loop, warning, and rollback output, but it does not mutate the host and does not yet provide privileged execution, verified leak protection, or health-check apply/verify behavior.

`connect --mode proxy-only` currently applies only daemon-owned Xray process lifecycle and generated runtime config state. It must not mutate TUN, routes, DNS, nftables, or firewall state.

`recover` exists as the recovery path. In early builds it is dry-run only and must not change host networking state.

## Documentation

Start with the documentation index:

- [Documentation map](docs/README.md)

Primary documents:

- [Product requirements](docs/product-requirements.md)
- [CLI contract](docs/cli.md)
- [Profile management](docs/profile-management.md)
- [Subscription management](docs/subscription-management.md)
- [Proxy-only plan](docs/proxy-only-plan.md)
- [System snapshot model](docs/system-snapshot.md)
- [TUN full-tunnel dry-run plan](docs/tun-full-tunnel-plan.md)
- [Proxy-only lifecycle](docs/proxy-only-lifecycle.md)
- [Daemon local API](docs/daemon-api.md)
- [Status command](docs/status.md)
- [Doctor diagnostics](docs/doctor-diagnostics.md)
- [Logs command](docs/logs.md)
- [Recovery dry-run](docs/recovery-dry-run.md)
- [v0.1 acceptance checklist](docs/v0.1-acceptance.md)
- [Architecture](docs/architecture.md)
- [State and security requirements](docs/state-and-security.md)
- [Debian package contract](docs/debian-package.md)
- [Package boundaries](docs/package-boundaries.md)
- [Networking and reliability requirements](docs/networking-reliability.md)
- [Subscriptions and profiles](docs/subscriptions-and-profiles.md)
- [Roadmap](docs/roadmap.md)
- [Development guide](docs/development.md)
- [References](docs/references.md)
- [tunwarden(1)](docs/man/tunwarden.1)
- [tunwardend(8)](docs/man/tunwardend.8)

## License

TunWarden is licensed under the MIT License. See [LICENSE](LICENSE).
