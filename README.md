# Podlaz

Podlaz is a command-line VPN client for Linux.

[![CI](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml/badge.svg)](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AidarKhusainov/podlaz)](https://github.com/AidarKhusainov/podlaz/releases/latest)

## Features

- Import VPN profiles and subscriptions.
- Connect through Xray.
- Use proxy-only or full VPN mode.
- Check connection status and run diagnostics.
- Recover from interrupted or failed sessions.
- Run the CLI without root.

## Install

Podlaz is distributed as Debian packages for `amd64` and `arm64`.

Download the latest package from [GitHub Releases](https://github.com/AidarKhusainov/podlaz/releases) and install it:

```bash
sudo apt install ./podlaz_<version>_linux_<arch>.deb
```

Check the installation:

```bash
podlaz version
systemctl is-active podlazd.service
```

Podlaz requires a systemd-based Linux system.

Release assets include `SHA256SUMS` and GitHub build provenance attestations.

<details>
<summary>Verify a downloaded package</summary>

```bash
grep -F 'podlaz_<version>_linux_<arch>.deb' SHA256SUMS | sha256sum -c -
```

With GitHub CLI:

```bash
gh attestation verify podlaz_<version>_linux_<arch>.deb \
  -R AidarKhusainov/podlaz
```

</details>

## Quick start

Import a subscription:

```bash
podlaz import '<subscription-url>'
```

Or import a profile:

```bash
podlaz profile import '<share-uri>'
```

List profiles:

```bash
podlaz profile list
```

Validate and connect:

```bash
podlaz profile validate '<profile-id>' --mode proxy-only
podlaz connect --mode proxy-only '<profile-id>'
```

Check the connection:

```bash
podlaz status
podlaz check '<profile-id>'
```

Disconnect:

```bash
podlaz disconnect
```

See [docs/cli.md](docs/cli.md) for all commands and options.

## Connection modes

### Proxy-only

Runs Xray without changing system routes, DNS, or firewall state.

```bash
podlaz connect --mode proxy-only '<profile-id>'
```

### TUN

Routes system traffic through a TUN interface.

```bash
podlaz profile validate '<profile-id>' --mode tun
podlaz connect --mode tun '<profile-id>'
```

TUN operations are handled by the `podlazd` system service. The CLI itself does not need to run as root.

## Supported profiles

Podlaz can import share links, subscriptions, and supported Xray JSON configurations.

| Format | Import | Proxy-only | TUN |
| --- | --- | --- | --- |
| VLESS | Yes | Yes | Yes |
| VMess | Yes | No | No |
| Trojan | Yes | No | No |
| Shadowsocks | Yes | No | No |
| Base64 URI-list subscription | Yes | VLESS profiles | VLESS profiles |
| Xray JSON | Yes | Supported configurations | Supported configurations |

Some Xray configurations have mode-specific limits.

See [docs/cli.md](docs/cli.md) for exact compatibility rules.

## Diagnostics and recovery

```bash
podlaz status
podlaz doctor
podlaz doctor --tun
podlaz logs --daemon --since 15m
```

Inspect a recovery plan:

```bash
podlaz debug recover
```

Run it after reviewing the proposed cleanup:

```bash
podlaz debug recover --execute --yes
```

Podlaz only removes network state that it can identify as its own.

## Documentation

- [CLI reference](docs/cli.md)
- [Architecture](ARCHITECTURE.md)
- [Releases](https://github.com/AidarKhusainov/podlaz/releases)
- [Issues](https://github.com/AidarKhusainov/podlaz/issues)

## License

Podlaz is licensed under the [MIT License](LICENSE).
