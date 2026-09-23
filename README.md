# podlaz — Linux VPN client

A quiet way through.

Podlaz is a CLI-first Linux VPN client for Xray-compatible profiles and
subscriptions. The unprivileged `podlaz` CLI owns user intent and user-scoped
state; the local `podlazd` service owns privileged VPN/network mutations. It
supports local proxy operation and a transaction-backed full-TUN mode with
diagnostics and recovery.

## Install from a GitHub Release

The release pipeline publishes Debian packages for `amd64` and `arm64`, plus
matching tarballs and `SHA256SUMS`. The packaged path is the supported
end-user installation path; building from source is not required.

The package requires a systemd-based Linux userspace. Its declared runtime
dependencies include `libc6 >= 2.34`, `systemd`, `ca-certificates`, `iproute2`,
`nftables`, `systemd-resolved | systemd`, and Polkit (`polkitd` or
`policykit-1`). `apt` resolves those dependencies with the package. TUN mode
also relies on the packaged systemd/resolver integration. An interactive
desktop or TTY Polkit agent is needed when an ordinary user authorizes
privileged daemon actions such as a TUN connect.

Download the current release package for the host architecture and verify it
before installing:

```bash
set -euo pipefail

REPO=AidarKhusainov/podlaz
ARCH="$(dpkg --print-architecture)"
case "$ARCH" in
  amd64|arm64) ;;
  *) echo "unsupported release architecture: $ARCH" >&2; exit 1 ;;
esac

TAG="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")"
TAG="${TAG##*/}"
VERSION="${TAG#v}"

curl -fLO "https://github.com/${REPO}/releases/download/${TAG}/podlaz_${VERSION}_linux_${ARCH}.deb"
curl -fLO "https://github.com/${REPO}/releases/download/${TAG}/SHA256SUMS"
grep -F "podlaz_${VERSION}_linux_${ARCH}.deb" SHA256SUMS | sha256sum -c -

sudo apt install "./podlaz_${VERSION}_linux_${ARCH}.deb"
podlaz version
systemctl is-active podlazd.service
```

`podlaz version` prints the packaged version, source commit, and build date.
Release artifacts also receive GitHub build-provenance attestations. If the
GitHub CLI is installed, the downloaded package can additionally be checked
against the repository attestation:

```bash
gh attestation verify "podlaz_${VERSION}_linux_${ARCH}.deb" -R AidarKhusainov/podlaz
```

Published assets and checksums are available on
[GitHub Releases](https://github.com/AidarKhusainov/podlaz/releases).

### Supported package boundary

| Surface | Current support |
| --- | --- |
| OS/runtime | Linux with the package dependencies above; systemd service integration is part of the package contract. |
| `amd64` | Release package and tarball; deepest hosted installed-package, isolated-guest, VM, recovery, and synthetic full-TUN qualification. |
| `arm64` | Native release package and tarball; build/package validation is automated. It does not currently have the same hosted virtualization depth as `amd64`. |
| Other architectures | No release package is published. |
| Signed APT repository | Not published yet. Until [#73](https://github.com/AidarKhusainov/podlaz/issues/73) is completed, install/upgrade from verified GitHub Release packages. |

This boundary is intentionally narrower than “all Linux distributions”.

## Five-minute start

Import either a subscription URL or a supported share URI:

```bash
podlaz import '<subscription-url>'
# or:
podlaz profile import '<share-uri>'

podlaz profile list
```

Use the profile ID shown by `profile list`. For the least invasive first
connection, start in proxy-only mode:

```bash
podlaz profile validate '<profile-id>' --mode proxy-only
podlaz connect --mode proxy-only '<profile-id>'
podlaz status
podlaz check '<profile-id>' --target cloudflare
podlaz disconnect
```

`check` is a bounded proxy-path diagnostic. It does not create a TUN device or
change host routes, DNS, or nftables state.

For a profile that validates for full tunnel mode:

```bash
podlaz profile validate '<profile-id>' --mode tun
podlaz connect --mode tun '<profile-id>'
podlaz status
podlaz doctor --tun
podlaz disconnect
```

TUN operations go through `podlazd` and the packaged Polkit policy; the CLI
itself does not need to run as root.

See [docs/cli.md](docs/cli.md) for exact command syntax, modes, output semantics,
exit codes, and diagnostic behavior.

## Compatibility

Podlaz accepts supported share URIs and subscription/profile data and renders
them through Xray. Support is format- and mode-specific; importing data does not
imply that every profile is renderable in every mode.

| Input / capability | Import/update | Proxy-only | TUN |
| --- | --- | --- | --- |
| VLESS share URI | Yes | Supported when validation succeeds | Supported when validation succeeds |
| VMess share URI | Yes | Not currently renderable by the generated runtime | Not currently renderable by the generated runtime |
| Trojan share URI | Yes | Not currently renderable by the generated runtime | Not currently renderable by the generated runtime |
| Shadowsocks (`ss://`) share URI | Yes | Not currently renderable by the generated runtime | Not currently renderable by the generated runtime |
| Base64 URI-list subscription | Yes, `file/http/https` | VLESS entries are runtime candidates; other imported protocols remain stored only | VLESS entries are runtime candidates; other imported protocols remain stored only |
| Single-location VLESS Xray JSON subscription profile | Yes | Supported when validation succeeds | Supported when validation succeeds, except mode-specific transports below |
| VLESS Xray JSON with `xhttp` | Yes | Supported | Not supported; validation/planning fails before host-network mutation |
| Grouped/provider-owned Xray JSON | Yes, kept as one grouped profile | Supported | Not supported; validation/planning/connect fails before host-network mutation |
| Stable `x-hwid` subscription identity | Yes for HTTP(S) subscription fetches | Not mode-specific | Not mode-specific |

### Remnawave subscriptions

HTTP(S) subscription fetches send `User-Agent: podlaz` and `x-hwid`.
The `x-hwid` value is a randomly generated stable local UUID-shaped client
identity stored in the invoking user's state directory. It is not derived from
hardware and Podlaz does not read a hardware identifier to create it.

Typical Remnawave-compatible flow:

```bash
podlaz import '<remnawave-subscription-url>'
podlaz subscription list
podlaz profile list
podlaz connect --mode proxy-only '<profile-id>'
podlaz status
podlaz check '<profile-id>'
podlaz disconnect
```

To refresh an already imported subscription:

```bash
podlaz subscription update '<subscription-id>'
```

Base64 subscriptions, ordinary supported Xray JSON entries, and grouped
provider-owned Xray JSON are handled by the same user-state subscription
workflow. Grouped Xray JSON keeps provider routing/outbound selection semantics
in proxy-only mode; it is deliberately rejected for TUN because Podlaz cannot
safely infer a single VPN-server bypass from provider-owned routing.

A provider-side device limit or rejected subscription is not bypassed by
rotating identity or weakening validation. Keep the existing client identity,
check the provider account/device state, and retry the update after resolving
the provider-side condition. Failed fetch/parse/update must not replace the
last known good imported profile set.

Podlaz does not claim compatibility with a particular Remnawave server release
unless that exact environment has been separately qualified. The repository's
deterministic tests prove the HTTP header, parsing, atomic update, grouped-profile,
and redaction contracts without publishing provider credentials.

### Remnawave qualification status

Public claims distinguish deterministic client-contract evidence from a live
provider qualification. The currently audited published Podlaz release is
`v0.2.42`; no specific Remnawave server release is claimed as live-qualified
until a disposable environment is exercised end to end.

| Podlaz release | Remnawave environment | Base64 | Xray JSON | `x-hwid` | Grouped Xray JSON | Evidence |
| --- | --- | --- | --- | --- | --- | --- |
| `v0.2.42` | No disposable server version available during the public-readiness audit | Deterministic import/update contract verified | Deterministic import/update contract verified | Stable random local identity + request-header contract verified | Proxy-only preservation and pre-mutation TUN rejection verified | Repository tests/CI; live provider acceptance intentionally skipped |

When a disposable Remnawave environment is available, use only disposable
credentials and record a sanitized result with this checklist:

1. Record `podlaz version` and the exact disposable Remnawave server version.
2. Import a disposable HTTP(S) subscription URL; confirm subscription/profile
   creation without publishing the URL, token, profile UUIDs, endpoint data, or
   `x-hwid`.
3. Refresh the same subscription and confirm the provider observes the same
   client identity when HWID/device tracking is enabled.
4. Validate and connect one supported VLESS profile in `proxy-only`; run
   `status` and `check`, then disconnect.
5. If the disposable subscription exposes grouped provider-owned Xray JSON,
   verify proxy-only operation and verify TUN is rejected before host-network
   mutation.
6. Exercise one provider-side rejection/device-limit case when the disposable
   environment supports it; confirm Podlaz preserves the last known good state
   and does not rotate identity to bypass the provider policy.
7. Delete/expire the disposable provider credentials and review any captured
   evidence for secrets before publication.

### Public visual evidence

The secret-free terminal preview prepared for the Remnawave Awesome submission
is derived from real GREEN CLI-contract output using repository example/fixture
data only:

![Podlaz CI-derived terminal preview](https://raw.githubusercontent.com/AidarKhusainov/panel/bad5425455bcf694a118065aa42618943907fc56/static/awesome/podlaz.webp)

This image is not presented as a live Remnawave acceptance run. A live terminal
demo remains intentionally unclaimed until the disposable checklist above can be
run against an identified Remnawave server version.

## Security and privacy expectations

Podlaz has no product telemetry or analytics subsystem. It does make network
requests that are intrinsic to explicit product operations: fetching/updating a
remote subscription, running a requested connectivity diagnostic, and operating
the selected proxy/VPN connection.

User-owned profile, subscription, and client-identity state stays under the
invoking user's XDG state/config locations. Privileged runtime/network state is
owned by `podlazd` under the packaged systemd service boundary. Generated Xray
runtime configuration is runtime output, not persistent source of truth.

The networking lifecycle is ownership-driven and fail-closed. Observed host
state alone is never cleanup authority. When Podlaz cannot prove ownership or a
safe transition, it prefers an actionable failure/recovery state over deleting
ambiguous host resources.

Human/JSON command output and maintained public test artifacts have redaction
contracts for credentials and provider data. This is not a promise that an
arbitrary external command, shell history, screen recording, or user-created
archive is safe to publish.

For a non-sensitive defect, use [GitHub Issues](https://github.com/AidarKhusainov/podlaz/issues).
For a suspected vulnerability, do **not** publish exploit details or secrets in
a public issue. First use the repository's
[Security](https://github.com/AidarKhusainov/podlaz/security) surface and its
private **Report a vulnerability** flow when GitHub exposes that control. If
private vulnerability reporting is unavailable, open only a minimal
non-sensitive issue asking the maintainer for a private reporting channel before
sharing technical details.

When reporting a bug, include the output of `podlaz version`, the command that
failed, the redacted error, and relevant `status`, `doctor`, or `recover`
output. Do **not** attach a real subscription URL/token, share URI, UUID,
password/private key, endpoint IP/domain, raw provider response, raw stored
profile/subscription file, generated runtime config, or the `x-hwid` value.

The durable ownership, privilege, recovery, storage, and release guarantees are
specified in [ARCHITECTURE.md](ARCHITECTURE.md).

## Troubleshooting and recovery

Start with read-only inspection:

```bash
podlaz status
podlaz profile validate '<profile-id>' --mode proxy-only
podlaz check '<profile-id>'
podlaz doctor
```

For an active TUN session:

```bash
podlaz doctor --tun
podlaz doctor --tun --verbose
```

Inspect recent service events without dumping generated runtime config:

```bash
podlaz logs --daemon --since 15m
```

Recovery is inspect-first:

```bash
podlaz recover
```

Only after reviewing the projected owned cleanup:

```bash
podlaz recover --execute --yes
```

Recovery skips ambiguous or unowned resources rather than treating their
presence as permission to remove them.

## Upgrade, rollback, and uninstall

Upgrade by downloading and verifying the newer release package exactly as in
the installation section, then install it in place:

```bash
sudo apt install "./podlaz_${VERSION}_linux_${ARCH}.deb"
podlaz version
podlaz status
```

Package replacement, daemon restart, continuation, cleanup, and historical
upgrade behavior are exercised by automated package/runtime qualification. A
failed or interrupted lifecycle is not converted into an unverified active
connection; use `podlaz status` and `podlaz recover` when the result requires
operator attention.

There is no automatic package downgrade command. To roll back the package,
download a previously published release asset, verify its checksum/provenance,
and install that exact `.deb`; then inspect `status`/recovery before
continuing. Do not replace release assets in place.

To remove the packaged service and binaries:

```bash
podlaz disconnect || true
sudo apt purge podlaz
```

User-owned XDG profile/subscription state is not package-owned home-directory
content and is not a substitute target for package purge. Remove user state
separately only when you intentionally want to discard profiles, subscriptions,
and the stable subscription client identity.

## Build and verify from source

Development builds remain available after the release installation path:

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

Repository-wide checks used before merging also include formatting,
vulnerability scanning, shell/workflow/package checks, and the relevant
race/E2E suites. Executable CI and `scripts/**` are the canonical source for
exact automation commands.

## Documentation and project links

The repository intentionally keeps four permanent prose surfaces:

- [README.md](README.md) — installation, quick start, compatibility/trust summary, and documentation routing.
- [docs/cli.md](docs/cli.md) — public CLI commands, flags, outputs, lifecycle/status semantics, and user-facing behavior.
- [ARCHITECTURE.md](ARCHITECTURE.md) — component boundaries, state ownership, security/network invariants, recovery, packaging/runtime, and E2E architecture.
- [AGENTS.md](AGENTS.md) — contributor/agent workflow and minimum-context routing rules.

Implementation details are documented by code and executable tests. Historical
issue/spec/plan prose is intentionally not a permanent knowledge source.

- [Releases](https://github.com/AidarKhusainov/podlaz/releases)
- [Issues](https://github.com/AidarKhusainov/podlaz/issues)
- [Signed APT repository workstream](https://github.com/AidarKhusainov/podlaz/issues/73)

## License

Podlaz is licensed under the MIT License. See [LICENSE](LICENSE).
