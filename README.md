# Podlaz

Podlaz is an open-source command-line VPN client for Linux written in Go.\nIt provides full-system TUN routing with fail-closed protection, DNS recovery,\nautomatic reconnection, exact-owned cleanup, and panic rollback.

[![CI](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml/badge.svg)](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AidarKhusainov/podlaz)](https://github.com/AidarKhusainov/podlaz/releases/latest)

## Features

- Import VPN profiles and subscriptions through one command.
- Connect the selected profile as a full VPN by default.
- Keep privileged networking in the `podlazd` service, not the CLI.
- Preserve fail-closed protection while Podlaz reconnects or replaces its own session.
- Keep diagnostics and recovery available without making them part of the normal workflow.

## Install

Podlaz targets systemd-based Debian/Ubuntu Linux on `amd64` and `arm64`.
Packages require `libc6 >= 2.34`, `iproute2`, `nftables`, systemd, and Polkit;
`apt` resolves packaged dependencies. Full-TUN authorization may require an
interactive Polkit agent. The deepest hosted runtime qualification covers
Ubuntu 24.04 `amd64`; `arm64` packages are built and package-validated.

Install the latest release with one command:

```bash
curl -fsSL https://raw.githubusercontent.com/AidarKhusainov/podlaz/master/scripts/install.sh | bash
```

The installer detects the Debian architecture, downloads the matching package
and `SHA256SUMS` from the latest GitHub Release, verifies the package checksum,
and elevates privileges only for the package installation. **Before running it,
check that the latest release contains the matching `.deb` and `SHA256SUMS`.**
If either asset is missing, that release is incomplete: do not bypass
verification or try to build from source as an installation workaround. Use a
complete, verified earlier release until publication is repaired.

For a downloaded package, compare its SHA256 with the matching entry in
`SHA256SUMS`. Where GitHub build provenance is available, verify it with
`gh attestation verify <package-file>.deb -R AidarKhusainov/podlaz`.

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

`plz` is a short alias for `podlaz`:

```bash
plz status
```

Release assets include `SHA256SUMS` and GitHub build provenance attestations.

### Signed APT repository (pending production publication)

The signed APT publication path is implemented but is **not yet a supported
public install method**. Issue [#423](https://github.com/AidarKhusainov/podlaz/issues/423)
remains open until the production signing key and GitHub Pages endpoint are
configured, the repository is actually deployed, and the public URL and exact
key fingerprint below can be replaced with real values. Until then, keep using
the verified GitHub Release installation above.

The APT channel serves the existing systemd-based Debian/Ubuntu package boundary
for `amd64` and `arm64`. Its deepest automated install/upgrade runtime
qualification is Ubuntu 24.04 `amd64`; the `arm64` index is generated from
the exact release-qualified `arm64` package and checksum-validated without
pretending that GitHub-hosted runners provide native `arm64` runtime coverage.

Once #423 is complete, setup uses a deb822 source with a repository-scoped key;
it does not use `apt-key`. The production URL and fingerprint must be copied
from this README after publication, not guessed:

```bash
APT_BASE_URL='<published APT base URL>'
PUBLISHED_FINGERPRINT='<published uppercase signing-key fingerprint>'

sudo apt update
sudo apt install -y gnupg

key_tmp="$(mktemp)"
trap 'rm -f "$key_tmp"' EXIT
curl -fsSL "$APT_BASE_URL/podlaz-archive-keyring.gpg" -o "$key_tmp"

actual_fingerprint="$(
  gpg --batch --show-keys --with-colons "$key_tmp" 2>/dev/null |
    awk -F: '$1 == "fpr" { print toupper($10); exit }'
)"
test "$actual_fingerprint" = "$PUBLISHED_FINGERPRINT"

sudo install -d -m 0755 /etc/apt/keyrings
sudo install -m 0644 "$key_tmp" /etc/apt/keyrings/podlaz-archive-keyring.gpg

sudo tee /etc/apt/sources.list.d/podlaz.sources >/dev/null <<EOF
Types: deb
URIs: $APT_BASE_URL
Suites: stable
Components: main
Architectures: amd64 arm64
Signed-By: /etc/apt/keyrings/podlaz-archive-keyring.gpg
EOF

sudo apt update
sudo apt install podlaz
podlaz version
systemctl is-active podlazd.service
```

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

Connect, inspect status, run a read-only connection diagnostic, and disconnect:

```bash
podlaz connect
podlaz status
podlaz debug doctor --tun
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
established safely. Proxy-only does not redirect the host's system traffic.

See [docs/cli.md](docs/cli.md) for the full CLI contract and
[ARCHITECTURE.md](ARCHITECTURE.md) for ownership, recovery, Privacy Envelope,
restart, package-upgrade, and networking invariants.

## Supported formats and limitations

| Input | Import | Full-TUN | Explicit Proxy-only |
| --- | --- | --- | --- |
| VLESS share URI | Yes | Supported subset | Supported subset |
| VMess share URI (alterId 0) | Yes | Supported subset | Supported subset |
| Trojan (TLS) share URI | Yes | Supported subset | Supported subset |
| Shadowsocks (supported AEAD) share URI | Yes | Supported subset | Supported subset |
| HTTP(S) Base64 URI-list subscriptions | Yes | Per supported imported profile | Per supported imported profile |
| Native Xray JSON, including grouped/provider-owned configurations | Yes | When safe composed Xray config validates | When supported by bundled Xray |
| Clash/Mihomo YAML `proxies` list | Supported subset | Per imported profile | Per imported profile |
| Mihomo Hysteria2 proxy definition | Strict subset | Supported subset | Supported subset |

Importing a profile is not a guarantee that every transport and security
combination can connect. The YAML importer does not translate full Clash
configuration (rules, proxy groups, DNS, or unrelated protocols). Hysteria2
share URIs and unsupported Hysteria2 options are not claimed. Native grouped
Xray JSON is no longer unconditionally Proxy-only: full-TUN is attempted only
when Podlaz can compose and validate a safe configuration before host-network
mutation. See [the precise import and connection boundaries](docs/cli.md#import).

## Remnawave subscriptions and privacy

Import an authorized Remnawave HTTPS subscription using `podlaz import
'<subscription-url>'`, then use `podlaz subscription list`, `podlaz profile
list`, and `podlaz connect`. Refresh with `podlaz subscription update
<subscription-id>`. HTTP(S) subscription fetches send a locally generated,
stable, private `x-hwid` identity; it is **not** derived from physical hardware.
Do not rotate it to bypass a provider's device limit. On fetch or device-limit
failure, check the provider account and the redacted CLI error; the last
committed subscription state is preserved by the update contract.

Disposable hosted qualification exercises Remnawave Panel **3.4.5**, Node
**3.4.2**, subscription/HWID lifecycle (including a one-device limit), proxy
traffic and isolated full-TUN. This is not a blanket claim for every Remnawave
version or protocol/transport combination.

Podlaz has no application analytics/telemetry subsystem. Expected network
activity includes remote subscription requests, VPN/proxy traffic and explicit
diagnostics. User-owned profile, subscription and client-identity state lives
under the invoking user's XDG directories; privileged runtime and recovery
state belongs to `podlazd`. Do not publish real subscription URLs, share URIs,
credentials, endpoints, `x-hwid`, stored provider JSON or raw runtime configs.
For sensitive reports, follow [private security reporting](.github/SECURITY.md),
not a public issue.

## Recovery, upgrade and removal

If connection fails, inspect the current state and daemon diagnostics:

```bash
podlaz status
podlaz debug doctor
podlaz debug logs --daemon --since 15m
podlaz debug recover
```

Review the recovery plan before explicitly applying exact-owned recovery with
`podlaz debug recover --execute`. Unknown or unowned network state is never
cleanup authority; do not manually remove foreign routing, DNS or firewall
resources. If Polkit authorization is unavailable, use an authorized desktop
or TTY session with a working Polkit agent rather than running the CLI as root.

For an upgrade, install the verified newer `.deb` using `sudo apt install
./<downloaded-package>.deb`, then check `podlaz version` and `podlaz status`.
If an upgrade fails, inspect recovery rather than assuming the VPN is active.
To roll back, install a separately verified earlier release package and inspect
status again. There is no published signed Podlaz APT repository yet (tracked in
[#423](https://github.com/AidarKhusainov/podlaz/issues/423)); use the verified GitHub Release path until that issue is closed.

To uninstall:

```bash
podlaz disconnect
sudo apt purge podlaz
```

Purging the package does not erase user-owned XDG profile, subscription or
client-identity data. Delete those deliberately only if the data is no longer
needed. Read [Security](.github/SECURITY.md), [Releases](https://github.com/AidarKhusainov/podlaz/releases),
and [Issues](https://github.com/AidarKhusainov/podlaz/issues) for trust,
downloads and support.

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
