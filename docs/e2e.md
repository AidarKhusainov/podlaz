# Self-hosted E2E

Manual host validation for behavior that is not suitable for the default pull-request gate.

## Run

```text
Actions -> E2E -> Run workflow
```

Default job order:

1. CLI contract
2. Package and service
3. Proxy data-plane
4. Maximum server coverage
5. Gated TUN fault-injection coverage

## Runner

Required labels:

```text
self-hosted
linux
x64
vpn-e2e
ubuntu-24.04
```

Required host tools:

- systemd;
- `/dev/net/tun`;
- `iproute2`, `nftables`, `resolvectl`, `journalctl`;
- Go from the workflow setup step.

Additional Debian/Ubuntu or arm64 coverage requires dedicated runners or VMs.

## Configuration

Workflow inputs are owned by `.github/workflows/e2e.yml` and the `scripts/e2e/*.sh` entrypoints.
Do not duplicate the full input inventory here.

TUN fault-injection coverage is opt-in. The workflow job is safe by default and exits without host disruption unless `PODLAZ_E2E_ENABLE_TUN_FAULT_INJECTION=true` is set for the self-hosted runner environment. When enabled, `scripts/e2e/tun-fault-injection.sh` installs a temporary systemd drop-in for `podlazd.service` that enables daemon-owned E2E hooks, runs deterministic DNS apply, route apply, and pre-commit interruption probes, scans its artifacts for configured secrets, then removes the drop-in during cleanup.

The hook environment variables are E2E-only implementation details:

- `PODLAZ_E2E_TUN_HOOKS` enables daemon-side E2E hooks;
- `PODLAZ_E2E_TUN_HOOK_PHASE` selects the precise phase under test;
- `PODLAZ_E2E_TUN_HOOK_DIR` stores temporary marker files for runner coordination;
- `PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS` bounds the pre-commit pause probe.

Do not set these variables in packaged or production service operation.

## Scripts

| Job | Script | Scope |
| --- | --- | --- |
| CLI contract | `scripts/e2e/cli-contract.sh` | CLI command and error checks. |
| Package and service | `scripts/e2e/package-service.sh` | Package install, reinstall, service, cleanup. |
| Proxy data-plane | `scripts/e2e/data-plane.sh` | Proxy connect, egress, listener scope, cleanup. |
| Maximum server coverage | `scripts/e2e/server-coverage.sh` | Real-provider proxy/TUN probes and snapshots. |
| TUN fault injection | `scripts/e2e/tun-fault-injection.sh` | Explicitly gated DNS/route rollback and pre-commit daemon interruption probes. |

## Non-goals

- Not a fork PR gate.
- Not a replacement for unit tests.
- Not permanent release evidence; keep evidence in issues, PRs, or release notes.
- Not a production fault-injection interface; daemon hooks are for dedicated self-hosted E2E only.
