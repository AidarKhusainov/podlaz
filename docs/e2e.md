# Self-hosted E2E

Manual host validation for behavior that is not suitable for the default pull-request gate.

The repository keeps `.github/workflows/e2e.yml` and `.github/workflows/e2e-tun-package-convergence.yml` as `workflow_dispatch` workflows for maintainers who have a compatible self-hosted runner. E2E must be started explicitly from the GitHub Actions UI or by running the relevant `scripts/e2e/*.sh` checks manually on a controlled Linux host. It is optional infrastructure in general, but an issue or pull request may require a particular E2E result before completion. Record unavailable infrastructure or completed evidence in the related pull request, issue, or release notes.

The repository intentionally does not auto-dispatch E2E on `push`, `pull_request`, or `schedule`. E2E results are manually requested validation and release evidence, not an automatic default gate. When an issue explicitly requires package-level disposable-host validation, the corresponding pull request must remain draft until that workflow passes and redacted evidence is published.

## Run through GitHub Actions

General coverage:

```text
Actions -> E2E -> Run workflow
```

Installed-package TUN convergence gate:

```text
Actions -> TUN Package Convergence E2E -> Run workflow
```

The general E2E workflow runs:

1. CLI contract
2. Package and service
3. Proxy data-plane
4. Maximum server coverage
5. Gated TUN fault-injection coverage

The dedicated convergence workflow runs the release-like `.deb` scenario only. It is the required gate for issue #236 and equivalent changes to TUN rollback convergence.

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

Required runner labels for both workflows:

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

The dedicated package convergence workflow requires `PODLAZ_E2E_PROFILE_URI` or `PODLAZ_E2E_PROFILE_URI_LIST` in the `vpn-e2e` environment. Additional Debian/Ubuntu or arm64 coverage requires dedicated runners or VMs.

## Scripts

| Job | Script | Scope |
| --- | --- | --- |
| CLI contract | `scripts/e2e/cli-contract.sh` | CLI command and error checks. |
| Package and service | `scripts/e2e/package-service.sh` | Package install, reinstall, service, cleanup. |
| Proxy data-plane | `scripts/e2e/data-plane.sh` | Proxy connect, egress, listener scope, cleanup. |
| Maximum server coverage | `scripts/e2e/server-coverage.sh` | Real-provider proxy/TUN probes and snapshots. |
| TUN fault injection | `scripts/e2e/tun-fault-injection.sh` | Gated apply/verify failures, pre-rollback diagnostics, resolved subprocess edge cases, immediate retry, unrelated-state preservation, and pre-commit interruption. |
| Installed-package TUN convergence | `scripts/e2e/tun-package-convergence.sh` | Release-like `.deb`, real missing-link rollback, direct resource absence, unrelated host-state preservation, restart reconciliation, and immediate retry. |

## Manual script order

```bash
bash scripts/e2e/cli-contract.sh
bash scripts/e2e/package-service.sh
bash scripts/e2e/data-plane.sh
bash scripts/e2e/server-coverage.sh
bash scripts/e2e/tun-fault-injection.sh
bash scripts/e2e/tun-package-convergence.sh
```

Run only the subset that matches the risk of the change. A CLI-only change normally does not need provider-backed data-plane coverage. A change to transaction rollback, resolved cleanup, or generated runtime configuration requires the installed-package convergence script.

## Native Xray TUN validation

For changes that touch native Xray TUN startup, record VM or self-hosted runner evidence for these cases:

1. `podlaz connect --mode tun <profile>` starts Xray, verifies `podlaz0`, applies podlaz-owned routes, policy rules, DNS, and nftables, then commits the transaction.
2. `podlaz status` reports active TUN mode with the transaction ID and without exposing generated config content.
3. DNS resolution and TCP egress work through the tunnel after commit.
4. `podlaz disconnect` removes podlaz-owned routes, policy rules, DNS, nftables, generated config, and child process state.
5. A failing `xray test -config` preflight leaves no host-networking mutation and recovery can remove any tracked generated config.
6. A failure after host-networking apply captures a bounded redacted report before rollback, then rolls back nftables/DNS/routes/rules before Xray is stopped.
7. The report exposes a stable `failure_phase`, stable primary classification, safe report path, and `rollback_status`; after rollback and daemon restart, `podlaz doctor --tun --verbose` can read the historical report.
8. A complete `systemd-resolved` link with all planned DNS servers, `~.`, `+DefaultRoute`, and `Current Scopes: none` passes through the packaged production transaction path. Removing planned server/domain/default-route evidence or returning duplicate target-link sections fails closed and rolls back.
9. Removing `podlaz0` after packaged DNS apply and before rollback makes only the exact normal `resolvectl` exit status `1`, empty stdout, and bounded exact `No such device` stderr an idempotent success. Non-empty stdout, exit status `2`, unrelated exit status `1`, permission denial, launch failure, signal termination, oversized stderr, timeout, and cancellation remain failures in executor and recovery subprocess tests.
10. A generated runtime config removal failure keeps the transaction cleanup-required and preserves rollback metadata for recovery.
11. `podlaz status`, `podlaz doctor`, and `podlaz recover` agree after rollback/recovery; no cleanup-required transaction or stale startup-scan candidate blocks an immediate subsequent TUN connect.
12. `podlaz recover --execute --yes` after daemon interruption cleans transaction-owned state without deleting `/run/podlaz` wholesale or changing unrelated host networking.

## TUN fault-injection coverage

General TUN fault-injection coverage is opt-in. The general workflow job exits without host disruption unless `PODLAZ_E2E_ENABLE_TUN_FAULT_INJECTION=true` is set for the self-hosted runner environment.

When enabled, `scripts/e2e/tun-fault-injection.sh` installs a temporary systemd drop-in and runs bounded packaged scenarios for DNS apply failure, route apply failure, post-production network verification failure, synthetic `Current Scopes: none`, and pre-commit interruption. It also runs real-subprocess resolved matrices, proves diagnostic persistence precedes rollback, reloads historical diagnostics after daemon restart, retries immediately, verifies clean lifecycle publication, preserves a foreign nftables sentinel, scans artifacts for configured sensitive values, and removes E2E-only state during cleanup.

`scripts/e2e/tun-package-convergence.sh` is a separate release-like gate. It builds and installs the branch `.deb`, verifies that systemd executes `/usr/bin/podlazd`, waits after real DNS apply, confirms a daemon-owned transaction file exists, deletes real `podlaz0`, captures the real `resolvectl revert podlaz0` result, releases the installed daemon, and requires `network-verify` diagnostics to be persisted before rollback starts. After daemon restart it directly verifies absence of `podlaz0`, table `51820` routes, podlaz policy rules, the resolved link record, `inet podlaz`, Xray, generated config, and transaction files. It also preserves independent nftables, route, policy-rule, per-link DNS, service, default-route, and `systemd-resolved` sentinels and performs an immediate packaged reconnect/disconnect.

The hook event log contains only fixed lifecycle markers used to prove diagnostic/rollback ordering. It must not contain profile material, command output, addresses, or generated configuration.

The hook environment variables are E2E-only implementation details:

- `PODLAZ_E2E_TUN_HOOKS` enables daemon-side E2E hooks;
- `PODLAZ_E2E_TUN_HOOK_PHASE` selects the precise phase under test;
- `PODLAZ_E2E_TUN_HOOK_DIR` stores temporary marker files and lifecycle events;
- `PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS` bounds the pause probe.

Do not set these variables in packaged or production service operation.

## Evidence

Record only non-sensitive evidence in the PR or issue:

- host OS and architecture;
- commit SHA;
- workflow run URL and result;
- commands run;
- pass/fail result;
- redacted diagnostics or artifacts when useful.

Do not paste provider URLs, subscription links, credentials, raw generated configs, or unredacted logs.

## Non-goals

- Not a default PR gate for unrelated changes.
- Not an automatic post-merge check.
- Not a GitHub-hosted CI replacement.
- Not permanent release evidence; keep evidence in issues, PRs, or release notes.
- Not a production fault-injection interface; daemon hooks are for dedicated self-hosted E2E only.
