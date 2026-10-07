# Podlaz

Podlaz is a command-line VPN client for Linux.

[![CI](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml/badge.svg)](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AidarKhusainov/podlaz)](https://github.com/AidarKhusainov/podlaz/releases/latest)

## Features

- Import VPN profiles and subscriptions through one command.
- Connect the selected profile as a full VPN by default.
- Keep privileged networking in the `podlazd` service, not the CLI.
- Preserve fail-closed protection while Podlaz reconnects or replaces its own session.
- Keep diagnostics and recovery available without making them part of the normal workflow.

## Install

Podlaz is distributed as Debian packages for `amd64` and `arm64` on
systemd-based Linux systems.

Install the latest release with one command:

```bash
curl -fsSL https://raw.githubusercontent.com/AidarKhusainov/podlaz/master/scripts/install.sh | bash
```

The installer detects the Debian architecture, downloads the matching package
and `SHA256SUMS` from the latest GitHub Release, verifies the package checksum,
and elevates privileges only for the package installation.

For manual installation, download a package from
[GitHub Releases](https://github.com/AidarKhusainov/podlaz/releases) and install it:

```bash
sudo apt install ./podlaz_<version>_linux_<arch>.deb
```

Check the installation:

```bash
podlaz version
systemctl is-active podlazd.service
```

Release assets include `SHA256SUMS` and GitHub build provenance attestations.

## Quick start

Import a share URI, subscription URL, or supported local file:

```bash
podlaz import '<uri-or-url-or-file>'
```

If the import produces exactly one profile and no profile is selected, Podlaz
selects it automatically. Otherwise choose one explicitly:

```bash
podlaz profile list
podlaz profile use work
```

Connect, inspect status, and disconnect:

```bash
podlaz connect
podlaz status
podlaz disconnect
```

An explicit profile is a one-shot choice and does not change the saved selection:

```bash
podlaz connect work
```

Canonical `connect` always requests full VPN/TUN protection. It never silently
falls back to Proxy-only operation.

## Profiles and subscriptions

Normal profile selectors accept an exact stable ID or an exact unique
case-insensitive display name. Ordinary completion and list output prefer human
names.

```bash
podlaz profile list
podlaz profile show work
podlaz profile use work
podlaz profile delete work

podlaz subscription list
podlaz subscription show <subscription-id>
podlaz subscription update <subscription-id>
podlaz subscription delete <subscription-id>
```

Profile/subscription deletion is destructive user-data removal and requires an
explicit confirmation. In non-interactive use, pass `--yes`.

## Autostart

Enable the selected full-VPN intent for a future boot:

```bash
podlaz autostart enable
podlaz autostart status
podlaz autostart disable
```

`autostart enable` snapshots the selected profile at that time. A later
`profile use` does not silently rewrite the saved boot policy.

## Advanced diagnostics

The normal workflow does not require manual validation, planning, health checks,
or recovery commands. Advanced support tools are under `debug`:

```bash
podlaz debug doctor
podlaz debug doctor --tun
podlaz debug logs --daemon --since 15m
podlaz debug recover
```

Exact-owned recovery may be executed explicitly:

```bash
podlaz debug recover --execute
```

Proxy-only is an explicit reduced-protection advanced capability:

```bash
podlaz debug proxy work
```

Podlaz does not use Proxy-only as a fallback when a full VPN connection cannot be
established safely.

See [docs/cli.md](docs/cli.md) for the full CLI contract and
[ARCHITECTURE.md](ARCHITECTURE.md) for ownership, recovery, Privacy Envelope,
restart, package-upgrade, and networking invariants.

## Provider compatibility evidence

Permanent hosted qualification pins and exercises Remnawave Panel 3.4.5 with
Remnawave Node 3.4.2. The acceptance path provisions a disposable fixture from
empty runner state and proves subscription import/refresh, stable private
`x-hwid` identity with a one-device policy, proxy traffic, and isolated
full-TUN DNS/TCP/TLS/HTTPS plus privacy/cleanup invariants. No maintainer-owned
Remnawave account, subscription, API credential, or production provider state is
required by these permanent checks.

## License

Podlaz is licensed under the [MIT License](LICENSE).
