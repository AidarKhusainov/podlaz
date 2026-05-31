# TunWarden documentation

This directory is the source of truth for TunWarden product and engineering documentation.

TunWarden is a Linux-first, CLI-first VPN/proxy client for Xray-compatible configurations. The project prioritizes safe Linux networking, explicit recovery, and deterministic behavior for technical users over early GUI features or broad protocol expansion.

## Documentation ownership rules

- Use lowercase kebab-case filenames for canonical documents.
- Keep one document responsible for one concern.
- Do not duplicate the same requirement in multiple files unless one file links to the canonical source.
- If code behavior changes, update the related requirement or roadmap section in the same pull request.
- Architecture and networking rules are requirements, not implementation notes.
- Historical uppercase documents are deprecated and must not be used as canonical references.

## Canonical documentation map

| Document | Purpose |
| --- | --- |
| [Product requirements](./product-requirements.md) | Product thesis, target users, scope, functional requirements, non-functional requirements, success metrics. |
| [Architecture](./architecture.md) | CLI/daemon split, privilege boundary, state model, transaction model, engine abstraction, backend interfaces. |
| [Networking and reliability requirements](./networking-reliability.md) | TUN, routing, DNS, nftables, NetworkManager, sleep/resume, health checks, panic reset, reliability tests. |
| [Subscriptions and profiles](./subscriptions-and-profiles.md) | Subscription inputs, format adapters, normalized profile model, validation, update behavior, storage. |
| [Roadmap](./roadmap.md) | Ordered implementation phases and milestone boundaries. |
| [Development guide](./development.md) | Local checks, contribution rules, safety constraints, documentation update rules. |
| [References](./references.md) | External technical references and the assumptions derived from them. |

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
2. **Rollback first.** Cleanup and panic reset must exist before advanced full-tunnel features are considered stable.
3. **CLI-first.** The first UX is a stable command-line tool.
4. **Daemon-owned privilege.** Privileged networking belongs in a root daemon, not in a SUID GUI/client binary.
5. **Observable by default.** Users must be able to inspect routes, DNS, firewall state, core process status, and connection health.
6. **Linux networking is dynamic.** Sleep/resume, Wi-Fi roaming, DHCP changes, DNS changes, and interface changes are normal events.
7. **NetworkManager connectivity is advisory.** Desktop connectivity indicators may be wrong while the VPN data path still works; TunWarden must run independent health checks.
8. **Small reliable core before convenience.** Proxy-only and safe TUN foundations come before GUI, auto-select, complex routing UI, and additional engines.

## Suggested command shape

```bash
# subscriptions
tunwarden subscription add personal https://example.com/sub
tunwarden subscription update personal
tunwarden subscription list

# profiles
tunwarden profile list
tunwarden profile show personal/us-1

# connection lifecycle
tunwarden connect personal/us-1
tunwarden disconnect
tunwarden status
tunwarden doctor
tunwarden logs

# safety
tunwarden plan personal/us-1
tunwarden panic-reset
```

## Definition of done for early development

The first implementation is not ready until the following are true:

- `tunwarden panic-reset` can restore networking after an interrupted connection attempt.
- `tunwarden doctor` can explain route, DNS, TUN, firewall, core, daemon, and NetworkManager status.
- The daemon can survive or recover from core process crashes.
- The connection can be re-established after suspend/resume and Wi-Fi reconnection.
- A failed connection attempt cannot leave stale routes, rules, DNS settings, nftables rules, generated configs, or core processes behind.
