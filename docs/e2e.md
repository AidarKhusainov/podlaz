# Self-hosted E2E

Manual host validation for behavior that is not suitable for the default pull-request gate.

The repository keeps `.github/workflows/e2e.yml` and `.github/workflows/e2e-tun-package-convergence.yml` as `workflow_dispatch` workflows for maintainers who have a compatible self-hosted runner. E2E must be started explicitly from the GitHub Actions UI or by running the relevant `scripts/e2e/*.sh` checks manually on a controlled Linux host. It is optional infrastructure in general, but an issue or pull request may require a particular E2E result before completion. Record unavailable infrastructure or completed evidence in the related pull request, issue, or release notes.

The repository intentionally does not auto-dispatch E2E on `push`, `pull_request`, or `schedule`. E2E results are manually requested validation and release evidence, not an automatic default gate. When an issue explicitly requires package-level disposable-host validation, the corresponding pull request must remain draft until that workflow passes and safe evidence is published.

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

The dedicated convergence workflow is the required gate for issue #236 and equivalent changes to TUN rollback convergence. A single successful run must cover both packaged acceptance cases:

1. valid per-link DNS with all planned servers, `~.`, `+DefaultRoute`, and synthetic `Current Scopes: none` is accepted through the installed production daemon;
2. removing real `podlaz0` after DNS apply produces the exact supported `resolvectl` missing-link result, diagnostics are persisted before the first DNS rollback invocation, cleanup converges, and an immediate packaged retry succeeds.

A green result from only the general E2E workflow does not replace this dedicated gate.

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
| Installed-package TUN convergence | `scripts/e2e/tun-package-convergence.sh` | Release-like `.deb`, packaged inactive-scope verification, real missing-link rollback, provenance, direct resource absence, unrelated host-state preservation, restart reconciliation, and immediate retry. |
| Installed-package teardown | `scripts/e2e/tun-package-cleanup.sh` | Bounded daemon recovery, exact metadata-driven network cleanup, package purge, sentinel removal, and direct post-cleanup assertions. |

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
6. A failure after host-networking apply captures a bounded redacted report before rollback, then rolls back nftables/DNS/routes/rules before Xray is stopped. A composition executor must return partial applied ownership with the error and must not perform hidden cleanup before the transaction boundary records diagnostics.
7. The report exposes a stable `failure_phase`, stable primary classification, safe report path, and `rollback_status`; after rollback and daemon restart, `podlaz doctor --tun --verbose` can read the historical report.
8. A complete `systemd-resolved` link with all planned DNS servers, `~.`, `+DefaultRoute`, and `Current Scopes: none` passes through the packaged production transaction path. Removing planned server/domain/default-route evidence or returning duplicate target-link sections fails closed and rolls back.
9. Removing `podlaz0` after packaged DNS apply and before rollback makes only the exact normal `resolvectl` exit status `1`, empty stdout, and bounded exact `No such device` stderr an idempotent success. Non-empty stdout, exit status `2`, unrelated exit status `1`, permission denial, launch failure, signal termination, oversized stderr, timeout, and cancellation remain failures in executor and recovery subprocess tests.
10. A generated runtime config removal failure keeps the transaction cleanup-required and preserves rollback metadata for recovery.
11. `podlaz status`, `podlaz doctor`, and `podlaz recover` agree after rollback/recovery; no cleanup-required transaction or stale startup-scan candidate blocks an immediate subsequent TUN connect.
12. `podlaz recover --execute --yes` after daemon interruption cleans transaction-owned state without deleting `/run/podlaz` wholesale or changing unrelated host networking.

## Installed-package convergence safety

The dedicated scenario starts with idempotent teardown that may remove only exact E2E sentinel identities left by a previous run: the fixed test table, route tuple, policy-rule tuple, dummy DNS link, and transient service. It never treats a shared priority, routing table, or partial match as E2E ownership. Any nonmatching object or reserved-namespace conflict blocks the scenario. After teardown, the scenario verifies that its exact sentinel identities are absent before recreating them.

It installs the freshly built `.deb` and performs an explicit reinstall even when the package version is unchanged. It extracts the built package and compares SHA-256 hashes for `podlaz` and `podlazd` with the installed files. It also verifies that systemd's current `MainPID` executes `/usr/bin/podlazd`, that `/proc/<pid>/exe` has the same hash, and that installed version metadata identifies the tested commit.

Every background connect has a bounded wait. Child completion is tracked separately from the child's exit code: after `wait` reaps a process, that PID is never signalled. TERM/KILL escalation requires the same `/proc/<pid>/stat` start time. Xray escalation additionally revalidates the exact executable and transaction-generated config reference before TERM and again before KILL, so PID reuse cannot authorize a signal.

The scenario trap and the workflow `if: always()` step both invoke `tun-package-cleanup.sh`. Before any route or policy-rule mutation, teardown parses every transaction file and atomically snapshots validated rollback metadata into a private temporary manifest. Unreadable JSON, an unsupported schema or owner, unexpected directory entries, or cleanup-required/committed state without exact rollback tuples makes teardown fail closed. The original transaction metadata is retained for inspection and recovery.

A routing table number or rule priority is a namespace hint, not proof of ownership. Fallback never flushes table `51820` and never deletes every rule at priority `9999` or `10000`. It removes only exact persisted tuples: destination, gateway, device and table for routes; priority, selectors, mark and table for policy rules. The logical `podlaz` table name is normalized to numeric table `51820` before execution. An unrecorded object in a reserved namespace is not deleted; it causes teardown to fail as an ownership conflict.

After normal daemon recovery and exact metadata-driven fallback, teardown accumulates rather than suppresses failures. It verifies the private manifest, reserved namespace absence, `podlaz0`, resolver and nftables state, transaction-owned Xray identity, generated config and transaction directories, hook drop-in and hook directory, every exact E2E sentinel including route/rule/service state, package absence after purge, and restored direct DNS plus IPv4 egress. Transaction metadata is removed only after all recorded network tuples and other podlaz-owned fallback state are proven clean. A failed or timed-out package purge records `package_purged=false`.

The scenario timeout is shorter than the job timeout so cleanup retains a dedicated execution window. Failure to prove clean host state fails the workflow. Artifact scanning and upload are permitted only after teardown reports success.

## TUN fault-injection coverage

General TUN fault-injection coverage is opt-in. The general workflow job exits without host disruption unless `PODLAZ_E2E_ENABLE_TUN_FAULT_INJECTION=true` is set for the self-hosted runner environment.

When enabled, `scripts/e2e/tun-fault-injection.sh` installs a temporary systemd drop-in and runs bounded packaged scenarios for DNS apply failure, route apply failure, post-production network verification failure, synthetic `Current Scopes: none`, and pre-commit interruption. It also runs real-subprocess resolved matrices, proves diagnostic persistence precedes rollback, reloads historical diagnostics after daemon restart, retries immediately, verifies clean lifecycle publication, preserves a foreign nftables sentinel, scans artifacts for configured sensitive values, and removes E2E-only state during cleanup.

`scripts/e2e/tun-package-convergence.sh` is a separate release-like gate. It builds, installs, and reinstalls the branch `.deb`; verifies package and running-daemon provenance; runs packaged `Current Scopes: none`; waits after real DNS apply; confirms a daemon-owned transaction exists; deletes real `podlaz0`; validates the real `resolvectl revert podlaz0` result; and requires the fixed event order `diagnostics-persisted`, `rollback-started`, `dns-rollback-started`. After daemon restart it directly verifies complete resource absence, unrelated-state preservation, and immediate packaged reconnect/disconnect.

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
- tested commit SHA;
- hashes of the built package and its installed binaries;
- workflow run URL and result;
- normalized pass/fail verdicts;
- bounded diagnostic classifications and lifecycle phases.

Raw public or local IP addresses, gateways, interface names, DNS server/domain output, complete routes, `ip link` output, resolver status, provider URLs, subscription links, credentials, generated configs, and unredacted logs must stay outside the artifact directory. The dedicated workflow scans the artifact directory against configured secrets and network values collected from the host. Evidence is uploaded only when both teardown assertions and the pre-upload scan pass.

## Non-goals

- Not a default PR gate for unrelated changes.
- Not an automatic post-merge check.
- Not a GitHub-hosted CI replacement.
- Not permanent release evidence; keep evidence in issues, PRs, or release notes.
- Not a production fault-injection interface; daemon hooks are for dedicated self-hosted E2E only.
