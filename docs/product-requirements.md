# Product Requirements

## 1. Problem statement

Linux users who rely on Xray-compatible VPN/proxy configurations often experience unstable desktop networking when clients modify TUN interfaces, routes, DNS, firewall rules, or system proxy settings without robust recovery.

Typical failure modes include:

- Wi-Fi appears connected, but applications lose connectivity.
- GNOME/NetworkManager shows a question mark or limited connectivity state.
- DNS stops resolving after disconnecting the client.
- Routes or policy rules remain after the VPN process exits.
- nftables/iptables rules remain after crashes.
- VPN stops after suspend/resume.
- Reconnecting Wi-Fi is required to restore the machine.
- The VPN server route accidentally goes through the VPN TUN interface, causing a traffic loop.

podlaz exists to make these failure modes explicit, testable, and recoverable.

## 2. Product thesis

podlaz is a Linux-first, CLI-first VPN/proxy client for Xray-compatible configurations.

The project should prioritize:

1. reliable networking,
2. safe rollback,
3. clear diagnostics,
4. subscription/profile interoperability,
5. minimal but powerful UX for technical users.

The project should not initially prioritize:

1. GUI,
2. visual profile management,
3. mobile support,
4. broad protocol coverage at the expense of reliability,
5. hiding networking details from advanced users.

## 3. Target users

### Primary users

- Linux desktop users on Ubuntu/Debian.
- Backend developers, DevOps engineers, sysadmins, and technical power users.
- Users who understand CLI tools and want deterministic behavior.
- Users who import subscriptions from panels such as Remnawave, 3x-ui, and similar Xray-compatible systems.

### Secondary users

- Linux users who want a future GUI but can start with CLI.
- Users who need stable VPN behavior across laptop suspend/resume.
- Users who want a safe alternative to ad-hoc scripts.

## 4. Product goals

### G1. CLI-first operation

podlaz must be fully usable through CLI commands.

The canonical command contract is maintained in [CLI contract](./cli.md). Required command families:

```bash
podlaz import <uri-or-file-or-url>
podlaz profile ...
podlaz subscription ...
podlaz status
podlaz connect <profile-id>
podlaz disconnect
podlaz doctor
podlaz logs
podlaz plan --mode <proxy-only|tun> <profile-id>
podlaz recover
```

### G2. Safe privileged networking

The project must never rely on a privileged GUI process or SUID frontend for normal operation.

Required model:

```text
unprivileged CLI/user client
  -> Unix socket or D-Bus
privileged daemon
  -> TUN, routes, DNS, nftables, core lifecycle
```

### G3. Transactional network changes

Every privileged VPN activation must be treated as a transaction:

```text
plan -> snapshot -> apply -> verify -> commit
                             ↓
                          rollback
```

podlaz must keep enough state to recover after failed attempts and crashes.

### G4. Reliable laptop behavior

podlaz must handle:

- suspend/resume,
- Wi-Fi reconnect,
- default interface change,
- DHCP renewal,
- DNS change,
- NetworkManager connectivity changes,
- core process crash.

### G5. Observable diagnostics

podlaz must explain its decisions.

Examples:

```bash
podlaz plan --mode tun my-profile
podlaz doctor
podlaz doctor --routes
podlaz doctor --dns
podlaz doctor --firewall
```

## 5. Scope and sequencing

The product has two early technical milestones. This avoids mixing a safe proxy-only preview with the higher-risk full TUN implementation.

### v0.1.0: proxy-only technical preview

Included:

- Linux only.
- CLI only.
- Daemon managed by systemd.
- Local IPC between CLI and daemon.
- Xray core lifecycle management.
- Manual node import for VLESS, VMess, Trojan, Shadowsocks where feasible.
- Base64 subscription import.
- Proxy-only mode.
- `status`, `logs`, `doctor`, `plan --mode proxy-only`, and dry-run `recover` basics.
- No system route, DNS, firewall, or TUN mutation.

Exit expectation:

- A user can import a profile/subscription, start Xray in proxy-only mode, inspect status/logs, preview the proxy-only runtime plan, and stop it cleanly.

### v0.2.0: safe TUN preview

Included:

- TUN full-tunnel mode.
- systemd-resolved DNS backend.
- NetworkManager event integration.
- nftables-based firewall/kill-switch foundation.
- Transaction apply/verify/commit/rollback.
- `plan --mode tun` dry-run output.
- `doctor` diagnostics for route/DNS/TUN/firewall/core state.
- `recover --execute --yes` cleanup of podlaz-owned state.

Exit expectation:

- Failed connection attempts roll back, disconnect leaves no podlaz-owned networking state, and `recover --execute --yes` can recover from common broken states.

### Excluded from early milestones

- GUI.
- Windows/macOS/mobile.
- Browser extension.
- Router mode.
- Automatic core updater.
- Advanced visual routing editor.
- Enterprise device management.
- Full AmneziaWG support.

## 6. Functional requirements

### FR-001: Profile management

podlaz must support storing, listing, showing, validating, and deleting profiles.

A profile must include profile ID, display name, source type, protocol, server address, server port, security settings, transport settings, DNS policy, routing policy, and metadata.

### FR-002: Subscription management

podlaz must support adding and updating subscription sources.

A subscription source must include source ID, URL, format detection mode, update interval, last update status, imported profiles/nodes, and optional provider metadata.

### FR-003: Connection lifecycle

podlaz must support connect, disconnect, reconnect, status, graceful shutdown, and forced cleanup.

### FR-004: Dry-run planning

podlaz must support a planning mode that prints intended changes without applying them.

Example output categories:

- TUN interface changes,
- routes,
- policy rules,
- DNS changes,
- nftables changes,
- core process config,
- rollback state location.

### FR-005: Diagnostics

`podlaz doctor` must check daemon status, core process status, TUN interface status, default route, policy rules, route to VPN server, DNS configuration, DNS resolution, nftables state, NetworkManager state, external TCP connectivity, optional UDP connectivity, and stale podlaz-owned state.

Facet flags such as `doctor --core`, `doctor --routes`, `doctor --dns`, and `doctor --firewall` should be preferred over separate low-level check command families.

### FR-006: Recovery

`podlaz recover` must inspect podlaz-owned stale state even if the daemon has crashed.

`podlaz recover --execute --yes`, introduced only after safe TUN work is ready, must clean podlaz-owned TUN interfaces, policy rules, routing tables, nftables state, DNS settings where possible, core processes, and pending transaction state.

The default `recover` command must be read-only. Cleanup requires explicit `--execute --yes`.

### FR-007: Proxy-only mode

Proxy-only mode must allow validating profiles and running Xray without modifying system networking.

It must not create a TUN interface, replace the default route, mutate global DNS, install nftables redirect rules, or claim full VPN leak protection.

### FR-008: Full-tunnel mode

Full-tunnel mode must be implemented only through the network transaction model.

It must create and own a stable TUN interface, route general traffic through the TUN interface, route the VPN server outside the TUN interface, configure DNS intentionally, verify health before commit, and roll back on failure.

## 7. Non-functional requirements

### NFR-001: Safety

The project must prefer restoring direct connectivity over preserving a broken VPN tunnel, unless the user enabled strict kill-switch mode.

### NFR-002: Auditability

Every privileged change must be logged with enough detail to debug failures.

### NFR-003: Idempotency

Cleanup operations must be safe to run multiple times.

### NFR-004: Minimal assumptions

podlaz must not assume interface names are stable, Wi-Fi is the only uplink, IPv6 is available, systemd-resolved is always configured the same way, NetworkManager connectivity state is always accurate, or subscription format is always correctly declared.

### NFR-005: Testability

Core planners must be testable without root privileges by producing desired state plans from input snapshots.

### NFR-006: Lightweight operation

podlaz should avoid unnecessary resident components, polling loops, broad dependency chains, and hidden global mutation. Background work should be justified by reliability or observability.

### NFR-007: Secure defaults

podlaz must not silently accept unsafe profile settings. Risky settings such as insecure TLS, unsupported transports, ambiguous DNS behavior, and incomplete IPv6 handling must be visible to the user.

Output redaction, JSON compatibility, filesystem layout, confirmation behavior, systemd hardening, and core process safety are owned by [State and security requirements](./state-and-security.md).

## 8. Success metrics

Early success should be measured by reliability, not feature count.

Examples:

- Proxy-only mode starts and stops Xray cleanly without touching system networking.
- Connection/disconnection leaves no stale podlaz routes/rules/firewall state.
- Suspend/resume reconnects automatically on Ubuntu LTS.
- `recover --execute --yes` restores direct internet connectivity in common failure cases once safe TUN work is implemented.
- `doctor` reports actionable causes for DNS/routing failures.
- At least one common subscription source can be imported end-to-end.
