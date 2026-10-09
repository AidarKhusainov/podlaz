# Podlaz

Podlaz is an open-source command-line VPN client for Linux written in Go.
It uses xray-core as its only VPN/proxy runtime and provides a normal
full-system VPN flow with fail-closed TUN protection, DNS recovery, automatic
reconnection, exact-owned cleanup, and panic rollback.

[![CI](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml/badge.svg)](https://github.com/AidarKhusainov/podlaz/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AidarKhusainov/podlaz)](https://github.com/AidarKhusainov/podlaz/releases/latest)

## Features

- Import supported share URIs, subscription URLs, local URI lists, Xray JSON,
  and supported Clash/Mihomo YAML subsets through one command.
- Connect the selected profile as a full VPN by default.
- Run VLESS, VMess, Trojan, Shadowsocks, and the documented Hysteria2 subset
  through both full-TUN and explicit Proxy-only operation.
- Keep privileged networking in the `podlazd` service, not the CLI.
- Preserve fail-closed protection while Podlaz reconnects or replaces its own
  session.
- Keep diagnostics and exact-owned recovery available without making them
  prerequisites for a normal connection.

## Install

The supported packaged path is a Debian package on a systemd-based Linux
userspace. Release packaging targets `amd64` and `arm64`.

Install the latest qualified release with one command:

```bash
curl -fsSL https://raw.githubusercontent.com/AidarKhusainov/podlaz/master/scripts/install.sh | bash
```

The installer detects the Debian architecture, downloads the matching `.deb`
and `SHA256SUMS` from the latest GitHub Release, verifies the package checksum,
and elevates privileges only for package installation. It fails instead of
installing when the expected release assets or checksum are unavailable.

A qualified installable release contains the architecture-specific package and
`SHA256SUMS`. Do not treat a GitHub release without those assets as an
installable Podlaz package.

For manual installation, download the matching package and `SHA256SUMS` from
[GitHub Releases](https://github.com/AidarKhusainov/podlaz/releases), verify the
package, then install it:

```bash
grep -F 'podlaz_<version>_linux_<arch>.deb' SHA256SUMS | sha256sum -c -
sudo apt install ./podlaz_<version>_linux_<arch>.deb
```

Check the installation:

```bash
podlaz version
systemctl is-active podlazd.service
```

Packaged installs also provide `plz` as an alias for `podlaz`.

### Supported package boundary

| Surface | Support |
| --- | --- |
| Runtime | Linux/systemd Debian package with `libc6 >= 2.34`, systemd, CA certificates, `iproute2`, `nftables`, `systemd-resolved \| systemd`, and Polkit dependencies declared by the package. |
| `amd64` | Release target with the deepest hosted installed-package, isolated-guest, VM, recovery, and full-TUN qualification. |
| `arm64` | Release target with automated build and package validation; it does not have the same hosted virtualization depth as `amd64`. |
| Other architectures | No release package is published. |
| Signed APT repository | Not published yet; use verified GitHub Release packages. The scoped follow-up is [#423](https://github.com/AidarKhusainov/podlaz/issues/423). |

An interactive desktop or TTY Polkit agent is required when an ordinary user
needs to authorize privileged daemon actions.

## Quick start

Import a subscription, share URI, or supported local file:

```bash
podlaz import '<uri-or-url-or-file>'
```

If import creates exactly one profile and no valid profile is already selected,
Podlaz selects it automatically. Otherwise choose one explicitly:

```bash
podlaz profile list
podlaz profile use '<profile>'
```

Connect through the normal full-system VPN path:

```bash
podlaz connect
```

Inspect the product state and, when needed, the read-only TUN diagnostics:

```bash
podlaz status
podlaz debug doctor --tun
```

Disconnect:

```bash
podlaz disconnect
```

Canonical `podlaz connect` always requests full-TUN protection. It never
silently falls back to Proxy-only operation.

An explicit profile is a one-shot choice and does not change the saved
selection:

```bash
podlaz connect '<profile>'
```

## Compatibility

Import success is not used as evidence of runtime support. The supported
protocol subsets below have executable data-plane coverage for canonical
full-TUN and explicit Proxy-only operation on the current implementation.

| Protocol | Supported imported material | Full-TUN | Proxy-only |
| --- | --- | --- | --- |
| VLESS | Share URI / URI-list subscriptions, supported Clash/Mihomo VLESS transports and security, native Xray JSON | Yes, for validated supported material | Yes |
| VMess | Share URI / URI-list subscriptions with `alterId=0`, supported Clash/Mihomo material, native Xray JSON | Yes | Yes |
| Trojan | Share URI / URI-list subscriptions, supported TLS/transport Clash/Mihomo material, native Xray JSON | Yes | Yes |
| Shadowsocks | `ss://` / URI-list subscriptions, supported AEAD Clash/Mihomo material, native Xray JSON | Yes | Yes |
| Hysteria2 | Strict Clash/Mihomo subset (server, port, password, TLS SNI, ALPN) represented through private Xray-native material; native Xray JSON remains schema-opaque | Yes | Yes |
| Native/grouped Xray JSON | Provider-owned Xray configuration preserved as schema-opaque material | Yes when the composed config validates and can participate safely in TUN planning | Yes |

Podlaz does **not** claim a Hysteria2 share-URI format. Unsupported Hysteria2
bandwidth, obfuscation, port hopping, certificate-verification overrides,
legacy VMess semantics, unsupported transports/security combinations, and
unrecognized Clash/Mihomo options fail explicitly instead of being silently
discarded.

Supported source formats:

| Source | Local import | HTTP(S) subscription/update | Notes |
| --- | --- | --- | --- |
| Direct share URI | Yes | N/A as a standalone source | VLESS, VMess, Trojan, Shadowsocks supported subsets. |
| Plain URI list | Yes | Yes | Supported typed protocols; remote refresh is atomic. |
| Base64 URI-list | Yes | Yes | Supported typed protocols; remote refresh is atomic. |
| Native Xray JSON | Yes | Yes | Schema-opaque; provider-owned fields survive persistence/runtime composition. |
| Clash/Mihomo YAML `proxies` | Yes | Yes | Only documented proxy subsets are translated; full Clash client config is not translated. |

The canonical details, including rejected combinations and output semantics, are
in [docs/cli.md](docs/cli.md).

### Explicit Proxy-only operation

Proxy-only is an advanced reduced-protection capability:

```bash
podlaz debug proxy '<profile>'
```

It does not mutate TUN, routes, DNS, nftables, or firewall state. It is never an
automatic fallback from a failed full-TUN connection.

## Remnawave compatibility

HTTP(S) subscription fetches send `User-Agent: podlaz` and `x-hwid`.
The `x-hwid` value is a randomly generated stable local UUID-shaped client
identity. It is stored in user-owned state, is not derived from hardware, and
Podlaz does not read a hardware identifier to create it.

Import and refresh a Remnawave subscription through the ordinary subscription
workflow:

```bash
podlaz import '<remnawave-subscription-url>'
podlaz subscription list
podlaz subscription update '<subscription-id>'
```

Permanent hosted qualification provisions a disposable Remnawave environment
from empty runner state and pins **Remnawave Panel 3.4.5** with **Remnawave Node
3.4.2**. It proves real HTTPS subscription import/refresh, stable private
`x-hwid` registration under a one-device policy, rejection of a second clean
identity without rotating the original identity, explicit Proxy-only traffic,
isolated full-TUN DNS/TCP/TLS/HTTPS, and cleanup/privacy invariants.

If the provider rejects a refresh because of a device/account policy, Podlaz
does not rotate identity to bypass that policy. Resolve the provider-side
condition and retry the update. A failed fetch, parse, or update preserves the
last committed subscription/profile state.

No provider URL, token, endpoint, UUID, raw provider payload, or `x-hwid`
value is part of public qualification evidence.

## Failed connection and recovery

Start with read-only product and diagnostic state:

```bash
podlaz status
podlaz debug doctor --tun
podlaz debug logs --daemon --since 15m
```

Inspect exact-owned recovery without mutating networking:

```bash
podlaz debug recover
```

If the inspection identifies Podlaz-owned cleanup that should be applied:

```bash
podlaz debug recover --execute
```

Recovery never treats observation or historical resemblance as cleanup
authority. Ambiguous or foreign network state remains untouched.

## Security and privacy

Podlaz has no product analytics or telemetry subsystem. Network activity still
occurs when it is intrinsic to an explicit operation: fetching/updating a remote
subscription, running requested diagnostics, or operating the selected VPN/proxy
connection.

User profile, subscription, selection, and client-identity state is kept under
the invoking user's XDG locations. Privileged runtime/network state is owned by
`podlazd`. Normal command output, maintained diagnostics, and public
qualification artifacts are designed to redact credentials, subscription URLs,
raw provider material, generated runtime configuration, and private ownership
details where they are not needed for the user task.

For a vulnerability, follow the repository
[Security Policy](.github/SECURITY.md) and use GitHub private vulnerability
reporting when available. Do not put credentials, subscription data, private
endpoints, raw runtime configs, or `x-hwid` values in public issues.

The durable privilege, ownership, fail-closed, redaction, recovery, and package
invariants are documented in [ARCHITECTURE.md](ARCHITECTURE.md).

## Release integrity, upgrade, rollback, and uninstall

Qualified release publication builds the `amd64`/`arm64` artifact set once,
creates `SHA256SUMS`, passes the exact same artifacts through package/runtime,
full-TUN, and Remnawave qualification, then publishes and attests those exact
files.

After downloading a package, verify both the checksum and, when GitHub CLI is
available, its build-provenance attestation:

```bash
grep -F 'podlaz_<version>_linux_<arch>.deb' SHA256SUMS | sha256sum -c -
gh attestation verify podlaz_<version>_linux_<arch>.deb -R AidarKhusainov/podlaz
```

To upgrade, run the installer again after a new qualified release is published,
or download/verify/install the newer package manually. Then inspect:

```bash
podlaz version
podlaz status
```

There is no automatic downgrade command. For rollback, download a previously
published package, verify its checksum/provenance, install that exact `.deb`,
then inspect status/recovery before reconnecting.

To remove the packaged service and binaries:

```bash
podlaz disconnect || true
sudo apt purge podlaz
```

Package purge does not silently delete user-owned XDG profile/subscription state
or the stable subscription client identity.

## Public evidence

Current executable evidence includes:

- [#414 closure matrix](https://github.com/AidarKhusainov/podlaz/issues/414)
  for format/protocol/import/update classification.
- [#421 runtime evidence](https://github.com/AidarKhusainov/podlaz/issues/421)
  for real Proxy-only and full-TUN data-plane coverage across the claimed
  protocol families.
- [Remnawave/HWID qualification evidence](https://github.com/AidarKhusainov/podlaz/issues/341#issuecomment-6036205302)
  for the pinned disposable provider environment.

The prepared Remnawave Awesome card uses a synthetic/secret-free terminal
preview. It is presentation evidence, not the CLI specification:

![Podlaz secret-free terminal preview](https://raw.githubusercontent.com/AidarKhusainov/panel/071c063097ffae3b8e95efff25c9c8a5b23b8493/static/awesome/podlaz.webp)

## Documentation and project links

- [CLI reference](docs/cli.md) — commands, outputs, compatibility boundaries,
  status, diagnostics, and recovery.
- [Architecture](ARCHITECTURE.md) — privileged ownership, networking,
  fail-closed recovery, packaging/runtime, and E2E invariants.
- [Security Policy](.github/SECURITY.md) — private security-reporting guidance.
- [Releases](https://github.com/AidarKhusainov/podlaz/releases) — packages,
  checksums, attestations, and release notes.
- [Issues](https://github.com/AidarKhusainov/podlaz/issues) — non-sensitive
  bugs and feature requests.

## Build from source

The packaged release is the end-user path. For development:

```bash
go test ./...
go vet ./...
go run ./cmd/podlaz version
go run ./cmd/podlazd
```

Build a local Debian package:

```bash
bash scripts/build-deb.sh
sudo apt install ./dist/podlaz_0.0.0~dev-1_linux_amd64.deb
```

## License

Podlaz is licensed under the [MIT License](LICENSE).
