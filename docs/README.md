# TunWarden documentation

This directory is the source of truth for TunWarden product and engineering documentation.

TunWarden is a Linux-first, CLI-first VPN/proxy client for Xray-compatible configurations. The project prioritizes safe Linux networking, explicit recovery, and deterministic behavior for technical users over early GUI features or broad protocol expansion.

## Documentation ownership rules

- Use lowercase kebab-case filenames for canonical documents.
- Keep one document responsible for one concern.
- Do not duplicate the same requirement in multiple files unless one file links to the canonical source.
- If code behavior changes, update the related requirement or roadmap section in the same pull request.
- Architecture and networking rules are requirements, not implementation notes.
- CLI command names, arguments, flags, and milestone boundaries are owned by [CLI contract](./cli.md).
- Implemented manual and share-URI `tunwarden profile` behavior is owned by [Profile management](./profile-management.md).
- Implemented Base64 URI-list `tunwarden subscription` behavior is owned by [Subscription management](./subscription-management.md).
- Implemented `tunwarden plan --mode proxy-only` behavior is owned by [Proxy-only plan](./proxy-only-plan.md).
- Implemented `tunwarden plan --mode tun` snapshot input behavior is owned by [System snapshot model](./system-snapshot.md).
- Implemented `tunwarden plan --mode tun` full-tunnel route/TUN dry-run behavior is owned by [TUN full-tunnel dry-run plan](./tun-full-tunnel-plan.md).
- Implemented `tunwarden connect --mode proxy-only` and `tunwarden disconnect` behavior is owned by [Proxy-only lifecycle](./proxy-only-lifecycle.md).
- The implemented v0.1 daemon transport is owned by [Daemon local API](./daemon-api.md).
- Implemented daemon-owned nftables firewall execution is owned by [nftables Firewall Executor](./nftables-firewall-executor.md).
- `tunwarden status`'s implemented daemon-backed and local fallback behavior is owned by [Status command](./status.md).
- `tunwarden status` daemon socket fallback classification is owned by [Status daemon socket classification](./status-daemon-socket.md).
- `tunwarden doctor`'s implemented daemon-backed and local fallback diagnostic behavior is owned by [Doctor diagnostics](./doctor-diagnostics.md).
- `tunwarden logs`'s implemented journald-backed daemon log behavior is owned by [Logs command](./logs.md).
- `tunwarden recover`'s implemented local dry-run scan is owned by [Recovery dry-run](./recovery-dry-run.md).
- The v0.1 proxy-only release-gate checklist is owned by [v0.1 proxy-only acceptance checklist](./v0.1-acceptance.md).
- The v0.2 safe TUN preview release-gate checklist is owned by [v0.2 safe TUN preview acceptance checklist](./v0.2-acceptance.md).
- The v0.2 safe TUN preview release-gate result is recorded in [v0.2 safe TUN preview acceptance verification record](./v0.2-acceptance-record.md).
- Filesystem layout, output redaction, JSON compatibility, confirmation behavior, systemd hardening, and core process safety are owned by [State and security requirements](./state-and-security.md).
- Local Debian package layout and lifecycle behavior are owned by [Debian package contract](./debian-package.md).
- GitHub Release artifact automation is owned by [Release workflow](./release.md).
- Package dependency direction is owned by [Package boundaries](./package-boundaries.md).
- Local manual pages are concise user/admin references. They must link back to canonical docs instead of becoming a second source of truth for unstable implementation details.
- Historical uppercase documents are deprecated and must not be used as canonical references.

## Canonical documentation map

| Document | Purpose |
| --- | --- |
| [Product requirements](./product-requirements.md) | Product thesis, target users, scope, functional requirements, non-functional requirements, success metrics. |
| [CLI contract](./cli.md) | Canonical command names, arguments, flags, output expectations, safety semantics, and milestone boundaries. |
| [Profile management](./profile-management.md) | Implemented v0.1 manual profile add, share URI profile import, list, show, delete, validation, storage, JSON output, and safety boundary. |
| [Subscription management](./subscription-management.md) | Implemented v0.1 Base64 URI-list subscription add, list, show, update, share URI import, JSON output, redaction, and safety boundary. |
| [Proxy-only plan](./proxy-only-plan.md) | Implemented v0.1 read-only proxy-only planning, generated Xray config validation, local proxy listeners, JSON output, and safety boundary. |
| [System snapshot model](./system-snapshot.md) | Implemented read-only TUN planning snapshot model, hostname server-route resolution, fake snapshots, status vocabulary, and safety boundary. |
| [TUN full-tunnel dry-run plan](./tun-full-tunnel-plan.md) | Implemented read-only full-tunnel TUN/route planner, policy rules, VPN server bypass, loop-risk warnings, rollback steps, JSON output, and safety boundary. |
| [Proxy-only lifecycle](./proxy-only-lifecycle.md) | Implemented v0.1 daemon-managed Xray process lifecycle for `connect --mode proxy-only`, `disconnect`, generated runtime config cleanup, and safety boundary. |
| [Daemon local API](./daemon-api.md) | Implemented v0.1 Unix socket daemon API transport, status and doctor endpoints, lifecycle, and safety boundary. |
| [nftables Firewall Executor](./nftables-firewall-executor.md) | Implemented nftables apply, verification, rollback, ownership boundary, kill-switch rule mapping, and disconnect cleanup for TUN transactions. |
| [Status command](./status.md) | Implemented v0.1 read-only `tunwarden status` daemon-backed behavior, local fallback behavior, output shape, and safety boundary. |
| [Status daemon socket classification](./status-daemon-socket.md) | Packaged daemon socket access model consequences for `tunwarden status` fallback classification and permission-denied behavior. |
| [Doctor diagnostics](./doctor-diagnostics.md) | Implemented v0.1 read-only `tunwarden doctor` daemon-backed behavior, local fallback checks, severities, and stale resource detection boundaries. |
| [Logs command](./logs.md) | Implemented v0.1 read-only `tunwarden logs` journald integration, daemon log source, redaction, and failure behavior. |
| [Recovery dry-run](./recovery-dry-run.md) | Implemented v0.1 read-only `tunwarden recover` candidate scan, output shape, and safety boundary. |
| [v0.1 acceptance checklist](./v0.1-acceptance.md) | Manual release-gate checklist for validating the proxy-only technical preview on a Tier 1 Linux host without host networking mutation. |
| [v0.2 acceptance checklist](./v0.2-acceptance.md) | Manual release-gate checklist for validating the safe TUN preview on a Tier 1 Linux host, including success, rollback, cleanup, and recovery dependency evidence. |
| [v0.2 acceptance record](./v0.2-acceptance-record.md) | Redacted release-gate verification record for issue #31; remains `PENDING` until the exact commit passes on a Tier 1 Linux host. |
| [Architecture](./architecture.md) | CLI/daemon split, privilege boundary, state model, transaction model, engine abstraction, backend interfaces. |
| [State and security requirements](./state-and-security.md) | User/daemon/system state separation, XDG/systemd paths, JSON compatibility, redaction, confirmations, service hardening, and core process safety. |
| [Debian package contract](./debian-package.md) | Local `.deb` artifact layout, install/upgrade/remove lifecycle, service auto-start policy, and package validation gates. |
| [Release workflow](./release.md) | Tagged GitHub Release automation, artifact naming, version mapping, permissions, validation gates, and safety boundary. |
| [Package boundaries](./package-boundaries.md) | Dependency direction between CLI, daemon, API, domain, planner, snapshot, executor, and adapter packages. |
| [Networking and reliability requirements](./networking-reliability.md) | TUN, routing, DNS, firewall, NetworkManager, sleep/resume, health checks, recovery, and reliability test requirements. |
| [Subscriptions and profiles](./subscriptions-and-profiles.md) | Subscription inputs, format adapters, normalized profile model, validation, update behavior, storage. |
| [Roadmap](./roadmap.md) | Ordered implementation phases and milestone boundaries. |
| [Development guide](./development.md) | Local checks, contribution rules, safety constraints, documentation update rules. |
| [References](./references.md) | External technical references and the assumptions derived from them. |
| [tunwarden(1)](./man/tunwarden.1) | Local CLI manual page installed as `man tunwarden` by packages. |
| [tunwardend(8)](./man/tunwardend.8) | Local daemon/admin manual page installed as `man tunwardend` by packages. |

## Product thesis

TunWarden should not be “another Xray GUI”. It should be a Linux networking tool that treats VPN activation as a reversible transaction.

The primary value proposition is:

> A lightweight Linux-first Xray client that does not leave the machine without recoverable networking after crashes, failed connects, sleep/resume, Wi-Fi changes, DNS changes, or route changes.

## Initial platform scope

### Tier 1

- Ubuntu LTS desktop.
- Debian stable desktop.
- systemd.
- NetworkManager.
- systemd-resolved.
- nftables.
- iproute2.
- Linux TUN device.

### Tier 2

- Fedora Workstation.
- Arch Linux.
- systemd-networkd where practical.

### Out of initial scope

- GUI.
- Mobile platforms.
- Windows/macOS.
- Router distributions.
- Non-systemd Linux distributions.
- Complex enterprise policy management.

## Non-negotiable design principles

1. **No blind system mutation.** Every privileged networking operation must be planned, logged, and reversible.
2. **Rollback and recovery first.** Cleanup and recovery must exist before advanced full-tunnel features are considered stable.
3. **CLI-first.** The first UX is a stable command-line tool.
4. **Daemon-owned privilege:** Privileged networking belongs in the daemon, not in a SUID GUI/client binary.
5. **Observable by default.** Users must be able to inspect routes, DNS, firewall state, core process status, and connection health while respecting the documented output policy.
6. **Linux networking is dynamic.** Sleep/resume, Wi-Fi roaming, DHCP changes, DNS changes, and interface changes are normal events.
7. **NetworkManager connectivity is advisory.** Desktop connectivity indicators may be wrong while the VPN data path still works; TunWarden must show NetworkManager connectivity state as diagnostic metadata but run independent health checks.
8. **Small reliable core before convenience.** Proxy-only and safe TUN foundations come before GUI, auto-select, complex routing UI, and additional engines.

## Canonical command shape

The canonical command contract is maintained in [CLI contract](./cli.md). Short examples:

```bash
# first-run import
tunwarden import <uri-or-file-or-url>

# explicit resources
tunwarden profile add --name test --server example.com --port 443 --protocol vless
tunwarden profile import '<share-uri>'
tunwarden profile list
tunwarden profile show <profile-id>
tunwarden subscription add --name personal --url file:///tmp/sub.txt
tunwarden subscription list
tunwarden subscription update <subscription-id>

# connection lifecycle
tunwarden connect [--mode proxy-only] <profile-id>
tunwarden connect --mode tun <profile-id>
tunwarden disconnect
tunwarden status
tunwarden doctor
tunwarden logs

# safety and recovery
tunwarden plan --mode proxy-only <profile-id>
tunwarden plan --mode tun <profile-id>
tunwarden recover
tunwarden recover --execute --yes

# local package build and install
./scripts/build-deb.sh
sudo apt install ./dist/tunwarden_0.0.0~dev_amd64.deb

# local reference documentation
man tunwarden
man tunwardend
```

## Definition of done for early development

The first implementation is not ready until the following are true:

- `tunwarden recover` can inspect recovery candidates after an interrupted connection attempt.
- `tunwarden doctor` can explain route, DNS, TUN, firewall, core, daemon, and NetworkManager status.
- The daemon can survive or recover from core process crashes.
- The connection can be re-established after suspend/resume and Wi-Fi reconnection.
- A failed connection attempt cannot leave stale routes, rules, DNS settings, nftables rules, generated configs, or core processes behind.
