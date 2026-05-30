# TunWarden Documentation

TunWarden is planned as a Linux-first, CLI-first VPN/proxy client focused on safe and reliable Xray operation on desktop Linux, starting with Ubuntu.

The project is intentionally documentation-first. Before implementing privileged networking code, we define the invariants, reliability expectations, and failure handling rules that the implementation must satisfy.

## Documentation map

- [Product Requirements](./product-requirements.md) — target users, scope, user stories, MVP boundaries.
- [Architecture](./architecture.md) — daemon/CLI split, engines, state model, privilege boundary.
- [Networking and Reliability Requirements](./networking-reliability.md) — TUN, routing, DNS, NetworkManager, sleep/resume, rollback, health checks.
- [Subscriptions and Profiles](./subscriptions-and-profiles.md) — supported subscription families, normalized profile model, import rules.
- [Roadmap](./roadmap.md) — phased development plan.
- [References](./references.md) — external technical references used by the requirements.

## Core product thesis

TunWarden should not be “another Xray GUI”. It should be a Linux networking tool that treats VPN activation as a reversible transaction.

The primary value proposition is:

> A Linux-first Xray client that does not leave the machine without working networking after crashes, sleep/resume, Wi-Fi changes, DNS changes, or failed connections.

## Initial platform scope

### Tier 1

- Ubuntu LTS desktop
- Debian stable desktop
- NetworkManager
- systemd
- systemd-resolved
- nftables
- iproute2

### Tier 2

- Fedora Workstation
- Arch Linux
- systemd-networkd where practical

### Out of initial scope

- GUI
- mobile platforms
- Windows/macOS
- router distributions
- non-systemd Linux distributions
- complex enterprise policy management

## Non-negotiable design principles

1. **No blind system mutation.** Every privileged networking operation must be planned, logged, and reversible.
2. **Rollback first.** The project must implement cleanup and panic reset before adding advanced modes.
3. **CLI-first.** The first UX is a stable command-line tool, not a graphical shell.
4. **Daemon-owned privilege.** Privileged networking belongs in a root daemon, not in a SUID GUI/client binary.
5. **Observable by default.** Users must be able to inspect routes, DNS, firewall state, core process status, and connection health.
6. **Linux networking is dynamic.** Sleep/resume, Wi-Fi roaming, DHCP changes, DNS changes, and interface changes are normal events, not edge cases.
7. **NetworkManager connectivity is advisory.** Desktop connectivity indicators may be wrong while the VPN data path still works; TunWarden must run its own health checks.

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
- `tunwarden doctor` can explain route, DNS, TUN, firewall, and core status.
- The daemon can survive or recover from core process crashes.
- The connection can be re-established after suspend/resume and Wi-Fi reconnection.
- A failed connection attempt cannot leave stale routes, rules, DNS settings, or nftables rules behind.
