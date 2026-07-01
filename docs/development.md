# Development guide

## Requirements

- Go 1.26.4, as pinned in `go.mod`.
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
go run ./cmd/podlaz doctor
go run ./cmd/podlaz recover
go run ./cmd/podlaz completion bash >/dev/null
go run ./cmd/podlaz completion zsh >/dev/null
go run ./cmd/podlaz completion fish >/dev/null
```

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
| Self-hosted E2E runner behavior | `docs/e2e.md` |
| Local developer workflow | `docs/development.md` |

Everything else belongs in issues, PRs, release notes, or code comments near the implementation.
