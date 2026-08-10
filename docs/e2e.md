# Self-hosted E2E

Manual host validation for behavior that is not suitable for the default pull-request gate.

The repository keeps `.github/workflows/e2e.yml`, `.github/workflows/e2e-tun-package-convergence.yml`, and `.github/workflows/e2e-tun-resource-soak.yml` as `workflow_dispatch` workflows for maintainers who have a compatible self-hosted runner. E2E must be started explicitly from the GitHub Actions UI or by running the relevant `scripts/e2e/*.sh` checks manually on a controlled Linux host. It is optional infrastructure in general, but an issue or pull request may require a particular E2E result before completion. Record unavailable infrastructure or completed evidence in the related pull request, issue, or release notes.

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

Installed-package TUN resource-lifecycle gate:

```text
Actions -> TUN Resource Soak E2E -> Run workflow
```

The general E2E workflow runs:

1. CLI contract
2. Package and service
3. Proxy data-plane
4. Maximum server coverage
5. Gated TUN fault-injection coverage

The dedicated convergence workflow is the required gate for issue #236, issue #243, and equivalent changes when their acceptance criteria require installed-package TUN/resolver convergence. A single successful run covers the applicable packaged acceptance cases:

1. valid per-link DNS with all planned servers, `~.`, `+DefaultRoute`, and synthetic `Current Scopes: none` is accepted through the installed production daemon;
2. removing real `podlaz0` after DNS apply produces the exact supported result from the daemon-owned production `resolvectl revert podlaz0` rollback call, diagnostics are persisted before that call, cleanup converges, and an immediate packaged retry succeeds. No preliminary manual revert is permitted. Strictly gated instrumentation captures the production call's exit code and raw stdout/stderr in the private hook directory; the gate accepts only exit code `1`, empty raw stdout, and the documented marker followed by one `LF` or one `CRLF`, without newline deletion or whitespace normalization;
3. issue #243 verifies the separate read-only `resolvectl status podlaz0 --no-pager` absence protocol on Ubuntu 24.04/systemd 255. The initial inactive boundary first must publish healthy `podlaz status` and clean `recover --json`. The gate then boundedly waits for the exact supported exit-`0`, empty-stdout, byte-exact missing-device stderr envelope; during that wait only the already-supported proven-empty transient Link shape may be treated as an intermediate state, and any other raw process/result shape fails closed. The scenario then verifies clean `recover --execute --yes --json` followed by a fresh clean scan, performs two consecutive healthy active status reads, normal disconnect, immediate clean inactive status/recovery publication, immediate reconnect, and a repeated lifecycle without restarting `podlazd` or `systemd-resolved`. Post-disconnect success is defined by the product's semantic absence contract and does not require a particular raw resolver representation. Raw resolver/profile/host evidence stays in the private E2E temporary area; uploaded evidence contains only normalized structural verdicts and remains subject to the workflow redaction scan.

A green result from only the general E2E workflow does not replace this dedicated gate.

## When to run

Run E2E validation when a change touches:

- TUN devices or native Xray TUN inbound behavior;
- route, DNS, nftables, firewall, or resolver behavior;
- current TUN health, suspend/resume, network-generation tracking, or event-source resynchronization;
- daemon privilege boundaries;
- systemd service behavior;
- package install, reinstall, purge, or service lifecycle;
- provider-backed proxy/TUN data-plane behavior;
- crash, rollback, fault-injection, diagnostics-before-rollback, or recovery behavior;
- long-lived process, goroutine, thread, file-descriptor, task, timer, cache, or memory lifecycle behavior.

## Runner and host requirements

Required runner labels for the dedicated self-hosted workflows:

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
- a unified cgroup v2 hierarchy and readable root-level procfs/cgroup structural metrics;
- `iproute2`, `nftables`, `resolvectl`, `journalctl`;
- Go from the workflow setup step, or Go 1.26.5 for manual script runs;
- package build tools from `docs/development.md`;
- provider/profile configuration supplied through the runner environment, not committed to the repository.

The dedicated package convergence and resource-soak workflows require `PODLAZ_E2E_PROFILE_URI` or `PODLAZ_E2E_PROFILE_URI_LIST` in the `vpn-e2e` environment. Additional Debian/Ubuntu or arm64 coverage requires dedicated runners or VMs.

## Scripts

| Job | Script | Scope |
| --- | --- | --- |
| CLI contract | `scripts/e2e/cli-contract.sh` | CLI command and error checks. |
| Package and service | `scripts/e2e/package-service.sh` | Package install, reinstall, service, cleanup. |
| Proxy data-plane | `scripts/e2e/data-plane.sh` | Proxy connect, egress, listener scope, cleanup. |
| Maximum server coverage | `scripts/e2e/server-coverage.sh` | Real-provider proxy/TUN probes and snapshots. |
| TUN fault injection | `scripts/e2e/tun-fault-injection.sh` | Gated apply/verify failures, pre-rollback diagnostics, resolved subprocess edge cases, immediate retry, unrelated-state preservation, and pre-commit interruption. |
| Installed-package TUN convergence | `scripts/e2e/tun-package-convergence.sh` | Release-like `.deb`, packaged inactive-scope verification, byte-exact capture of the actual production missing-link rollback, private exact route/rule manifests, provenance, tri-state resource absence, unrelated host-state preservation, restart reconciliation, and immediate retry. |
| Installed-package TUN resource soak | `scripts/e2e/tun-resource-soak.sh` | Exact package/Xray provenance, cgroup-v2 and procfs attribution for podlazd plus its exact supervised Xray child, bounded active traffic/health sampling, strict disconnect cleanup, immediate reconnect non-accumulation, and sanitized trend evidence. |
| Issue #243 resolver acceptance | `scripts/e2e/issue243-package-acceptance.sh` | Installed-package clean inactive baseline, bounded convergence to the read-only exit-0 resolver envelope, recover-execute refresh, repeated active status, disconnect convergence, immediate reconnect, and normalized safe evidence. |
| Installed-package teardown | `scripts/e2e/tun-package-cleanup.sh` | State-aware pre-release verification obligations, post-quiescence authoritative mutation snapshot, exact metadata-driven cleanup, ownership-union verification, identity-material-preserving package purge gate, sentinel removal, and tri-state post-cleanup assertions. |

## Manual script order

```bash
bash scripts/e2e/cli-contract.sh
bash scripts/e2e/package-service.sh
bash scripts/e2e/data-plane.sh
bash scripts/e2e/server-coverage.sh
bash scripts/e2e/tun-fault-injection.sh
bash scripts/e2e/tun-package-convergence.sh
bash scripts/e2e/tun-resource-soak.sh
```

Run only the subset that matches the risk of the change. A CLI-only change normally does not need provider-backed data-plane coverage. A change to transaction rollback, resolved cleanup, generated runtime configuration, current TUN health, suspend/resume handling, or event-source resynchronization requires installed-package/target-host TUN validation. A change that can retain session-owned processes, workers, timers, file descriptors, reports, buffers, or other resources requires the dedicated installed-package resource soak.

## Native Xray TUN validation

For changes that touch native Xray TUN startup, record VM or self-hosted runner evidence for these cases:

1. `podlaz connect --mode tun <profile>` starts Xray, verifies `podlaz0`, applies podlaz-owned routes, policy rules, DNS, and nftables, then commits the transaction.
2. `podlaz status` reports active TUN mode with the transaction ID and without exposing generated config content.
3. DNS resolution and TCP egress work through the tunnel after commit.
4. `podlaz disconnect` removes podlaz-owned routes, policy rules, DNS, nftables, generated config, and child process state.
5. A failing `xray test -config` preflight leaves no host-networking mutation and recovery can remove any tracked generated config.
6. A failure after host-networking apply captures a bounded public-safe report before rollback, then rolls back nftables/DNS/routes/rules before Xray is stopped. A composition executor must return partial applied ownership with the error and must not perform hidden cleanup before the transaction boundary records diagnostics.
7. The report exposes a stable `failure_phase`, stable primary classification, safe report path, and `rollback_status`; `completed` is permitted only after host rollback and bounded supervised Xray stop both succeed. A TERM-ignoring child must be escalated to KILL and reaped. Any stop/force-stop error must produce `rollback_status=failed` and retain cleanup-required transaction ownership. After successful rollback and daemon restart, `podlaz doctor --tun --verbose` can read the historical report. Persisted JSON, human output, and JSON client output must independently exclude every injected profile name/ID, transaction ID, endpoint/domain, IPv4/IPv6 address, DNS server, SSID, physical interface, route/rule token, and command-output marker while retaining safe structural verdict evidence.
8. A complete `systemd-resolved` link with exactly all planned DNS servers, exactly `~.`, `+DefaultRoute`, and `Current Scopes: none` passes through the packaged production transaction path. Removing planned server/domain/default-route evidence, adding an extra DNS server/domain, or returning duplicate target-link sections fails closed and rolls back.
9. Removing `podlaz0` after packaged DNS apply and before rollback makes only the actual daemon-owned `resolvectl revert podlaz0` result with exit status `1`, empty raw stdout, and raw stderr equal to the supported marker plus one `LF` or one `CRLF` an idempotent success. The gate must not issue an earlier manual revert. Extra blank lines, embedded newlines, unterminated stderr, repeated or mixed line terminators, non-empty stdout, exit status `2`, unrelated exit status `1`, permission denial, launch failure, signal termination, oversized stderr, timeout, cancellation, and capture failure remain failures. Unit regressions verify the same production capture and byte contract used by the package script.
10. A generated runtime config removal failure keeps the transaction cleanup-required and preserves rollback metadata for recovery.
11. `podlaz status`, `podlaz doctor`, and `podlaz recover` agree after rollback/recovery; no cleanup-required transaction or stale startup-scan candidate blocks an immediate subsequent TUN connect.
12. `podlaz recover --execute --yes` after daemon interruption cleans transaction-owned state without deleting `/run/podlaz` wholesale or changing unrelated host networking.
13. The podlaz-owned nftables table is accepted only when chain cardinality/name/type/hook/numeric priority/policy and ordered rule cardinality/content exactly match the canonical plan; an added foreign/extra rule or chain metadata drift must fail closed.

## Issue #245 suspend/resume and revalidation acceptance

Changes to current TUN health require target-host evidence that cannot be replaced by unit tests alone. The packaged acceptance run must use an active committed TUN session and exercise a real suspend/resume or equivalent controlled underlying-uplink transition while preserving the daemon process where practical.

The run must prove all of the following:

1. Immediately after connect, generation 1 is `verified` only after a fresh post-commit authoritative observation has itself passed canonical composition and connectivity verification. There must be no publication interval where a new unverified fingerprint is exposed as `verified`.
2. A real post-resume signal forces reproof even when interface, ifindex, gateway, address, and NetworkManager identity are unchanged. `committed` may remain the durable transaction state, but active status returns exit `0` only after current health returns to `verified`.
3. A material change to underlying interface/ifindex, gateway, relevant IPv4 address, active NetworkManager connection identity, or server-bypass next hop advances `network_generation`; ordinary unchanged event bursts do not create repeated generations or unbounded probes.
4. If NetworkManager itself is detected but active-connection inspection fails, the fingerprint is unavailable/fail-closed. The run must not treat that observation as authoritative unmanaged/empty identity.
5. Events that occur while connect, disconnect, or recovery owns the lifecycle mutation boundary are not lost. A consumed pending trigger waits for mutation-idle; an in-flight revalidation cancelled by mutation is requeued; the post-mutation attempt takes a fresh authoritative snapshot before making the fingerprint decision.
6. Both logind and rtnetlink event sources follow subscribe-then-resync semantics. A controlled source reconnect must queue one coalesced authoritative resync, so a transition during the watcher outage cannot leave stale `verified` health until an unrelated future event.
7. The verification phase remains read-only: it may execute snapshot/status/route-get, canonical verification, and bounded DNS UDP/TCP, system-resolution, TCP/443, TLS, and HTTPS probes, but it must not apply or roll back routes, flush route cache, mutate DNS/nftables/NetworkManager, recreate TUN state, broaden cleanup authority, or perform repair.
8. At least one controlled terminal case must be exercised after fresh ownership has been proved: force a required verification layer to fail or force the revalidation deadline to expire. Old `verified` evidence must remain invalid, the failed layer or timeout classification must be observable, and the session must not remain indefinitely active as only `degraded`.
9. Before the first automatic rollback mutation, the bounded centrally redacted TUN diagnostic report must already exist with `rollback_status=pending` and the terminal failure phase/classification. Private ordering evidence may prove this boundary, but raw host-specific values must remain outside uploaded artifacts.
10. The terminal handoff must occur only after read-only revalidation authority has been released. The daemon then invokes the normal exact transaction-backed `Disconnect` path automatically, and that disconnect must converge within the documented cleanup bound rather than waiting for another revalidation/probe deadline.
11. When automatic cleanup succeeds, the session must converge to inactive, the historical diagnostic report must finalize to `rollback_status=completed`, the transaction-owned Xray process and podlaz-owned network state must be absent, and no cleanup-required recovery candidate may remain.
12. A controlled cleanup-failure case must prove the opposite terminal branch: the diagnostic report finalizes to `rollback_status=failed`, current health becomes `cleanup-required`, and durable exact ownership remains available for later recovery rather than falsely publishing clean inactive state.
13. Explicit `podlaz disconnect`, recovery, and daemon shutdown cancellation during an in-flight revalidation must retain lifecycle precedence and must not schedule a second recursive automatic disconnect. Evidence must show one lifecycle cleanup owner and bounded convergence for the initiating operation.
14. Public evidence records only structural health state, generation transitions, terminal classifications/phases, diagnostic/rollback ordering verdicts, timing bounds, and pass/fail results. Raw host addresses, gateways, physical interface names, NetworkManager UUIDs/names, server endpoints, resolver output, or other host-specific values remain in private E2E evidence and are removed before artifact scanning.

A successful target-host run is evidence for the complete #245 current-health revalidation contract: the verification phase itself remains read-only, while a proved terminal verification failure/deadline is followed, only after revalidation authority is released and diagnostics are persisted, by the normal bounded transaction-backed disconnect as a fail-safe lifecycle disposition. That disconnect is not repair and does not authorize speculative route/DNS/firewall/NetworkManager mutation or foreign cleanup; those remain separately evidence-gated by issue #245.

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

`scripts/e2e/tun-package-convergence.sh` is a separate release-like gate. It
builds, installs, and reinstalls the branch `.deb`; verifies package and
running-daemon provenance; proves that a foreign `198.18.0.1/32` conflict blocks
before podlaz mutation; verifies the exact address is present once on `podlaz0`;
performs an uncached interface-scoped DNS query whose result identifies
`podlaz0`; executes the inactive-scope and real missing-link acceptance cases; deletes real `podlaz0` and releases the daemon hook without issuing a manual revert; waits for the failed connect and production rollback to finish; then checks the private capture of that exact production `resolvectl revert`
exit code and raw stdout/stderr byte-for-byte. The fault-injection scenario also
fails immediately after real TUN address mutation, requires the exact ownership
step and `tun_address_apply_failure`, proves address/routes/rules/DNS/nftables
absence and no cleanup-required transaction before any restart or explicit
recovery, then reconnects and disconnects on the same `podlazd` and
`systemd-resolved` lifecycle and proves clean state again. Capture is enabled only for the rollback delegate, remains outside uploaded artifacts, and fails rollback closed if capture cannot be persisted. The scenario persists only normalized summaries and delegates failure-path cleanup to the same conservative teardown helper used by the workflow's `always` step.

The issue #243 acceptance step runs after the base package-convergence scenario and the issue #241 acceptance on the same installed branch package. It first requires the daemon-published inactive state and dry-run recovery view to be clean. Because the preceding lifecycle may still leave the already-supported proven-empty `systemd-resolved` Link record, the step then uses a bounded raw-status convergence loop: the exact exit-0 missing-device envelope completes the proof, the strict proven-empty transient shape is the only retryable intermediate result, and every other process/output shape fails closed. After normal disconnect, the step immediately checks clean inactive `podlaz status` and clean `recover --json` without requiring the same raw representation, then reconnects and repeats the lifecycle without restarting `podlazd` or `systemd-resolved`.

The hook event log contains only fixed lifecycle markers used to prove diagnostic/rollback ordering. It must not contain profile material, command output, addresses, or generated configuration. Raw production rollback capture files remain in the private hook directory and are removed before artifact scanning.

The hook environment variables are E2E-only implementation details:

- `PODLAZ_E2E_TUN_HOOKS` enables daemon-side E2E hooks;
- `PODLAZ_E2E_TUN_HOOK_PHASE` selects the precise phase under test;
- `PODLAZ_E2E_TUN_HOOK_DIR` stores temporary marker files and lifecycle events;
- `PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS` bounds the pause probe.

Do not set these variables in packaged or production service operation.


## Installed-package TUN resource soak

`TUN Resource Soak E2E` is the manual, self-hosted gate for long-lived TUN
resource-lifecycle changes. It builds and reinstalls the exact branch package,
proves the running daemon and bundled Xray hashes match that package, starts from
a clean inactive lifecycle boundary, and establishes exact process attribution
before warm-up. The release gate is a **three-hour post-warm-up** sampling window
with a 120-second warm-up and 60-second default interval. A shorter run may be
used to develop or reproduce a signal, but it is observation evidence rather
than the release gate.

Attribution is fail-closed. `scripts/e2e/lib/tun_soak_metrics.py` identifies
podlazd from the systemd `MainPID`, identifies the exact supervised Xray child
from the one committed Podlaz TUN transaction, verifies executable identity,
process start time, and the direct child relationship to the exact daemon. Both
processes must belong to the same `podlazd.service` cgroup. A foreign VPN/core
process aborts the clean run rather than contaminating accounting. Exact PIDs,
transaction metadata, generated configuration references, process command data,
and host networking evidence remain in the private E2E directory and are
removed before artifact scanning.

The bounded current-health convergence gate runs after each connect and during
periodic status checks. It retries only structurally valid `revalidating` or
transient `degraded` publications. It accepts only `verified` with status exit `0`;
`inactive`, `cleanup-required`, malformed evidence, command failure, or timeout
fails closed. Raw status output remains private, while public failure evidence
contains only an allowlisted structural verdict.

The following observations have distinct authority and must not be conflated:

- cgroup total `memory.current`, optional `memory.peak`, `pids.current`, and CPU
  time describe the whole service cgroup, not either process individually;
- podlazd and Xray RSS, PSS where available, thread/task count, file-descriptor
  count, and CPU time come from the exact process identity in procfs;
- current memory is a point-in-time footprint, while peak memory is a historical
  high-water mark and is never treated as a sustained-growth signal;
- warm-up/cache movement is allowed, while a materially sustained trend after
  warm-up is evaluated separately for each component and metric.

Trend analysis uses all first-session active samples after warm-up. It records a
robust Theil-Sen slope, net growth, early/late medians, positive-delta fraction,
and a metric-specific noise floor. At least six samples are required. One generic
RSS ceiling or one generic slope is not applied to cgroup total, podlazd, and
Xray. `scripts/e2e/tun-resource-soak-policy.json` has two modes:

- `observe` attributes a reproduced growth candidate without pretending that an
  uncalibrated threshold is a release assertion;
- `accept` requires a named reproduced growth signal plus explicit
  metric-specific slope, net-growth, and sustained-trend rules. Any other
  sustained candidate without its own rule fails closed.

An `observe` result is not sufficient to close a resource-retention defect. The
final release gate for such a fix must use `accept` after repeated clean
baselines identify the responsible metric and justify its envelope.

Lifecycle assertions remain stricter than trend tolerance. Normal disconnect
must terminate the exact supervised Xray child, leave no packaged Xray orphan,
remove exact transaction-owned routes/rules and all Podlaz TUN/DNS/nftables,
generated-config, and transaction state, and publish clean recovery state.
Post-cleanup podlazd/cgroup thread, task, file-descriptor, and PID counts may not
increase over the equivalent inactive baseline. Current-memory comparisons use
only the documented cleanup tolerance; that tolerance is a test envelope, not a
production memory limit. Immediate reconnect must keep the same daemon, create a
new exact supervised Xray identity, remain within the documented per-metric
reconnect tolerance, and pass a second strict disconnect cleanup.

The public artifact contains only sanitized structural samples and one compact
JSON report with exact package/Xray provenance, configuration, component-specific
trend summaries, lifecycle verdicts, and policy verdict. It never contains
profile identifiers, provider domains, addresses, SSIDs, gateways, resolver
values, process identities, generated configuration, or raw health output. The
harness adds no production pprof listener or debug endpoint; any future Go
runtime profiling boundary requires separate security review.

## Evidence

Record only non-sensitive evidence in the PR or issue:

- host OS and architecture;
- tested commit SHA;
- hashes of the built package and its installed binaries;
- workflow run URL and result;
- normalized pass/fail verdicts;
- bounded diagnostic classifications and lifecycle phases;
- for current-health validation, only structural generation transitions and health-state transitions.

Raw public or local IP addresses, gateways, interface names, DNS server/domain output, complete routes, `ip link` output, resolver status, NetworkManager connection identities, provider URLs, subscription links, credentials, generated configs, private exact network manifests, private production process-result capture files, and unredacted logs must stay outside the artifact directory. The dedicated workflow scans the artifact directory against configured secrets and network values collected from the host. Evidence is uploaded only when both the teardown assertions and pre-upload scan pass.

For issue #243, the raw initial `resolvectl status podlaz0 --no-pager` stdout/stderr capture is part of that private evidence boundary and must be deleted before artifact scanning. Public evidence may record only normalized facts such as exact-envelope convergence, clean inactive publication, clean recover dry-run/execute refresh, repeated active-status stability, disconnect convergence, and immediate reconnect success.

## Non-goals

- Not a default PR gate for unrelated changes.
- Not an automatic post-merge check.
- Not a GitHub-hosted CI replacement.
- Not permanent release evidence; keep evidence in issues, PRs, or release notes.
- Not a production fault-injection interface; daemon hooks are for dedicated self-hosted E2E only.
