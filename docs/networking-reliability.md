# Networking and Reliability Requirements

## 1. Purpose

This document defines the networking invariants TunWarden must preserve.

The main requirement is simple:

> TunWarden must not leave the user's Linux machine without recoverable networking.

VPN correctness is not enough. Cleanup, rollback, recovery, and diagnostics are part of the product.

## 2. Supported initial environment

Initial target:

- Ubuntu LTS desktop,
- Debian stable desktop,
- NetworkManager,
- systemd,
- systemd-resolved,
- nftables,
- iproute2,
- Linux TUN device.

The implementation must be structured so Fedora and Arch can be added without rewriting core logic.

## 3. Networking modes

### 3.1 Proxy-only mode

Proxy-only mode must start first because it is the safest mode.

Expected behavior:

- no default route changes,
- no TUN interface,
- no nftables redirect,
- local SOCKS/HTTP/mixed inbound if supported,
- useful for validating profiles and subscriptions.

### 3.2 TUN full-tunnel mode

TUN mode is the primary VPN-like mode.

Expected behavior:

- create a stable TUN interface, for example `tunwarden0`,
- route general traffic through the TUN interface,
- route the VPN server itself outside the TUN interface,
- configure DNS intentionally,
- prevent traffic loops,
- clean up on disconnect/crash.

The current foundation `plan --mode tun` implementation is still read-only, but it now produces a full-tunnel TUN/route dry-run plan from the current system snapshot. It shows intended TUN device, route, policy-rule, VPN server bypass, route-loop warning, and rollback output without creating TUN devices or mutating host routes/rules. DNS, nftables/firewall, kill-switch, health-check apply plans, and actual TUN execution remain future daemon-owned transaction work.

### 3.3 Split-tunnel mode

Split-tunnel mode is future scope.

It must be implemented only after full-tunnel mode has strong diagnostics and rollback.

## 4. Required invariants

### INV-001: Server route must bypass VPN TUN

Traffic to the active proxy/VPN server must not be routed through `tunwarden0`.

A health check must verify:

```bash
ip route get <server-ip>
```

The result must use the physical/default uplink, not the TunWarden TUN interface.

For hostname profile servers, read-only snapshot collection must resolve the hostname under a bounded timeout before performing server-route lookup. DNS resolution failure must be reported as incomplete visibility instead of being hidden by planner defaults.

### INV-002: TunWarden-owned state must be identifiable

Routes, rules, nftables tables/chains, generated config files, and runtime state must be identifiable as TunWarden-owned.

Naming examples:

```text
tunwarden0
fwmark 0x... documented range
routing table name/id reserved for TunWarden
nft table inet tunwarden
/run/tunwarden/*
```

### INV-003: Cleanup must be idempotent

Running cleanup multiple times must be safe.

This applies to:

- TUN deletion,
- route deletion,
- rule deletion,
- nftables cleanup,
- DNS rollback,
- core process termination,
- transaction file cleanup.

### INV-004: Failed connection must not damage direct connectivity

If connection setup fails and strict kill-switch is not enabled, TunWarden must attempt to restore direct connectivity.

### INV-005: NetworkManager connectivity state is not the only health signal

NetworkManager or desktop UI may show limited connectivity while application traffic still works through VPN.

TunWarden must run its own health checks and expose both states separately.

## 5. TUN requirements

### TUN-001: Stable interface name

Use a stable interface name for diagnostics.

Initial candidate:

```text
tunwarden0
```

The current dry-run plan must show the intended stable TUN interface before any execution work is implemented.

### TUN-002: MTU must be configurable

Default MTU should be conservative.

The actual default must be validated during implementation, but the config must allow overrides. The current dry-run plan may show the planner's initial MTU assumption without applying it.

### TUN-003: IPv6 must be explicit

IPv6 must not be accidentally half-enabled.

Initial default:

```text
IPv6 disabled or bypassed until full IPv6 routing/DNS leak handling is implemented.
```

### TUN-004: TUN lifecycle must be daemon-owned

The daemon must own TUN creation and deletion.

The CLI must not create TUN devices directly. The current CLI `plan --mode tun` may describe a future daemon-owned TUN create step only as dry-run output.

## 6. Routing requirements

### RT-001: Use policy routing where appropriate

Full-tunnel mode should use deterministic routing state rather than ad-hoc default route replacement.

The current dry-run planner shows intended policy-rule state before applying anything.

### RT-002: Dedicated routing table

TunWarden should use a dedicated routing table ID/name.

Initial dry-run values:

```text
Table: tunwarden
ID: 51820
```

### RT-003: Route visibility and route-change planning must be inspectable

`tunwarden plan --mode tun <profile>` must show route visibility inputs before any TUN mutation work:

- default IPv4/IPv6 route state;
- default interface when detected;
- server route after resolving hostname servers to a concrete IP address;
- warning when the server route is unknown or would loop through `tunwarden0`.

It must also show intended full-tunnel route and policy-rule desired state without applying it:

- default IPv4 route through the TunWarden routing table;
- policy rule that sends default IPv4 traffic through the TunWarden table;
- VPN server bypass route and policy rule only when the current snapshot provides a concrete server IP;
- blocked/incomplete server-bypass output and warnings when hostname resolution or route lookup does not produce a concrete server IP;
- rollback steps for planned routes and policy rules.

The current dry-run plan must not claim to apply route changes. It is inspectable planner output only.

### RT-004: Default interface must be re-detected

On Wi-Fi reconnect, DHCP change, resume, or default route change, TunWarden must re-detect:

- uplink interface,
- gateway,
- server route,
- DNS state,
- IPv4/IPv6 availability.

### RT-005: Route loop prevention

TunWarden must detect and reject a plan where:

```text
VPN server route -> tunwarden0
```

unless an advanced nested mode is explicitly implemented in the future.

The current dry-run planner must surface this as route-loop risk in human and JSON output.

## 7. DNS requirements

### DNS-001: Prefer systemd-resolved backend on Ubuntu/Debian

Initial DNS integration should use systemd-resolved when available.

### DNS-002: Do not blindly overwrite /etc/resolv.conf

Direct edits to `/etc/resolv.conf` are not allowed in normal operation.

Fallback handling may be added later, but must be explicit and documented.

### DNS-003: Full-tunnel DNS must be per-link where possible

For full-tunnel mode, TunWarden should use per-link DNS on `tunwarden0` where possible.

Candidate behavior:

```bash
resolvectl dns tunwarden0 <dns-server>
resolvectl domain tunwarden0 '~.'
resolvectl default-route tunwarden0 yes
```

The current `plan --mode tun` implementation reports DNS snapshot visibility and warnings, but DNS apply planning remains future scope.

### DNS-004: Bootstrap DNS must avoid loops

DNS needed to resolve the VPN server must not depend on the VPN tunnel that is not established yet.

### DNS-005: Remote DNS through proxy

When the profile uses remote DNS, remote DNS should go through the proxy/VPN path where supported.

### DNS-006: DNS diagnostics

`tunwarden doctor` must report:

- active DNS backend,
- DNS servers by link,
- domains by link,
- whether `~.` is active,
- whether DNS resolution works,
- whether server bootstrap DNS can work without a loop.

## 8. Firewall and kill-switch requirements

### FW-001: nftables first

Initial implementation should use nftables.

iptables fallback is future scope.

### FW-002: TunWarden-owned table

TunWarden must use a clearly named nftables table, for example:

```text
nft table inet tunwarden
```

The current `plan --mode tun` implementation reports nftables availability and TunWarden table presence, but nftables/firewall apply planning remains future scope.

### FW-003: Kill-switch modes

Kill-switch must have explicit modes:

```text
off
soft
strict
```

Suggested semantics:

- `off`: no kill-switch; rollback restores direct connectivity.
- `soft`: prevent accidental leaks during transition but restore direct connectivity on failure.
- `strict`: block non-VPN traffic if VPN fails, except recovery/control traffic.

### FW-004: Recovery must override kill-switch

`recover --execute --yes` must remove TunWarden-owned kill-switch rules even in strict mode.

## 9. Sleep/resume requirements

### SR-001: Resume is a normal lifecycle event

Suspend/resume must not be treated as an edge case.

### SR-002: Before sleep

TunWarden should:

- pause aggressive reconnect loops,
- mark active profile,
- optionally stop/release volatile state if needed,
- avoid leaving half-applied transactions.

### SR-003: After resume

TunWarden must:

- wait for network availability or relevant NetworkManager events,
- re-detect default route/interface,
- re-resolve server address,
- recreate or validate TUN,
- re-apply DNS/routing/firewall state,
- restart or reconfigure core,
- run health checks.

### SR-004: No stale assumptions

After resume, TunWarden must not assume:

- same Wi-Fi network,
- same gateway,
- same DNS servers,
- same DHCP lease,
- same default interface,
- same IPv6 state.

## 10. NetworkManager requirements

### NM-001: Listen for relevant network events

TunWarden must react to events equivalent to:

- up,
- down,
- DHCPv4 change,
- DHCPv6 change,
- DNS change,
- connectivity change,
- default route change.

### NM-002: Dispatcher scripts must be lightweight

If NetworkManager dispatcher scripts are used, they must only notify the daemon and exit quickly.

They must not perform heavy networking operations directly.

### NM-003: Connectivity state is diagnostic, not authoritative

NetworkManager connectivity state should be shown in diagnostics but must not be the only criterion for reconnecting or rolling back.

## 11. Health checks

`tunwarden doctor` must include these checks.

### Core checks

- daemon running,
- core process running,
- core config generated,
- core logs available.

### TUN checks

- TUN exists,
- interface is up,
- addresses assigned,
- MTU configured.

### Routing checks

- policy rule exists,
- routing table exists,
- default route points as expected,
- VPN server route bypasses TUN,
- LAN bypass works if configured.

### DNS checks

- DNS backend detected,
- per-link DNS configured,
- resolution works,
- bootstrap DNS is not looped.

### Firewall checks

- nftables table exists when expected,
- kill-switch state matches config,
- no stale TunWarden-owned table exists after disconnect.

### Connectivity checks

- TCP probe,
- optional UDP probe,
- optional HTTP probe,
- NetworkManager connectivity state shown separately.

## 12. Recovery requirements

`recover` must be designed as an emergency recovery command.

Default behavior:

```bash
tunwarden recover
```

This must be a read-only recovery plan.

Explicit cleanup behavior:

```bash
tunwarden recover --execute --yes
```

This should:

- stop TunWarden daemon-managed core processes,
- delete TunWarden TUN interfaces,
- remove TunWarden routes and rules,
- remove TunWarden nftables state,
- revert TunWarden DNS settings where possible,
- clean `/run/tunwarden`,
- print what changed and what could not be changed.

It must be safe to run when TunWarden is disconnected.

## 13. Reliability tests

Required tests before declaring TUN mode stable:

1. Connect/disconnect 100 times without stale state.
2. Kill core process during active connection.
3. Kill daemon during connection apply.
4. Fail DNS apply step and verify rollback.
5. Fail route apply step and verify rollback.
6. Suspend/resume while connected.
7. Change Wi-Fi network while connected.
8. Renew DHCP while connected.
9. Enable/disable IPv6 while connected.
10. Run `recover --execute --yes` after simulated crash.

## 14. Design warning

A VPN client that can connect but cannot reliably disconnect is not acceptable.

Disconnect, rollback, and recovery are core features, not maintenance tasks.
