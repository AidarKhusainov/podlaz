# podlaz documentation

This directory contains the stable product and engineering contracts for podlaz.

Documentation describes user-visible behavior, safety invariants, filesystem layout, API contracts, packaging behavior, and development rules. It is not a progress log, implementation inventory, pending verification record, or generated status report.

## Documentation rules

- Keep one document responsible for one concern.
- Prefer stable contracts over milestone/status wording.
- Do not duplicate command shapes, paths, package layout, or security rules across documents; link to the canonical owner instead.
- Keep temporary progress, pending verification, and release evidence in issues, pull requests, release notes, or external redacted records instead of permanent reference documentation.
- Update the canonical document in the same pull request as the behavior change.
- Manual pages are concise installed references. They are not a second source of truth for unstable implementation details.

## User-facing contracts

| Document | Purpose |
| --- | --- |
| [CLI contract](./cli.md) | Command names, arguments, flags, exit codes, output expectations, JSON compatibility, and safety semantics. |
| [Local import formats](./local-import-formats.md) | One-shot local Xray JSON and URI-list import format detection, normalization, unsupported-entry behavior, and safety boundary. |
| [Shell completion](./shell-completion.md) | `podlaz completion` behavior, shell-specific package files, static completion scope, and safety boundary. |
| [Profile management](./profile-management.md) | `podlaz profile` behavior, storage, validation, output, and safety boundary. |
| [Subscription management](./subscription-management.md) | `podlaz subscription` behavior, update safety, redaction, and local state ownership. |
| [Proxy-only plan](./proxy-only-plan.md) | Read-only proxy-only planning and generated Xray config inspection contract. |
| [TUN full-tunnel plan](./tun-full-tunnel-plan.md) | Full-tunnel plan output, ownership model, DNS/firewall plan, warnings, and rollback model. |
| [Proxy-only lifecycle](./proxy-only-lifecycle.md) | Daemon-managed proxy-only connect/disconnect and Xray runtime lifecycle. |
| [Status command](./status.md) | Read-only daemon-backed and fallback `podlaz status` behavior. |
| [Doctor diagnostics](./doctor-diagnostics.md) | Read-only `podlaz doctor` diagnostics and core binary validation. |
| [Logs command](./logs.md) | journald integration, log source selection, redaction, and failure behavior. |
| [Recovery dry-run and execute](./recovery-dry-run.md) | `podlaz recover` inspection and explicit daemon-owned cleanup behavior. |

## Engineering contracts

| Document | Purpose |
| --- | --- |
| [Product requirements](./product-requirements.md) | Product problem, target users, goals, functional requirements, and non-functional requirements. |
| [Architecture](./architecture.md) | CLI/daemon boundary, daemon-mediated access model, state model, transaction model, planner/executor split, and engine abstraction. |
| [State and security requirements](./state-and-security.md) | State layout, JSON compatibility, redaction, confirmation, systemd hardening, packaged daemon socket access, and core process safety. |
| [Networking and reliability requirements](./networking-reliability.md) | TUN, routing, DNS, firewall, NetworkManager, health, rollback, and recovery invariants. |
| [System snapshot model](./system-snapshot.md) | Read-only host snapshot inputs for planning and diagnostics. |
| [nftables Firewall Executor](./nftables-firewall-executor.md) | podlaz-owned nftables apply, verify, rollback, and cleanup boundary. |
| [Subscriptions and profiles](./subscriptions-and-profiles.md) | Normalized profile/subscription model, adapters, validation, storage, and provider compatibility. |
| [Daemon local API](./daemon-api.md) | Local daemon transport, access model, lifecycle endpoints, and daemon safety boundary. |
| [Polkit authorization](./polkit-authorization.md) | Optional daemon-side polkit actions, default policy, socket-group fallback, GUI/TTY behavior, and troubleshooting. |
| [Status daemon socket classification](./status-daemon-socket.md) | Conservative status fallback behavior when daemon socket access fails. |
| [Package boundaries](./package-boundaries.md) | Package dependency direction and review rules. |

## Packaging, release, and maintenance

| Document | Purpose |
| --- | --- |
| [Debian package contract](./debian-package.md) | Local `.deb` layout, package metadata, install/remove behavior, access contract, and validation gates. |
| [Self-hosted E2E validation](./e2e.md) | Manual self-hosted E2E tiers, runner contract, secrets, sudo scope, diagnostics, and safety gates. |
| [Release workflow](./release.md) | GitHub Release automation, artifacts, version mapping, permissions, and safety boundary. |
| [Release gates](./release-gates.md) | Reusable release-gate policy and evidence rules. |
| [Roadmap](./roadmap.md) | Sequencing constraints and deferrals. It is not a repository status log. |
| [Development guide](./development.md) | Local checks, contributor rules, documentation update rules, testing strategy, and implementation preferences. |
| [References](./references.md) | External references and assumptions used by the project. |

## Manual pages

| Page | Purpose |
| --- | --- |
| [podlaz(1)](./man/podlaz.1) | User command reference installed as `man podlaz`. |
| [podlazd(8)](./man/podlazd.8) | Daemon and administrator reference installed as `man podlazd`. |

## Canonical ownership

- CLI behavior is owned by [CLI contract](./cli.md).
- CLI/daemon separation and daemon-mediated access are owned by [Architecture](./architecture.md).
- Filesystem layout, output redaction, confirmation behavior, systemd hardening, and core process safety are owned by [State and security requirements](./state-and-security.md).
- Linux networking invariants are owned by [Networking and reliability requirements](./networking-reliability.md).
- Package layout, package lifecycle, and package validation boundaries are owned by [Debian package contract](./debian-package.md).
- Package dependency direction is owned by [Package boundaries](./package-boundaries.md).
