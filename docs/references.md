# References

This document tracks external technical references used while shaping TunWarden requirements.

The links are not implementation instructions by themselves. They explain the assumptions behind the initial requirements.

## Xray

### TUN inbound

Reference:

- https://xtls.github.io/config/inbounds/tun.html

Important notes for TunWarden:

- Xray has TUN inbound support.
- Linux routing must not be assumed to be fully automatic.
- Route-loop prevention is a core client responsibility.
- TunWarden should own Linux route/DNS/firewall orchestration instead of treating Xray as a full system network manager.

## sing-box

### TUN inbound

Reference:

- https://sing-box.sagernet.org/configuration/inbound/tun/

Important notes for TunWarden:

- `auto_route`, `strict_route`, and Linux `auto_redirect` are useful design references.
- `auto_redirect` is Linux-specific and nftables-oriented.
- The Throne client appears to benefit from sing-box/sing-tun behavior for TUN/routing stability.
- TunWarden can learn from this model while still remaining Xray-first.

## XDG Base Directory Specification

Reference:

- https://specifications.freedesktop.org/basedir-spec/latest/

Important notes for TunWarden:

- User-owned configuration, state, and cache should follow XDG paths.
- `$XDG_CONFIG_HOME`, `$XDG_STATE_HOME`, and `$XDG_CACHE_HOME` are the right basis for CLI-owned profile, subscription, preference, and cache files.
- Daemon-owned state should not be confused with user intent.

## systemd service execution and hardening

Reference:

- https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html

Important notes for TunWarden:

- `RuntimeDirectory=`, `StateDirectory=`, and `LogsDirectory=` are the right basis for package-managed daemon directories.
- `CapabilityBoundingSet=`, `AmbientCapabilities=`, `NoNewPrivileges=`, `ProtectSystem=`, and related service sandboxing directives should shape the daemon unit.
- TunWarden should start from least privilege and justify any relaxation needed for real networking behavior.

## Linux capabilities

Reference:

- https://man7.org/linux/man-pages/man7/capabilities.7.html

Important notes for TunWarden:

- `CAP_NET_ADMIN` is expected for interface, routing, and firewall work.
- Broad file permission bypass capabilities should not be in the default daemon baseline.
- Capability requirements should be validated and minimized during implementation.

## systemd-resolved

### Per-link DNS and routing domains

References:

- https://www.freedesktop.org/software/systemd/man/latest/systemd-resolved.service.html
- https://www.freedesktop.org/software/systemd/man/latest/resolved.conf.html

Important notes for TunWarden:

- Per-link DNS is preferable to blindly overwriting `/etc/resolv.conf`.
- The route-only domain `~.` is relevant for full-tunnel DNS behavior.
- DNS bootstrap must avoid depending on the VPN tunnel before the tunnel is established.

## NetworkManager

### Dispatcher scripts and network events

Reference:

- https://networkmanager.dev/docs/api/latest/NetworkManager-dispatcher.html

Important notes for TunWarden:

- NetworkManager dispatcher events can be used to notify the daemon about network changes.
- Dispatcher scripts should be lightweight and should not perform heavy reconnect logic directly.
- Events such as `up`, `down`, DHCP changes, DNS changes, and connectivity changes are relevant to laptop reliability.

### Connectivity state

Reference:

- https://networkmanager.dev/docs/api/latest/settings-connectivity.html

Important notes for TunWarden:

- Desktop connectivity state may not perfectly match actual VPN data-path health.
- TunWarden should show NetworkManager connectivity state as diagnostic information, but should run independent health checks.

## Remnawave

### Subscription templates

Reference:

- https://docs.rw/docs/learn-en/templates/

Important notes for TunWarden:

- Remnawave can expose multiple subscription/client formats.
- TunWarden should implement generic parsers first and provider-specific behavior later.

## 3x-ui

Reference:

- https://github.com/MHSanaei/3x-ui

Important notes for TunWarden:

- 3x-ui is an Xray panel commonly used to generate client connection/subscription data.
- TunWarden should initially treat it as a generic Xray-compatible subscription source.

## Throne

Reference:

- https://github.com/throneproj/Throne

Important notes for TunWarden:

- Throne is a useful reference for observed Linux stability.
- Its architecture is not a direct template for TunWarden because Throne is Qt-first and heavily sing-box-based.
- TunWarden should borrow the reliability lessons, not the SUID/GUI lifecycle model.

## Linux tooling expected in Tier 1

References:

- iproute2: https://wiki.linuxfoundation.org/networking/iproute2
- nftables: https://wiki.nftables.org/wiki-nftables/index.php/Main_Page
- systemd: https://www.freedesktop.org/wiki/Software/systemd/

Important notes for TunWarden:

- Tier 1 assumes modern Linux desktop networking components.
- Fallbacks should be explicit and tested, not accidental.
