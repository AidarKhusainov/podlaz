# Development guide

## 1. Requirements

- Go 1.26.4, the current pinned project toolchain.
- Linux for networking implementation work.
- Ubuntu LTS or Debian stable for Tier 1 manual testing.
- `iproute2`, `nftables`, `systemd`, `systemd-resolved`, and NetworkManager for full networking work.
- `nfpm`, `dpkg-deb`, and optionally `lintian` for local Debian package work.

The module language version is declared as `go 1.26` in `go.mod`; the exact stable toolchain is pinned with `toolchain go1.26.4` and mirrored in CI.

## 2. Local checks

Run before opening a pull request:

```bash
gofmt -w .
go test ./...
go run ./cmd/tunwarden version
go run ./cmd/tunwarden doctor
go run ./cmd/tunwarden recover
go run ./cmd/tunwarden completion bash >/dev/null
go run ./cmd/tunwarden completion zsh >/dev/null
go run ./cmd/tunwarden completion fish >/dev/null
```

For packaging changes, also run where the required tools are available:

```bash
bash scripts/build-deb.sh
dpkg-deb --info dist/tunwarden_0.0.0~dev-1_amd64.deb
dpkg-deb --contents dist/tunwarden_0.0.0~dev-1_amd64.deb
file dist/package-root/usr/bin/tunwarden dist/package-root/usr/bin/tunwardend
ldd dist/package-root/usr/bin/tunwarden
ldd dist/package-root/usr/bin/tunwardend
test -f dist/package-root/usr/share/bash-completion/completions/tunwarden
test -f dist/package-root/usr/share/zsh/vendor-completions/_tunwarden
test -f dist/package-root/usr/share/fish/vendor_completions.d/tunwarden.fish
lintian --fail-on error dist/tunwarden_0.0.0~dev-1_amd64.deb
sudo apt install ./dist/tunwarden_0.0.0~dev-1_amd64.deb
tunwarden version
tunwarden completion bash >/dev/null
tunwarden completion zsh >/dev/null
tunwarden completion fish >/dev/null
man -l /usr/share/man/man1/tunwarden.1.gz >/dev/null
man -l /usr/share/man/man8/tunwardend.8.gz >/dev/null
sudo apt install -y --reinstall ./dist/tunwarden_0.0.0~dev-1_amd64.deb
sudo apt remove -y tunwarden
```

CI currently checks:

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
bash scripts/build-deb.sh
dpkg-deb --info dist/tunwarden_0.0.0~dev-1_amd64.deb
dpkg-deb --contents dist/tunwarden_0.0.0~dev-1_amd64.deb
file dist/package-root/usr/bin/tunwarden dist/package-root/usr/bin/tunwardend
ldd dist/package-root/usr/bin/tunwarden
ldd dist/package-root/usr/bin/tunwardend
lintian --fail-on error dist/tunwarden_0.0.0~dev-1_amd64.deb
sudo apt install -y ./dist/tunwarden_0.0.0~dev-1_amd64.deb
tunwarden version
tunwarden completion bash >/dev/null
tunwarden completion zsh >/dev/null
tunwarden completion fish >/dev/null
man -l /usr/share/man/man1/tunwarden.1.gz >/dev/null
man -l /usr/share/man/man8/tunwardend.8.gz >/dev/null
sudo apt install -y --reinstall ./dist/tunwarden_0.0.0~dev-1_amd64.deb
sudo apt remove -y tunwarden
```

Release workflow checks are defined in [Release workflow](./release.md). The release workflow adds tagged artifact validation, checksum generation, and GitHub Release publication on top of the regular CI and package gates.

CI uses Go 1.26.4. The package job intentionally runs on Ubuntu 22.04 to keep the dynamically linked package binary baseline aligned with the declared `libc6 (>= 2.34)` dependency. Local development should use the same Go toolchain unless a PR explicitly updates `go.mod`, CI, and this guide together.

## 3. Safety rules for contributors

These rules are mandatory for any code touching privileged Linux networking:

1. Add a rollback path before adding route, rule, DNS, nftables, TUN, or process-state changes.
2. Do not add SUID binaries.
3. Do not write directly to `/etc/resolv.conf` in normal operation.
4. Keep route, DNS, and firewall behavior explicit and reviewable.
5. Prefer dry-run output before execution.
6. Keep cleanup idempotent.
7. Keep daemon-owned resources identifiable by name, marker, table ID, or state file.
8. Treat `recover` as a product feature, not a debug script.
9. Do not print secrets in human output, JSON output, or logs.
10. Do not broaden daemon privileges without documenting the reason.

## 4. Documentation update rules

When changing behavior, update documentation in the same pull request.

Required mapping:

| Change type | Documentation to update |
| --- | --- |
| User-visible command | `docs/cli.md`, `README.md`, `docs/README.md` |
| CLI exit code or JSON output | `docs/cli.md`, `docs/state-and-security.md` |
| CLI/daemon boundary | `docs/architecture.md`, `docs/package-boundaries.md` |
| State path, runtime file, or ownership model | `docs/state-and-security.md`, `docs/architecture.md` |
| Output redaction or secret handling | `docs/state-and-security.md` |
| systemd unit or daemon privilege behavior | `docs/state-and-security.md`, `docs/architecture.md` |
| Debian package layout or lifecycle | `docs/debian-package.md`, `README.md`, `docs/README.md` |
| Release workflow, release artifact naming, or release validation | `docs/release.md`, `docs/README.md`, `docs/development.md` |
| TUN, route, DNS, firewall, NetworkManager, suspend/resume behavior | `docs/networking-reliability.md` |
| Profile, subscription, parser, validation behavior | `docs/subscriptions-and-profiles.md` |
| Development phase or milestone change | `docs/roadmap.md` |
| External assumption or design reference | `docs/references.md` |

## 5. Branching and pull requests

Work should go through pull requests.

Pull requests should be small enough that networking behavior can be reviewed precisely.

Recommended PR checklist:

- [ ] Code is formatted with `gofmt`.
- [ ] Tests pass with `go test ./...`.
- [ ] New privileged behavior has explicit rollback or a documented reason why it is read-only.
- [ ] `doctor`/diagnostics behavior is updated when system behavior changes.
- [ ] JSON output and exit codes follow `docs/cli.md`.
- [ ] Output follows the redaction policy.
- [ ] Documentation is updated with code changes.
- [ ] Failure modes are described in the PR body.

Packaging PR checklist:

- [ ] Local `.deb` artifact builds for `amd64`.
- [ ] `dpkg-deb --info` and `dpkg-deb --contents` show expected metadata and file layout.
- [ ] Packaged binaries report the package version through `tunwarden version`.
- [ ] Packaged shell completion files exist for bash, zsh, and fish.
- [ ] Packaged binaries have the expected dynamic linkage baseline for the declared package dependencies.
- [ ] `lintian` is clean of errors, or every relevant warning is documented and justified.
- [ ] The package does not ship `/usr/local`, `/run`, `/var/run`, user-home, or generated runtime config paths.
- [ ] Install, same-version reinstall, man page, shell completion, and remove behavior are validated in a container or VM.
- [ ] Full systemd behavior is validated in a VM or systemd-capable host when the PR claims service lifecycle acceptance.

Release PR checklist:

- [ ] Version tag mapping is documented.
- [ ] Release artifacts include binary, Debian package, and checksums.
- [ ] Release notes include tag and commit SHA.
- [ ] Build/test jobs use read-only permissions.
- [ ] Release publication grants only the write permission needed for GitHub Release assets.
- [ ] Third-party Actions are pinned to a full-length commit SHA, or tag pinning is explicitly justified in `docs/release.md`.

## 6. Testing strategy

### Unit tests

Use unit tests for:

- profile parsing,
- subscription parsing,
- profile normalization,
- route planning,
- DNS planning,
- firewall planning,
- transaction state transitions,
- idempotent cleanup planning,
- redaction helpers,
- exit code mapping,
- JSON output shape.

### Integration tests

Use Linux network namespaces where possible for:

- route/rule planner execution,
- nftables behavior,
- TUN lifecycle,
- rollback behavior,
- stale state detection.

### Package tests

Use package inspection and local install/remove validation for:

- Debian metadata,
- installed file layout,
- package lifecycle behavior,
- absence of generated runtime files in package contents,
- binary, shell completion, and man page availability after install,
- version consistency between package metadata and `tunwarden version`,
- same-version reinstall behavior,
- dynamic linkage compatibility with the declared package dependency baseline.

Use a VM or systemd-capable host for service lifecycle assertions such as `systemctl status tunwardend`, runtime directory creation, journald behavior, and daemon startup under the packaged unit.

### Manual tests

Before declaring TUN mode stable, run manual tests on Ubuntu LTS at minimum:

- connect/disconnect loop,
- failed connection rollback,
- core crash during active connection,
- daemon crash during apply,
- suspend/resume,
- Wi-Fi reconnect,
- DHCP renewal,
- DNS change,
- `recover --execute --yes` after simulated failure.

## 7. Implementation preferences

- Keep planners mostly pure and testable without root.
- Keep executors narrow, explicit, and auditable.
- Follow the state ownership model in `docs/state-and-security.md`.
- Store daemon runtime state under `/run/tunwarden/`.
- Store daemon persistent state under `/var/lib/tunwarden/`.
- Store user intent/state through the documented XDG layout.
- Use journald as the primary log destination for the daemon.
- Generate core configs under `/run/tunwarden/generated/`; do not treat generated engine config as persistent source of truth.
- Write generated core configs atomically and avoid logging them in full.
- Prefer nftables over iptables for initial firewall work.
- Prefer systemd-resolved per-link DNS over global resolver mutation.
- Treat NetworkManager connectivity as diagnostic metadata, not the only health source.

## 8. Networking safety boundary

The foundation build is intentionally safe and mostly declarative. Read-only commands print contracts, diagnostic summaries, and recovery plans. Host networking changes require daemon-owned execution paths with explicit planning, verification, and recovery behavior.
