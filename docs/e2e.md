# Self-hosted E2E

Manual host validation for behavior that is not suitable for the default pull-request gate.

The repository keeps `.github/workflows/e2e.yml` as a `workflow_dispatch` workflow for maintainers who have a compatible self-hosted runner. E2E must be started explicitly from the GitHub Actions UI or by running the relevant `scripts/e2e/*.sh` checks manually on a controlled Linux host. It is optional infrastructure: if no VPS/self-hosted runner is available, record manual evidence in the related pull request, issue, or release notes.

The repository intentionally does not auto-dispatch E2E on `push`, `pull_request`, or `schedule`. E2E results should be treated as manually requested validation and release evidence, not as a default merge blocker.

## Run through GitHub Actions

```text
Actions -> E2E -> Run workflow
```

Default job order:

1. CLI contract
2. Package and service
3. Proxy data-plane
4. Maximum server coverage
5. Gated TUN fault-injection coverage

## When to run

Run E2E validation when a change touches:

- TUN devices or native Xray TUN inbound behavior;
- route, DNS, nftables, firewall, or resolver behavior;
- daemon privilege boundaries;
- systemd service behavior;
- package install, reinstall, purge, or service lifecycle;
- provider-backed proxy/TUN data-plane behavior;
- crash, rollback, fault-injection, diagnostics-before-rollback, or recovery behavior.

## Runner and host requirements

Required runner labels for the workflow:

```text
self-hosted
linux
x64
vpn-e2e
ubuntu-24.04
```

Use a disposable or recoverable Linux host. Full coverage expects:

- systemd;
- `/dev/net/tun`;
- `iproute2`, `nftables`, `resolvectl`, `journalctl`;
- Go from the workflow setup step, or Go 1.26.5 for manual script runs;
- package build tools from `docs/development.md`;
- provider/profile configuration supplied through the runner environment, not committed to the repository.

Additional Debian/Ubuntu or arm64 coverage requires dedicated runners or VMs.

## Scripts

| Job | Script | Scope |
| --- | --- | --- |
| CLI contract | `scripts/e2e/cli-contract.sh` | CLI command and error checks. |
| Package and service | `scripts/e2e/package-service.sh` | Package install, reinstall, service, cleanup. |
| Proxy data-plane | `scripts/e2e/data-plane.sh` | Proxy connect, egress, listener scope, cleanup. |
| Maximum server coverage | `scripts/e2e/server-coverage.sh` | Real-provider proxy/TUN probes and snapshots. |
| TUN fault injection | `scripts/e2e/tun-fault-injection.sh` | Explicitly gated DNS/route rollback and pre-commit daemon interruption probes. |

## Manual script order

```bash
bash scripts/e2e/cli-contract.sh
bash scripts/e2e/package-service.sh
bash scripts/e2e/data-plane.sh
bash scripts/e2e/server-coverage.sh
bash scripts/e2e/tun-fault-injection.sh
```

Run only the subset that matches the risk of the change. For example, a CLI-only change normally does not need provider-backed data-plane coverage.

## Native Xray TUN validation

For changes that touch native Xray TUN startup, record VM or self-hosted runner evidence for these cases:

1. `podlaz connect --mode tun <profile>` starts Xray, verifies `podlaz0`, applies podlaz-owned routes, policy rules, DNS, and nftables, then commits the transaction.
2. `podlaz status` reports active TUN mode with the transaction ID and without exposing generated config content.
3. DNS resolution and TCP egress work through the tunnel after commit.
4. `podlaz disconnect` removes podlaz-owned routes, policy rules, DNS, nftables, generated config, and child process state.
5. A failing `xray test -config` preflight leaves no host-networking mutation and recovery can remove any tracked generated config.
6. A failure after host-networking apply captures a bounded redacted report before rollback, then rolls back nftables/DNS/routes/rules before Xray is stopped.
7. The report exposes a stable `failure_phase`, stable primary classification, safe report path, and `rollback_status`; after rollback and daemon restart, `podlaz doctor --tun --verbose` can read the historical report.
8. A complete `systemd-resolved` link with all planned DNS servers, `~.`, `+DefaultRoute`, and `Current Scopes: none` passes through the packaged production transaction path. Removing any planned server/domain/default-route evidence fails closed and rolls back.
9. Removing `podlaz0` before DNS recovery makes the exact normal `resolvectl` exit status `1` plus bounded `No such device` stderr an idempotent success. Exit status `2`, unrelated exit status `1`, permission denial, signal termination, timeout, and cancellation remain failures.
10. `podlaz status`, `podlaz doctor`, and `podlaz recover` agree after rollback/recovery; no cleanup-required transaction or stale startup-scan candidate blocks an immediate subsequent TUN connect. Cleanup-complete terminal history may remain visible but must be non-blocking.
11. `podlaz recover --execute --yes` after daemon interruption cleans transaction-owned state without deleting `/run/podlaz` wholesale or changing unrelated host networking.

## TUN fault-injection coverage

TUN fault-injection coverage is opt-in. The workflow job is safe by default and exits without host disruption unless `PODLAZ_E2E_ENABLE_TUN_FAULT_INJECTION=true` is set for the self-hosted runner environment.

When enabled, `scripts/e2e/tun-fault-injection.sh` installs a temporary systemd drop-in for `podlazd.service` that enables daemon-owned E2E hooks, runs deterministic DNS apply, route apply, and pre-commit interruption probes, scans its artifacts for configured sensitive values, then removes the drop-in during cleanup.

For TUN lifecycle convergence changes, the fault-injection evidence must additionally prove that diagnostics are persisted before the first rollback command, cleanup completes even when diagnostics fail, the historical report is retained according to the `/run` lifetime, and an immediate retry is not rejected by stale daemon or transaction observations.

The hook environment variables are E2E-only implementation details:

- `PODLAZ_E2E_TUN_HOOKS` enables daemon-side E2E hooks;
- `PODLAZ_E2E_TUN_HOOK_PHASE` selects the precise phase under test;
- `PODLAZ_E2E_TUN_HOOK_DIR` stores temporary marker files for runner coordination;
- `PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS` bounds the pre-commit pause probe.

Do not set these variables in packaged or production service operation.

## Evidence

Record only non-sensitive evidence in the PR or issue:

- host OS and architecture;
- commit SHA;
- commands run;
- pass/fail result;
- redacted diagnostics or artifacts when useful.

Do not paste provider URLs, subscription links, credentials, raw generated configs, or unredacted logs.

## Non-goals

- Not a default PR gate.
- Not an automatic post-merge check.
- Not a GitHub-hosted CI replacement.
- Not permanent release evidence; keep evidence in issues, PRs, or release notes.
- Not a production fault-injection interface; daemon hooks are for dedicated self-hosted E2E only.
