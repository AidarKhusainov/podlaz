# Development guide

## Requirements

- Go 1.26.5, as pinned in `go.mod`.
- Linux for networking work.
- Debian/Ubuntu for package checks.
- `iproute2`, `nftables`, `systemd`, `systemd-resolved`, and NetworkManager for full TUN testing.
- `nfpm`, `dpkg-deb`, and optionally `lintian` for package work.

## Before opening a PR

```bash
gofmt -w .
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
go run ./cmd/podlaz version
go run ./cmd/podlaz completion bash >/dev/null
go run ./cmd/podlaz completion zsh >/dev/null
go run ./cmd/podlaz completion fish >/dev/null
```

`podlaz doctor` and `podlaz recover` are still useful local checks when the host has the expected daemon/systemd context. They are not part of the default hosted PR gate because the default gate must stay deterministic and must not depend on privileged host state.

For package changes:

```bash
bash scripts/build-deb.sh
dpkg-deb --info dist/podlaz_0.0.0~dev-1_linux_amd64.deb
dpkg-deb --contents dist/podlaz_0.0.0~dev-1_linux_amd64.deb
file dist/package-root/usr/bin/podlaz dist/package-root/usr/bin/podlazd
ldd dist/package-root/usr/bin/podlaz
ldd dist/package-root/usr/bin/podlazd
lintian --fail-on error dist/podlaz_0.0.0~dev-1_linux_amd64.deb
sudo apt install ./dist/podlaz_0.0.0~dev-1_linux_amd64.deb
podlaz version
plz version
podlaz completion bash >/dev/null
podlaz completion zsh >/dev/null
podlaz completion fish >/dev/null
man -l /usr/share/man/man1/podlaz.1.gz >/dev/null
man -l /usr/share/man/man8/podlazd.8.gz >/dev/null
sudo apt install -y --reinstall ./dist/podlaz_0.0.0~dev-1_linux_amd64.deb
sudo apt purge -y podlaz
```

The CI helper scripts under `scripts/ci/` are the executable source of truth for hosted package validation. Prefer updating those scripts instead of adding large inline shell blocks to workflow YAML.


The resource-soak attribution and contract helpers are deterministic Python/shell
checks and can be exercised without privileged host mutation:

```bash
python3 -m py_compile scripts/e2e/lib/tun_soak_*.py scripts/e2e/tests/test_tun_soak_*.py
python3 -m unittest scripts.e2e.tests.test_tun_soak_metrics
python3 -m unittest scripts.e2e.tests.test_tun_soak_status
python3 -m unittest scripts.e2e.tests.test_tun_soak_health
python3 -m unittest scripts.e2e.tests.test_tun_soak_cleanup
python3 -m unittest scripts.e2e.tests.test_tun_resource_soak_contract
bash -n scripts/e2e/lib/tun_soak_health.sh scripts/e2e/lib/tun_soak_cleanup.sh scripts/e2e/tun-resource-soak.sh
```

The real installed-package run remains a controlled Ubuntu 24.04 host operation:

```bash
PODLAZ_E2E_PROFILE_URI='<private-profile-uri>' \
PODLAZ_E2E_SOAK_DURATION_SECONDS=10800 \
bash scripts/e2e/tun-resource-soak.sh
```

Exact process identities and raw network/health evidence stay in the private E2E
temporary directory. Only sanitized cgroup/procfs counters and the compact report
belong in artifacts. The helper does not enable pprof or add a production debug
listener.


## CI/CD gates

The default PR and `master` push gate is intentionally limited to checks that are deterministic on GitHub-hosted Linux runners:

- workflow and shell lint for workflow/package-maintenance scripts;
- Go formatting, tests, vet, vulnerability scan, and CLI smoke checks;
- Debian package build and static validation for `amd64` and `arm64`;
- local install, same-version reinstall, route-diff check, and purge cleanup for the `amd64` package.

The self-hosted E2E workflow remains manual and optional. Run it, or run the relevant `scripts/e2e/*.sh` checks manually on a controlled Linux host, when a change touches TUN devices, routes, DNS, nftables, firewall behavior, daemon privilege boundaries, systemd service behavior, packaged service lifecycle, provider-backed data-plane behavior, crash/recovery behavior, or fault-injection behavior.

The tagged release workflow reuses the same validation scripts, creates release checksums, uploads workflow artifacts for review, attests the release artifacts, and publishes standalone Linux binary archives, `.deb` files, and `SHA256SUMS` to GitHub Releases.

## Rules

- Work through pull requests.
- Keep PRs small.
- Add tests for behavior changes.
- Update only the canonical doc that owns the changed behavior.
- Do not add new permanent docs for temporary milestones, acceptance evidence, or implementation inventory.
- Keep the CLI unprivileged; privileged networking belongs to the daemon.
- Add rollback before adding route, DNS, nftables, TUN, or process-state mutation.
- Keep cleanup idempotent and limited to podlaz-owned state.
- Do not print secrets in output, JSON, logs, diagnostics, or artifacts.

## Documentation ownership

| Change | Update |
| --- | --- |
| CLI command, flag, mode, exit code, JSON behavior | `docs/cli.md` |
| State, redaction, daemon boundary, networking safety | `docs/state-and-security.md` |
| Debian package layout or service install behavior | `docs/debian-package.md` |
| Release workflow or artifact naming | `docs/release.md` |
| Self-hosted/manual privileged E2E behavior | `docs/e2e.md` |
| Local developer workflow | `docs/development.md` |

Everything else belongs in issues, PRs, release notes, or code comments near the implementation.
