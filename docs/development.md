# Development guide

## 1. Requirements

- Go 1.26 or newer.
- Linux for networking implementation work.
- Ubuntu LTS or Debian stable for Tier 1 manual testing.
- `iproute2`, `nftables`, `systemd`, `systemd-resolved`, and NetworkManager for full networking work.

## 2. Local checks

Run before opening a pull request:

```bash
gofmt -w .
go test ./...
go run ./cmd/tunwarden version
go run ./cmd/tunwarden doctor
go run ./cmd/tunwarden recover
```

CI currently checks:

```bash
test -z "$(gofmt -l .)"
go test ./...
```

CI uses the Go version declared in `go.mod`.

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

## 8. Current implementation limitation

The current foundation build is intentionally safe and mostly declarative. Commands print contracts, diagnostic summaries, and recovery plans. They do not yet change host networking state.
