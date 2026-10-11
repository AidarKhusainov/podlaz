# Podlaz

Podlaz is a command-line VPN client for Linux built on Xray-core. It routes system traffic through a TUN interface and runs privileged network operations in the `podlazd` systemd service.

[![CI](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml/badge.svg)](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AidarKhusainov/podlaz)](https://github.com/AidarKhusainov/podlaz/releases/latest)

- Import VPN profiles from share links, subscriptions, Xray JSON and supported Clash/Mihomo YAML.
- Connect in full-system VPN mode; reconnect and recover without silently dropping to Proxy-only.
- Manage profiles, subscriptions, autostart and diagnostics from the CLI.

## Install

Podlaz supports systemd-based Debian and Ubuntu installations on `amd64` and `arm64`. Packages require `libc6 >= 2.34`, `iproute2`, `nftables`, systemd and Polkit. A desktop Polkit agent may be needed to authorize VPN operations. Installed-package runtime checks run on Ubuntu 24.04 `amd64`; `arm64` packages are built and validated, but do not have equivalent native runtime coverage.

### APT repository (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/AidarKhusainov/podlaz/master/scripts/install.sh | bash
```

The installer verifies the repository signing-key fingerprint, configures a source-scoped `Signed-By` keyring, and installs Podlaz from the signed `stable` APT repository. It asks for elevated privileges for system changes.

Updates then use standard APT commands:

```bash
sudo apt update
sudo apt upgrade
```

### Direct .deb from GitHub Releases

For installation without adding an APT repository:

```bash
curl -fsSL https://raw.githubusercontent.com/AidarKhusainov/podlaz/master/scripts/install-deb.sh | bash
```

This script downloads the latest release for the system architecture, checks the package against the release's `SHA256SUMS`, and installs the local `.deb` with APT. It does **not** configure automatic updates. Rerun it to install a newer release.

You can also [download a release](https://github.com/AidarKhusainov/podlaz/releases) and install a separately verified package with `sudo apt install ./podlaz_<version>_linux_<arch>.deb`. Release assets include `SHA256SUMS`; GitHub provides separate build-provenance attestations. To verify provenance, use `gh attestation verify <package-file>.deb -R AidarKhusainov/podlaz`. A checksum served alongside the package checks integrity against that manifest; it does not independently authenticate the publisher.

Both installers are shell scripts fetched from the repository. Review them before execution. Neither performs migration between installation methods.

### Signed APT repository

Repository: `https://aidarkhusainov.github.io/podlaz/apt`

Signing-key fingerprint:

```text
7C6F4EF9CDE73501E79CCC63E2F6F280766864C2
```

Confirm the fingerprint through a trusted maintainer source independently of the repository hosting the key. The installer checks the downloaded key against this pinned value. For manual configuration, follow the deb822 `Signed-By` setup in [`scripts/install.sh`](scripts/install.sh); do not use `apt-key` or disable signature verification.

Verify the installation:

```bash
podlaz version
systemctl is-active podlazd.service
```

## Quick start

Import a share URI, a subscription URL, or a supported file:

```bash
podlaz import '<uri-or-url-or-file>'
podlaz profile list
```

If there is only one profile and none is selected, Podlaz selects it automatically. Otherwise select one from the list:

```bash
podlaz profile use work
```

Connect, check status, and disconnect:

```bash
podlaz connect
podlaz status
podlaz disconnect
```

Replace `work` with the profile name shown by `podlaz profile list`. `podlaz connect work` connects to that profile without changing the saved selection. `plz` is an alias for `podlaz`.

### Profiles, subscriptions and autostart

```bash
podlaz profile list
podlaz profile show work
podlaz subscription list
podlaz subscription update YOUR_SUBSCRIPTION_ID
podlaz autostart enable
podlaz autostart status
```

`autostart enable` stores the selected profile for future boots; changing the selected profile does not change an existing autostart configuration. Profile and subscription deletion requires confirmation (or `--yes` in non-interactive use). See [CLI reference](docs/cli.md) for the full command set.

## Supported formats and limitations

| Input | Import | Connection |
| --- | --- | --- |
| VLESS, VMess (alterId 0), Trojan TLS, Shadowsocks share URIs | Supported subsets | Full VPN or explicit Proxy-only, for supported transports |
| HTTP(S) Base64 URI-list subscriptions | Yes | Depends on imported profiles |
| Native Xray JSON, including grouped configurations | Yes | Full VPN when safe runtime composition validates; otherwise explicit Proxy-only may be available |
| Clash/Mihomo YAML `proxies` | Supported subset | Depends on imported profiles |
| Mihomo Hysteria2 proxy definitions | Strict subset | Supported configurations only |

Import is not a guarantee of connectivity. Unsupported transports, security options and provider-specific fields can be rejected. The YAML importer reads supported proxy definitions, not complete Clash/Mihomo client configurations. Hysteria2 share URIs are not supported. Podlaz does not automatically fall back from full VPN to Proxy-only. See [import and connection details](docs/cli.md#import).

### Remnawave subscriptions

Import a Remnawave subscription URL with `podlaz import` and refresh it with `podlaz subscription update <subscription-id>`. Podlaz sends a locally generated, persistent `x-hwid` identifier; it is not derived from physical hardware. Device limits remain under the provider's control. Subscription-update failures retain the last committed profile state.

Automated compatibility checks cover Remnawave Panel 3.4.5 and Node 3.4.2, not every provider version or transport combination.

## Security and privacy

- Full VPN is the default connection mode. Privileged TUN, route, DNS and firewall changes are handled by `podlazd`; failure or uncertain recovery state does not authorize removal of unrelated network resources.
- `podlaz debug proxy <profile>` is an explicit reduced-protection mode. It does not route system traffic through a TUN interface.
- Podlaz has no application analytics or telemetry. Network access is used for requested subscription operations, VPN traffic and diagnostics.
- Profiles, subscriptions and client identity are stored in user-owned XDG directories. Daemon state is stored separately under `/run/podlaz` and `/var/lib/podlaz`.

Do not post subscription links, credentials, endpoint details, raw configurations or `x-hwid` values in public reports. Use the [private security reporting policy](.github/SECURITY.md) for vulnerabilities.

## Recovery, upgrade and removal

If a connection fails:

```bash
podlaz status
podlaz debug doctor --tun
podlaz debug logs --daemon --since 15m
podlaz debug recover
```

`debug recover` displays a recovery plan. To apply only the changes Podlaz can prove it owns, use `podlaz debug recover --execute`. Do not manually remove unfamiliar routes, DNS or firewall rules. If Polkit authorization fails, check your desktop or TTY Polkit agent rather than running the CLI as root.

For APT installations, use `sudo apt update && sudo apt upgrade`. For direct `.deb` installations, rerun `install-deb.sh` or install a verified newer package. After updating, check `podlaz version` and `podlaz status`. To roll back, install a separately verified earlier release and inspect status again.

To uninstall:

```bash
podlaz disconnect
sudo apt purge podlaz
```

Purging the package does not delete user-owned profiles, subscriptions or client identity data.

## Documentation

- [CLI reference](docs/cli.md) — commands, options and status behavior.
- [Architecture](ARCHITECTURE.md) — network ownership, fail-closed recovery, service and release guarantees.
- [Contributing and engineering workflow](AGENTS.md).
- [Releases](https://github.com/AidarKhusainov/podlaz/releases) · [Issues](https://github.com/AidarKhusainov/podlaz/issues) · [Security](.github/SECURITY.md).

## License

[MIT](LICENSE)
