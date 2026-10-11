# Podlaz

Podlaz is an open-source command-line VPN client for Linux, written in Go.
It routes system traffic through TUN, protects against direct egress during
reconnection, and supports DNS recovery and safe rollback.

[![CI](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml/badge.svg)](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AidarKhusainov/podlaz)](https://github.com/AidarKhusainov/podlaz/releases/latest)

## Features

- Import share links, subscription URLs, and supported configuration files.
- Connect the selected profile as a full-system VPN by default.
- Reconnect or replace a session without an unprotected traffic gap.
- Keep privileged networking in the `podlazd` service, not the CLI.
- Diagnose failures and recover only exactly owned resources.

## Install

Podlaz targets systemd-based Debian/Ubuntu on `amd64` and `arm64`.
Packages require `libc6 >= 2.34`, `iproute2`, `nftables`, systemd, and Polkit;
APT resolves dependencies. Full-TUN authorization may need an interactive Polkit
agent. Runtime qualification is deepest on Ubuntu 24.04 `amd64`; `arm64`
packages are built and package-validated.

**Recommended: signed APT repository.**

```bash
curl -fsSL https://raw.githubusercontent.com/AidarKhusainov/podlaz/master/scripts/install.sh | bash
```

The installer verifies the repository signing key against a pinned fingerprint,
configures a scoped `Signed-By` APT source, and installs Podlaz. Updates use
`sudo apt update && sudo apt upgrade`.

**Alternative: verified GitHub Release package.**

```bash
curl -fsSL https://raw.githubusercontent.com/AidarKhusainov/podlaz/master/scripts/install-deb.sh | bash
```

This checks the matching `.deb` against the release `SHA256SUMS` before
installation. It does not add an APT source or enable automatic updates; rerun
it for a newer release. Missing package or checksum assets cause failure.
You may also download the `.deb` and `SHA256SUMS` from
[GitHub Releases](https://github.com/AidarKhusainov/podlaz/releases),
verify the checksum, and install manually:

```bash
sudo apt install ./podlaz_<version>_linux_<arch>.deb
```

Where GitHub provenance is available, verify it separately with
`gh attestation verify <package-file>.deb -R AidarKhusainov/podlaz`.
Inspect either remote installer before running it.

Verify installation (`plz` is an alias for `podlaz`):

```bash
plz version
systemctl is-active podlazd.service
plz status
```

### Signed APT repository

The `stable` source is hosted at
[aidarkhusainov.github.io/podlaz/apt](https://aidarkhusainov.github.io/podlaz/apt).
Its production signing-key fingerprint is:

```text
7C6F4EF9CDE73501E79CCC63E2F6F280766864C2
```

Verify it against an **independently trusted** maintainer copy; a key served
by the same repository is not an independent trust anchor. For manual setup,
follow the [APT installer's procedure](scripts/install.sh): verify the key,
use a dedicated keyring and deb822 `Signed-By` source, then install through
APT. Never bypass signature errors with `Trusted: yes` or disabled checks.

`apt-cache policy podlaz` must show a `stable/main` repository candidate,
not only an installed version from `/var/lib/dpkg/status`. The published
indexes include `arm64`, but equivalent native runtime qualification is
not claimed.

## Quick start

Import a share URI, subscription URL, or supported local file:

```bash
plz import '<uri-or-url-or-file>'
```

A sole imported profile is selected automatically if no profile is selected.
Otherwise choose one:

```bash
plz profile list
plz profile use work
```

```bash
plz connect
plz status
plz disconnect
```

`plz connect work` is a one-shot choice that does not change the saved
selection. `connect` always requests full VPN/TUN and never silently
falls back to Proxy-only.

## Supported formats and limitations

| Input | Support |
| --- | --- |
| VLESS, VMess (alterId 0), Trojan (TLS), Shadowsocks (supported AEAD) share URIs | Supported subsets in full-TUN and explicit Proxy-only |
| HTTP(S) Base64 URI-list subscriptions | Import; connection per supported profile |
| Native Xray JSON, including grouped configurations | Full-TUN when safe composition validates; explicit Proxy-only when supported |
| Clash/Mihomo YAML `proxies` lists | Supported proxy subset, not a full Clash configuration |
| Mihomo Hysteria2 proxy definitions | Strict subset; no Hysteria2 share URI support |

Import does not guarantee connectivity for every transport/security combination.
Clash rules, groups, DNS, and unrelated protocols are not translated; unsupported
Hysteria2 options are rejected. Native Xray full-TUN composition must validate
**before** host-network mutation. See [precise import boundaries](docs/cli.md#import).

## Profiles and subscriptions

Profile selectors accept stable IDs or unique case-insensitive display names.

```bash
plz profile list
plz profile show work
plz profile use work
plz profile delete work

plz subscription list
plz subscription show <subscription-id>
plz subscription update <subscription-id>
plz subscription delete <subscription-id>
```

Deletion needs confirmation (`--yes` for non-interactive use). Failed
subscription updates preserve the last committed state.

## Autostart

```bash
plz autostart enable
plz autostart status
plz autostart disable
```

Autostart snapshots the selected full-VPN profile for a future boot.
A later `profile use` does not change that policy.

## Advanced diagnostics

Normal connections do not need separate health or recovery commands:

```bash
plz debug doctor --tun
plz debug logs --daemon --since 15m
plz debug recover
```

`plz debug recover` inspects the plan; adding `--execute` applies recovery
only to proven Podlaz-owned resources. It never authorizes changes to unknown
or foreign network state.

Explicit reduced-protection Proxy-only uses `plz debug proxy work`.
It does not route system traffic through the VPN and is never an automatic
fallback from `connect`.

See [docs/cli.md](docs/cli.md) for commands and [ARCHITECTURE.md](ARCHITECTURE.md)
for networking, ownership, privacy, and recovery invariants.

## Remnawave subscriptions and privacy

Import authorized Remnawave HTTPS subscriptions with `plz import
'<subscription-url>'` and refresh with `plz subscription update
<subscription-id>`. HTTP(S) subscription requests include a locally generated,
stable, private `x-hwid` identity, **not** a hardware-derived identifier.
Do not rotate it to bypass device limits; failed updates preserve saved state.

Disposable hosted qualification covers Remnawave Panel **3.4.5** and Node
**3.4.2**, including subscription/HWID, a one-device limit, proxy traffic,
and isolated full-TUN. This is not a guarantee for every provider version or
protocol/transport combination.

Podlaz has no analytics or telemetry subsystem; subscription requests, VPN
traffic, and explicit diagnostics still involve network activity. User profiles,
subscriptions, and identity are stored in user XDG directories; privileged
runtime/recovery state belongs to `podlazd`. Never publish real subscription
URLs, credentials, endpoints, `x-hwid`, provider JSON, or raw runtime configs.
Report sensitive issues [privately](.github/SECURITY.md).

## Recovery, upgrade and removal

On failure, inspect `plz status` and the diagnostics above. Review recovery
before using `--execute`; never manually delete unknown routing, DNS, or
firewall state. For Polkit authorization problems, use an authorized session
with a working agent rather than running the CLI as root.

APT installations use ordinary APT upgrades. Direct Release installations
require rerunning the verified installer or installing a separately verified
newer `.deb`. After upgrade, check `plz version` and `plz status`;
do not assume the VPN is active. To roll back, install a separately verified
earlier release and check status.

```bash
plz disconnect
sudo apt purge podlaz
```

Purging does **not** erase user-owned XDG profile, subscription, or identity
data. Remove those separately only when no longer needed. See
[Releases](https://github.com/AidarKhusainov/podlaz/releases),
[Issues](https://github.com/AidarKhusainov/podlaz/issues), and
[Security](.github/SECURITY.md) for downloads and support.

## License

Podlaz is licensed under the [MIT License](LICENSE).
