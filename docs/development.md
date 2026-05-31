# Development guide

## 1. Requirements

- Go 1.23 or newer.
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
go run ./cmd/tunwarden panic-reset
```

CI currently checks:

```bash
test -z "$(gofmt -l .)"
go test ./...
```

## 3. Safety rules for contributors

These rules are mandatory for any code touching privileged networking:

1. Do not add route, rule, DNS, nftables, TUN, or process mutations without a rollback path.
2. Do not add SUID binaries.
3. Do not write directly to `/etc/resolv.conf` in normal operation.
4. Do not hide route/DNS/firewall changes behind vague helper functions.
5. Prefer dry-run output before execution.
6. Keep cleanup idempotent.
7. Keep daemon-owned resources identifiable by name, marker, table ID, or state file.
8. Treat panic reset as a product feature, not a debug script.

## 4. Documentation update rules

When changing behavior, update documentation in the same pull request.

Required mapping:

| Change type | Documentation to update |
| --- | --- |
| User-visible command | `README.md`, `docs/README.md`, relevant requirement file |
| CLI/daemon boundary | `docs/architecture.md` |
| State path or runtime file | `docs/architecture.md` |
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
- idempotent cleanup planning.

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
- `panic-reset` after simulated crash.

## 7. Implementation preferences

- Keep planners mostly pure and testable without root.
- Keep executors narrow, explicit, and auditable.
- Store persistent daemon data under `/var/lib/tunwarden/`.
- Store volatile runtime state under `/run/tunwarden/`.
- Use journald as the primary log destination for the daemon.
- Generate core configs under `/run/tunwarden/generated/`; do not treat generated engine config as persistent source of truth.
- Prefer nftables over iptables for initial firewall work.
- Prefer systemd-resolved per-link DNS over global resolver mutation.
- Treat NetworkManager connectivity as diagnostic metadata, not the only health source.

## 8. Current implementation limitation

The current foundation build is intentionally safe and mostly declarative. Commands print contracts, diagnostic summaries, and cleanup plans. They do not yet mutate system networking state.
