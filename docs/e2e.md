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
2. removing real `podlaz0` after DNS apply produces the exact supported `resolvectl` missing-link result, diagnostics are persisted before the first DNS rollback invocation, cleanup converges, and an immediate packaged retry succeeds. The gate compares raw stderr bytes and accepts only the documented marker followed by one `LF` or one `CRLF`; no newline deletion or whitespace normalization is allowed.

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
| Installed-package TUN convergence | `scripts/e2e/tun-package-convergence.sh` | Release-like `.deb`, packaged inactive-scope verification, byte-exact real missing-link rollback, private exact route/rule manifests, provenance, tri-state resource absence, unrelated host-state preservation, restart reconciliation, and immediate retry. |
| Installed-package teardown | `scripts/e2e/tun-package-cleanup.sh` | State-aware pre-release verification obligations, post-quiescence authoritative mutation snapshot, exact metadata-driven cleanup, ownership-union verification, identity-material-preserving package purge gate, sentinel removal, and tri-state post-cleanup assertions. |

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
6. A failure after host-networking apply captures a bounded public-safe report before rollback, then rolls back nftables/DNS/routes/rules before Xray is stopped. A composition executor must return partial applied ownership with the error and must not perform hidden cleanup before the transaction boundary records diagnostics.
7. The report exposes a stable `failure_phase`, stable primary classification, safe report path, and `rollback_status`; after rollback and daemon restart, `podlaz doctor --tun --verbose` can read the historical report. Persisted JSON, human output, and JSON client output must independently exclude every injected profile name/ID, transaction ID, endpoint/domain, IPv4/IPv6 address, DNS server, SSID, physical interface, route/rule token, and command-output marker while retaining safe structural verdict evidence.
8. A complete `systemd-resolved` link with all planned DNS servers, `~.`, `+DefaultRoute`, and `Current Scopes: none` passes through the packaged production transaction path. Removing planned server/domain/default-route evidence or returning duplicate target-link sections fails closed and rolls back.
9. Removing `podlaz0` after packaged DNS apply and before rollback makes only the exact normal `resolvectl` exit status `1`, empty raw stdout, and raw stderr equal to the supported marker plus one `LF` or one `CRLF` an idempotent success. Extra blank lines, embedded newlines, unterminated stderr, repeated or mixed line terminators, non-empty stdout, exit status `2`, unrelated exit status `1`, permission denial, launch failure, signal termination, oversized stderr, timeout, and cancellation remain failures. Unit regressions verify the same byte contract used by the package script.
10. A generated runtime config removal failure keeps the transaction cleanup-required and preserves rollback metadata for recovery.
11. `podlaz status`, `podlaz doctor`, and `podlaz recover` agree after rollback/recovery; no cleanup-required transaction or stale startup-scan candidate blocks an immediate subsequent TUN connect.
12. `podlaz recover --execute --yes` after daemon interruption cleans transaction-owned state without deleting `/run/podlaz` wholesale or changing unrelated host networking.

## Installed-package convergence safety

The dedicated scenario starts with idempotent teardown that may remove only exact E2E sentinel identities left by a previous run: the fixed test table, route tuple, policy-rule tuple, dummy DNS link, and transient service. It never treats a shared priority, routing table, or partial match as E2E ownership. Any nonmatching object or reserved-namespace conflict blocks the scenario. After teardown, the scenario verifies that its exact sentinel identities are absent before recreating them.

It installs the freshly built `.deb` and performs an explicit reinstall even when the package version is unchanged. It extracts the built package and compares SHA-256 hashes for `podlaz` and `podlazd` with the installed files. It also verifies that systemd's current `MainPID` executes `/usr/bin/podlazd`, that `/proc/<pid>/exe` has the same hash, and that installed version metadata identifies the tested commit.

Every background connect has a bounded wait whose timeout status is captured before the surrounding shell conditional can overwrite it. A real live child that reaches the bound is escalated through TERM and, when necessary, KILL, then reaped. Child completion remains separate from the child's exit code. A transient `/proc` read failure alone never authorizes `wait`; the helper requires independent failed `kill -0` evidence that the shell's child no longer has a process identity. TERM/KILL escalation requires the same `/proc/<pid>/stat` start time. Pre-release proof capture and child termination are independent operations: capture failure is retained as teardown failure but never skips bounded TERM/KILL/reap, and hook removal occurs only after successful disappearance and reap of the tracked child identity. Xray escalation additionally revalidates the exact executable and transaction-generated config reference before TERM and again before KILL, so PID reuse cannot authorize a signal. Xray discovery is also tri-state: a `pgrep` operational error or a live candidate whose `/proc` identity cannot be validated blocks teardown rather than being treated as no Xray process.

The scenario trap captures a private pre-release verification-only manifest before CLI cancellation and before hook removal. Durable applied/rollback route and policy-rule ownership is always recorded as an absence obligation. For `planned` and states after successful `Apply`, an exact desired tuple without durable ownership is inspected at capture time: an already-present tuple is validated pre-existing host state and is excluded, while an absent tuple is recorded so a tuple created during cancellation or hook release must later disappear. `applying` is different: production enters that state before calling the composed executor and persists `applied_steps` plus rollback metadata only after `Apply` returns. Routes and policy rules may therefore already be podlaz-created while durable proof is still empty. Every unowned desired route/rule in `applying` is consequently recorded as an obligation without using current host presence as baseline evidence. Any metadata or required route/rule inspection error fails capture closed. The captured manifest can never authorize deletion. Capture failure is exported to the shared teardown, bounded child termination still runs independently, and recovery is skipped because the original proof is unavailable.

After hook removal, teardown runs bounded daemon recovery only when the early proof is valid, stops `podlazd`, stops every transaction-owned Xray, and proves both process classes absent. Only after all podlaz network mutators are quiescent does it parse surviving durable applied ownership and atomically create the post-quiescence authoritative manifest that alone may authorize fallback route/rule mutation.

After recovery and authoritative fallback mutation, teardown verifies absence of the union of exact obligations recorded by the pre-release manifest and exact ownership recorded by the authoritative manifest. This catches both a tuple that was absent at capture and appeared during cancellation or hook release, and an `applying` tuple that was already present as an in-flight mutation before capture, when either survives after transaction metadata is deleted. Baseline-eligible tuples already present before capture remain excluded. The early manifest remains verification-only: a remaining obligation causes teardown failure instead of authorizing deletion. Generated configuration, transaction state, and package executables are removed only after the complete obligation/ownership union is proven absent.

The validator treats `desired_plan` as intent and target-shape validation, never as proof that podlaz created an object and never as mutation authority. Network ownership is the exact multiset represented by durable route/policy-rule `applied_steps`; rollback routes/rules must equal that multiset exactly, including duplicate count. Desired tuples without durable ownership affect only the early verification envelope. For `planned`, `applied`, `verifying`, `committed`, `rolling_back`, and `failed`, exact host inspection may prove a present unowned tuple was pre-existing; an absent tuple remains an obligation because it may appear during cancellation or hook release. For `applying`, all unowned desired route/rule tuples remain obligations regardless of current presence because no true pre-mutation baseline exists at that boundary. A `planned` transaction containing network applied/rollback ownership is rejected as inconsistent. Mixed transactions grant mutation authority only for objects represented by durable applied steps; verification-only obligations never authorize deletion.

A surviving terminal `rolled_back` file is treated differently. Production has already completed rollback and relinquished ownership before the separate file deletion operation. Cleanup validates the stale record identity but contributes none of its historical route/rule tuples to the mutation manifest. The stale metadata is removed only after process and host-state safety checks; it can never authorize repeated deletion of a tuple created later by another owner.

A routing table number or rule priority is a namespace hint, not proof of ownership. Fallback never flushes table `51820` and never deletes every rule at priority `9999` or `10000`. It removes only exact persisted tuples: destination, gateway, device and table for routes; priority, selectors, mark and table for policy rules. The logical `podlaz` table name and numeric `51820` are normalized to the same identity both in persisted metadata and observed `ip rule show` output. Every route/rule inspection command must exit successfully; permission, netlink, launch, or other operational failures are inspection failures, not proof of absence. An unrecorded object in a reserved namespace is not deleted; it causes teardown to fail as an ownership conflict.

If daemon stop, Xray termination, `pgrep`, or `/proc` identity inspection fails, cleanup preserves all identity material and returns failure. If either ownership snapshot is invalid, the authoritative cleanup cannot be proven, or any tuple from the verification/ownership union remains, generated configuration and transaction metadata are preserved and package purge is refused. Package purge requires both process quiescence and successful union verification.

After normal daemon recovery and exact metadata-driven fallback, teardown accumulates rather than suppresses failures. Final checks use a tri-state contract: confirmed absence is success, confirmed presence is failure, and an operational inspection error is also failure. The contract covers both private manifests, route/rule inspection, links, resolver state, nftables, Xray process discovery and identity, generated and transaction paths, systemd service state, package database state, exact E2E sentinels, and direct DNS plus IPv4 egress. A failed or timed-out package purge records `package_purged=false`; no inspector error can publish `cleanup_assertions=pass`.

For each successful packaged connect, the scenario snapshots a private exact route/policy-rule manifest before disconnect. For missing-link fault injection it snapshots the same manifest before releasing the daemon hook. Immediately after production disconnect or rollback, it verifies every exact tuple absent, including arbitrary main-table server-bypass routes and managed-table rules. A remaining tuple or any `ip` inspection failure prevents both `network_absent=pass` and `resources_absent=pass`. The manifest stays in the private temporary directory and is never uploaded.

The scenario timeout is shorter than the job timeout so cleanup retains a dedicated execution window. Failure to prove clean host state fails the workflow. Artifact scanning and upload are permitted only after teardown reports success.

## TUN fault-injection coverage

General TUN fault-injection coverage is opt-in. The general workflow job exits without host disruption unless `PODLAZ_E2E_ENABLE_TUN_FAULT_INJECTION=true` is set for the self-hosted runner environment.

When enabled, `scripts/e2e/tun-fault-injection.sh` installs a temporary systemd drop-in and runs bounded packaged scenarios for DNS apply failure, route apply failure, post-production network verification failure, synthetic `Current Scopes: none`, and pre-commit interruption. It also runs real-subprocess resolved matrices, proves diagnostic persistence precedes rollback, reloads historical diagnostics after daemon restart, retries immediately, verifies clean lifecycle publication, preserves a foreign nftables sentinel, scans artifacts for configured sensitive values, and removes E2E-only state during cleanup.

`scripts/e2e/tun-package-convergence.sh` is a separate release-like gate. It builds, installs, and reinstalls the branch `.deb`; verifies package and running-daemon provenance; executes the inactive-scope and real missing-link acceptance cases; validates the missing-link stderr file byte-for-byte with the same one-`LF`/one-`CRLF` contract as production; persists only normalized summaries; and delegates failure-path cleanup to the same conservative teardown helper used by the workflow's `always` step.

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

Raw public or local IP addresses, gateways, interface names, DNS server/domain output, complete routes, `ip link` output, resolver status, provider URLs, subscription links, credentials, generated configs, private exact network manifests, and unredacted logs must stay outside the artifact directory. The dedicated workflow scans the artifact directory against configured secrets and network values collected from the host. Evidence is uploaded only when both the teardown assertions and pre-upload scan pass.

## Non-goals

- Not a default PR gate for unrelated changes.
- Not an automatic post-merge check.
- Not a GitHub-hosted CI replacement.
- Not permanent release evidence; keep evidence in issues, PRs, or release notes.
- Not a production fault-injection interface; daemon hooks are for dedicated self-hosted E2E only.
