# Roadmap

## Phase 0: foundation

- CLI and daemon skeleton.
- Diagnostic command contract.
- Emergency reset command contract.
- Network transaction model.
- CI.

## Phase 1: read-only Linux diagnostics

- Inspect default route and default interface.
- Detect NetworkManager and systemd-resolved.
- Inspect DNS mode.
- Detect nftables availability.
- Detect stale TunWarden-owned resources.

## Phase 2: safe proxy-only mode

- Start Xray as a managed engine.
- Generate local SOCKS/HTTP inbound config.
- Add status and logs.
- Keep system routes untouched.

## Phase 3: TUN full-tunnel mode

- Create `tunwarden0`.
- Snapshot routes, rules, DNS, nftables.
- Apply transaction plan.
- Verify route to VPN server bypasses TUN.
- Roll back on failure.

## Phase 4: subscriptions

- Parse URI lists.
- Parse base64 subscriptions.
- Normalize VLESS, VMess, Trojan, and Shadowsocks into internal profiles.
- Add subscription refresh.

## Phase 5: resilience

- systemd watchdog integration.
- NetworkManager event integration.
- sleep/resume handling.
- crash recovery from `/run/tunwarden` state.

## Phase 6: additional engines

- AmneziaWG engine abstraction.
- Optional sing-box compatibility experiments.
