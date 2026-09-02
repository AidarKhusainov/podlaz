# podlaz — Linux VPN client

A quiet way through.

Podlaz is a Linux VPN client with Xray-compatible profile management, a CLI, and a privileged local daemon. The CLI owns user intent and user-scoped profile state; `podlazd` owns privileged runtime/network mutations.

## Quick start

Import a profile, connect it in TUN mode, inspect status, then disconnect:

```bash
podlaz profile import '<share-uri>'
podlaz connect --mode tun '<profile-id>'
podlaz status
podlaz disconnect
```

See [docs/cli.md](docs/cli.md) for profile IDs, modes, flags, outputs, and other commands.

## Build and verify

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

Repository-wide checks used before merging also include formatting, vulnerability scanning, shell/workflow/package checks, and the relevant race/E2E suites. Executable CI and `scripts/**` are the canonical source for exact automation commands.

## Documentation

The repository intentionally keeps four permanent prose surfaces:

- [README.md](README.md) — entry point, build/package/release orientation, and documentation routing.
- [docs/cli.md](docs/cli.md) — public CLI commands, flags, outputs, lifecycle/status semantics, and user-facing behavior.
- [ARCHITECTURE.md](ARCHITECTURE.md) — component boundaries, state ownership, security/network invariants, recovery, packaging/runtime, and E2E architecture.
- [AGENTS.md](AGENTS.md) — contributor/agent workflow and the minimum-context routing rules.

Implementation details are documented by the code and executable tests. Historical issue/spec/plan prose is intentionally not a permanent knowledge source.

## Runtime model

`podlaz` is unprivileged. It parses commands, manages user-owned profile/subscription state, and calls the local daemon. `podlazd` is the privileged boundary for TUN, routes, policy rules, DNS, firewall state, recovery, diagnostics, and packaged runtime lifecycle. Host-network mutation is fail-closed and ownership-driven: observed host state is not cleanup authority.

For public command behavior use [docs/cli.md](docs/cli.md). For engineering invariants use [ARCHITECTURE.md](ARCHITECTURE.md).

## Repository

https://github.com/AidarKhusainov/podlaz

## License

Podlaz is licensed under the MIT License. See [LICENSE](LICENSE).
