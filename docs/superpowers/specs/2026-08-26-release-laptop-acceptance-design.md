# Release Laptop Acceptance Harness Design

Status: approved design, revised after review for full local release qualification.

## Goal

Provide one release-oriented acceptance harness that exercises a real Podlaz Debian release package on a maintainer laptop as deeply as practical without requiring a dedicated self-hosted E2E server.

The harness is a product-validation orchestrator over the lifecycle, package, coexistence, privacy, reconciliation, and boot-autostart contracts implemented by Issues #259-#263. It is not another networking implementation and must not repair product failures out of band.

Primary invocation:

```text
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb
```

When profile selection is ambiguous:

```text
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb --profile <profile-id>
```

If no lower release is currently installed, an explicit previous release may be supplied for the real upgrade-boundary scenario:

```text
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb \
  --previous-deb ./podlaz_<previous>_linux_<arch>.deb \
  --profile <profile-id>
```

`--previous-deb` is an explicit test input. The harness never downloads or guesses a previous release and never performs an implicit downgrade.

The long first run is followed by three explicit user-driven reboots for real boot behavior. After each reboot the user resumes the same run with:

```text
sudo ./scripts/acceptance/release-laptop.sh --resume
```

No temporary boot service, timer, cron job, login hook, or auto-resume unit is installed. The user remains in control of every reboot.

## Full qualification scope

A full `QUALIFIED_PASS` run exercises, where a scenario is genuinely applicable to the host/release boundary:

1. candidate `.deb` metadata/digest validation;
2. real lower-release active-TUN -> candidate package upgrade continuity;
3. candidate protected TUN/data-plane verification;
4. graceful daemon restart continuity;
5. unexpected daemon-main-process death and automatic systemd recovery;
6. explicit service stop -> start negative reconnect semantics;
7. same-candidate reinstall while active as an additional package-lifecycle regression;
8. functional privacy/no-direct-egress verification across candidate-protected recovery windows;
9. a single 60-minute active-session resource-soak orchestration envelope containing:
   - synthetic foreign-state coexistence and route/netlink churn;
   - controlled NetworkManager Wi-Fi reconnect/DHCP re-observation when applicable;
   - one real timed suspend/resume when supported;
   - periodic protected traffic and read-only diagnostics;
10. disconnect cleanup, ordinary-network restoration, and second-session resource comparison;
11. controlled established-session terminal failure through the production terminal path;
12. real reboot A with autostart disabled;
13. real reboot B with successful autostart enabled;
14. same-boot explicit-disconnect no-retry after successful autostart;
15. real reboot C with a controlled terminal autostart failure;
16. terminal-autostart same-boot no-retry after daemon restart;
17. restoration of the original autostart policy and all harness-created state;
18. detailed private evidence plus deterministic sanitized reports.

The harness also records resource/environment observations that can later support evidence-based system requirements. One laptop run is an observation, not universal proof of minimum/recommended requirements.

## Qualification result semantics

Scenario outcomes are distinct from the overall release-qualification result.

Scenario statuses:

```text
PASS
FAIL
SKIP_HOST_CAPABILITY
SKIP_RELEASE_CAPABILITY
SKIP_REMOTE_SESSION
SKIP_USER_REQUEST
NOT_EXERCISED
```

Overall result:

```text
QUALIFIED_PASS
PARTIAL_PASS
FAIL
```

Rules:

- any product, cleanup, restoration, or evidence-integrity `FAIL` makes the overall result `FAIL`;
- `QUALIFIED_PASS` requires the canonical 60-minute soak, the real lower-release upgrade boundary, all mandatory candidate lifecycle/privacy/terminal scenarios, and all three reboot phases;
- a user-forced skip, shortened soak, `--no-reboot-phases`, or missing real lower-release upgrade evidence makes the best possible result `PARTIAL_PASS`;
- `SKIP_HOST_CAPABILITY`, `SKIP_RELEASE_CAPABILITY`, and `SKIP_REMOTE_SESSION` are not equivalent to a user-forced skip. A genuinely inapplicable conditional scenario may be skipped without failing the product, but the report preserves missing coverage explicitly;
- a previous release cannot be required retroactively to provide a feature that did not exist in that release. In particular, privacy-envelope continuity across a legacy upgrade is gated only when the installed lower release can prove that capability; candidate-protected recovery windows are always gated;
- core candidate scenarios such as restart/crash semantics, terminal convergence, candidate privacy tripwire, and boot attempt semantics are not converted into capability skips merely because they are inconvenient to run;
- `PASS` is never used as an ambiguous overall result.

`PARTIAL_PASS` means useful validation completed without observed failure, but the run is not a complete release qualification.

## Hard safety constraints

### Release packages only

The harness tests prebuilt `.deb` packages only.

It must not:

- run `go build`, `go test`, `go install`, `make`, `goreleaser`, or any Podlaz build path;
- substitute Podlaz binaries from the repository working tree;
- build a development package from source;
- modify a package to make validation pass.

The product under test after candidate installation is `/usr/bin/podlaz`, `/usr/bin/podlazd`, `/usr/lib/podlaz/xray`, the packaged systemd unit/polkit policy, and Debian maintainer scripts from the exact candidate `.deb`.

For every supplied `.deb`, validate before mutation:

- regular file, no symlink;
- package name `podlaz`;
- native architecture equals `dpkg --print-architecture`;
- readable version;
- SHA-256 recorded;
- sibling `SHA256SUMS`, when present and containing the exact filename, must match.

The candidate remains installed after the run. The harness never downgrades back at finalization.

### No operating-system updates or dependency repair

The harness must not update or opportunistically modify the laptop software environment.

Forbidden include:

```text
apt update
apt upgrade
apt full-upgrade
apt-get update
apt-get upgrade
apt-get dist-upgrade
apt install
apt-get install
apt --fix-broken install
```

It must not install/update Go, Xray, NetworkManager, systemd, nftables, iproute2, Python, curl, or another system tool.

The only package-manager mutations are explicit `dpkg -i` operations for the supplied previous/candidate Podlaz release packages. Unsatisfied dependencies are an environment failure; the harness reports them and stops without repair.

### No generic network repair

A failed product scenario remains failed.

The success path never invokes:

```text
podlaz recover --execute
ip route flush ...
ip rule flush ...
nft flush ruleset
systemctl restart NetworkManager
systemctl restart systemd-resolved
```

Normal product operations such as `podlaz connect`, `disconnect`, `status`, `doctor --tun`, and `autostart ...` are allowed.

Final cleanup may make bounded attempts to return the laptop to a usable state, but cleanup is recorded separately and cannot change a failed scenario to PASS.

### Exact ownership for test fixtures

Synthetic network objects are owned only when the harness proved the candidate identity free immediately before creation and persisted the exact created tuple in its private fixture manifest.

Never delete by numeric/name resemblance. In particular:

- no `ip route flush table N`;
- no `ip rule flush`;
- no `nft flush ruleset`;
- no deletion of pre-existing historical Podlaz-looking resources.

Every exact delete rechecks current identity/type/composition first. Ambiguity fails closed and preserves the object for diagnosis.

### Local laptop requirement for disruptive stages

Wi-Fi reconnect and suspend/resume are not run through SSH/remote sessions. Remote detection occurs before mutation.

A genuinely unsupported local capability is `SKIP_HOST_CAPABILITY`; an explicit user suppression is `SKIP_USER_REQUEST` and prevents `QUALIFIED_PASS`.

## Original user, profiles, and user-owned state

The entry script runs with `sudo`, but normal Podlaz CLI operations run as `SUDO_USER`, not root. The harness resolves UID/GID/home/state paths from the account database rather than root's environment.

The normal test profile is an existing user profile. No real profile URI, endpoint, identity, or subscription is copied into repository fixtures or shareable reports.

Selection:

- `--profile <id>` is preferred;
- otherwise auto-select only when exactly one profile is usable for the required TUN scenario under the current release phase;
- zero/multiple valid choices fail before networking mutation.

For a real lower-release upgrade scenario, the selected profile must be usable by the installed lower release before candidate installation. After upgrade, the candidate validates the same user profile again with its own CLI before later candidate-only scenarios.

A separate temporary documentation-only profile may be created through the normal user CLI solely for terminal-autostart failure testing. It uses only reserved/example endpoint and credential values, its exact returned profile identity is kept private, and finalization deletes only that exact synthetic profile. If exact deletion/restoration authority is lost, overall result is FAIL.

## Pre-mutation baseline

### Layer A — before any package mutation

Record privately:

- installed Podlaz version/package state;
- service active/inactive state when conclusive;
- current lifecycle state when the installed CLI supports it;
- original persistent autostart state/material when present;
- current Linux `boot_id`;
- ordinary IPv4 route, DNS, TCP/HTTPS usability;
- exact host uplink identity needed for private recovery/tripwire checks;
- required already-installed host tools;
- candidate package metadata/digest;
- previous package metadata/digest if supplied.

If Podlaz is active or lifecycle state is ambiguous at this initial boundary, stop before mutation and ask the user to reach a clean disconnected state. The one exception is that the harness itself later creates the lower-release active session for the upgrade test.

Original autostart is captured before the first `dpkg -i`. If the installed lower release predates autostart and no persistent manifest exists, baseline policy is absent/disabled.

### Layer B — privacy tripwire baseline

Before connecting any VPN, prove a small direct-egress probe can reach a reviewed public test target while explicitly bound to the real physical/default uplink. The response body is discarded.

This establishes that the later same bound-uplink probe is capable of detecting direct egress. If a qualifying direct baseline cannot be established, the candidate no-direct-leak contract cannot be claimed as tested and the overall result cannot be `QUALIFIED_PASS`.

Public reports never expose the real interface name; they record only `direct_egress_baseline=available|unavailable`.

## Evidence and path ownership

Default run directory is inside the original user's state tree:

```text
$XDG_STATE_HOME/podlaz/release-acceptance/<run-id>/
```

or the documented default state path when `XDG_STATE_HOME` is unset.

Conceptual layout:

```text
<run>/
  summary.txt
  report.json
  requirements-observation.json
  private/
    raw command output
    package/process/session identities
    network baseline and fixture manifests
    soak samples
    diagnostics
```

Ownership/modes:

- the run tree and shareable/private artifacts are owned by the original user UID/GID even though orchestration runs as root;
- run/private directories are `0700`;
- private files are `0600`;
- shareable files are user-owned and never world-writable;
- the separate reboot checkpoint under `/var/lib/podlaz-release-acceptance` remains root-owned `0700`/`0600`.

`--artifact-dir` is fail-closed:

- path must resolve to an allowed user-owned location;
- no final target or existing path component may be an unexpected symlink;
- existing parents must have acceptable owner/type/permissions;
- reject `/`, `/etc`, `/run`, `/var/lib/podlaz`, another user's home/state tree, device/proc/sys filesystems, or any path that crosses an unsafe ownership boundary;
- creation uses the original user's ownership explicitly;
- resume must prove the path identity still matches the checkpoint.

### Deterministic public redaction

Shareable output never decides subjectively whether a real interface name is identifying. All host-real interfaces are mapped to structural roles, for example:

```text
wifi_uplink
wired_uplink
other_uplink
podlaz_tun
```

Only documentation-safe harness-created interface/table labels may be emitted literally.

Shareable reports must not contain real profile IDs/names, endpoints, credentials, public egress IP, local/private IPs, SSID/BSSID, physical interface names, NM UUIDs, transaction/session IDs, hostname/machine IDs, boot IDs, generated config contents, or private checkpoint values.

Private evidence may contain host-specific values and remains local. The harness never uploads evidence automatically.

## Persistent reboot checkpoint

Use a dedicated root-owned acceptance directory outside Podlaz daemon state, for example:

```text
/var/lib/podlaz-release-acceptance/
```

Checkpoint requirements:

- strict/versioned/bounded schema;
- atomic replace + restrictive permissions;
- exactly one active run;
- package digest/version/arch;
- current phase and expected `boot_id` transition;
- original user identity and private selected/synthetic profile IDs;
- original autostart restoration material;
- report path identity;
- accumulated structural outcomes;
- no synthetic networking fixture survives into reboot phases.

`--resume` accepts no new package/profile arguments and fails closed on absent, stale, corrupt, ambiguous, permission-invalid, or path-identity-mismatched checkpoint state.

## Phase 1 — real lower-release -> candidate package continuity

This is the canonical #259 package boundary and is required for `QUALIFIED_PASS`.

Preferred path: a lower released Podlaz package is already installed. Do **not** install the candidate while disconnected first.

Sequence:

1. using the installed lower release, validate/select the normal TUN profile with capabilities available in that release;
2. connect the lower release in TUN mode;
3. prove a real protected active baseline with version-appropriate status plus structural TUN/data-plane checks;
4. determine whether the lower release can prove an effective Privacy Envelope/no-direct-egress contract. If yes, start the direct-egress tripwire as a gated upgrade invariant; if not, record `legacy_upgrade_privacy=SKIP_RELEASE_CAPABILITY` and do not pretend the older release had a future feature;
5. record old daemon/core/session identities privately;
6. run `dpkg -i <exact-candidate.deb>` while TUN is active;
7. do not issue test-side `systemctl start/restart` and do not issue a second `connect` before the continuity verdict;
8. require installed package version becomes the candidate and daemon PID changes through package lifecycle;
9. require the same logical user connection returns to candidate `Connected`/verified automatically;
10. require candidate Network Session/Privacy Envelope is now effective, and from that proof onward the direct-uplink tripwire must have zero successful samples;
11. require protected DNS/TCP/TLS/HTTPS again;
12. if the lower release advertised/proved the privacy capability in step 4, require zero direct-egress tripwire successes across the entire protected upgrade/recovery window; otherwise any pre-candidate observation is diagnostic only and cannot be attributed as a candidate regression;
13. require exact recovery authority remains actionable if convergence cannot complete.

This deliberately separates two contracts:

- the legacy lower-release -> candidate boundary qualifies #259 lifecycle/authority continuity;
- privacy continuity is strictly gated for every candidate-protected recovery window, while legacy pre-candidate privacy is required only when the previous release actually implemented it.

If the candidate was already installed, a true lower-release upgrade may be prepared only through explicit `--previous-deb`, after disconnected-state and compatibility preflight. That path is deliberate test setup, never an implicit downgrade. If safe setup cannot be proven, mark `NOT_EXERCISED` and cap result at `PARTIAL_PASS`.

A same-version candidate reinstall later is additional coverage and never substitutes for this phase.

## Phase 2 — candidate protected baseline

After the upgrade boundary, require the candidate itself to validate the selected profile and expose clean current candidate semantics.

Require:

- `Connected` and TUN health `verified`;
- exact Network Session/transaction authority;
- effective Privacy Envelope composition;
- protected DNS and system resolution;
- IPv4 TCP/TLS/HTTPS;
- read-only `doctor --tun` healthy evidence;
- no cleanup-required state;
- direct-uplink tripwire fails while the protected session is established.

If Phase 1 was not exercised, install the candidate while disconnected, then explicitly connect it here; such a run is necessarily `PARTIAL_PASS` at best.

## Phase 3 — candidate lifecycle intent matrix

### 3A — graceful restart

While protected/verified:

1. run continuous direct-uplink tripwire;
2. `systemctl restart podlazd.service`;
3. prove daemon PID changed;
4. no second `connect`;
5. require same logical session returns verified;
6. require zero successful direct-egress tripwire samples throughout protected recovery;
7. recheck protected DNS/HTTPS.

This exercises restart intent (`RestartKillSignal=SIGUSR1`).

### 3B — unexpected daemon death

While protected/verified:

1. start tripwire;
2. kill only the service main daemon process with controlled `SIGKILL`/systemd main-process kill semantics, leaving systemd `Restart=on-failure` responsible for recovery;
3. prove a replacement daemon starts automatically without test-side service start;
4. no second `connect`;
5. require protected session returns verified;
6. require zero direct-egress successes during recovery;
7. verify protected data plane again.

This is intentionally different from graceful restart.

### 3C — explicit service stop -> start

While protected/verified:

1. `systemctl stop podlazd.service`;
2. require deliberate product teardown converges and ordinary host networking works;
3. direct-uplink probe must become usable only after protection/session teardown is complete;
4. `systemctl start podlazd.service`;
5. require daemon becomes ready but remains `Disconnected`;
6. prove no continuation/autoconnect from the prior ordinary session.

Then explicitly reconnect the candidate TUN session for later phases.

This exercises explicit-stop (`SIGTERM`) semantics and must not be conflated with restart/crash continuation.

### 3D — same-candidate reinstall

While the reconnected candidate session is verified, reinstall the exact same candidate `.deb` with `dpkg -i`.

Require daemon replacement through package lifecycle, no second connect, automatic return to verified, protected data plane, and zero direct-egress tripwire successes during recovery.

This is an additional regression only; Phase 1 remains the real upgrade qualification.

## Phase 4 — 60-minute active resource-soak orchestration envelope

All scheduled network-change sub-scenarios execute **inside one measured active-session envelope**. They are not run once before the soak and then repeated inside it.

Canonical total primary active interval is exactly 60 wall-clock minutes:

```text
00-05 min  warm-up (samples retained but excluded from steady-state trend)
05-20 min  steady protected workload
~20 min    synthetic foreign-state appearance + route/netlink churn
~30 min    Wi-Fi/NM reconnect when applicable
~40 min    real timed suspend/resume when supported
40-60 min  post-resume/post-churn stability
```

The five-minute warm-up is included in the 60-minute interval; canonical qualification is not 65 minutes.

`--soak-minutes N` may exist for debugging, but any value other than the canonical 60 makes the overall result at best `PARTIAL_PASS`.

### Continuous workload and sampling

Default sample interval: 60 seconds. Read-only `doctor --tun` cadence: about every 10 minutes.

Generate bounded low-volume traffic:

- protected DNS resolution;
- small HTTPS request with body discarded;
- product status;
- periodic `doctor --tun`.

Attribute resources to exact current `podlazd` and exact supervised Xray child, not same-name processes.

Collect, where available:

For daemon and Xray:

- RSS/PSS;
- CPU time and derived utilization;
- threads/tasks;
- total FDs;
- regular/pipe/anon-inode FDs;
- socket FDs;
- TCP listen/established/other;
- UDP connected/unconnected/other;
- UNIX/unclassified sockets.

For `podlazd.service` cgroup:

- `memory.current`;
- `memory.peak` when available;
- `pids.current`;
- CPU counters where available.

Record min/median/max/last, warm-up-to-end delta, simple trend/slope where meaningful, and later post-disconnect/reconnect deltas. Existing conservative non-accumulation invariants may remain pass/fail checks; new thresholds are observations until justified by repeated evidence.

### 4A — synthetic foreign-state coexistence/churn at ~20 min

Create product-neutral synthetic state using only verified-free documentation-safe identities, for example one unrelated TUN, point address, exact route in a private numeric table, selective policy rule(s), unrelated nftables table, and optional dummy resolved link.

Rules:

- inventory/read-only allocation first;
- live collision check immediately before every mutation;
- no fixture default route carrying ordinary traffic;
- exact fixture manifest persisted privately;
- no foreign/global cleanup.

After appearance/churn require Podlaz remains or returns verified automatically. Perform one exact fixture route replacement/link transition to trigger fresh observation.

Keep the fixture through the later disconnect/reconnect coexistence proof. Structural fixture state must remain unchanged except for harness-declared churn.

### 4B — Wi-Fi / DHCP reconnect at ~30 min

Only on a local NetworkManager-managed Wi-Fi uplink.

Privately record exact NM connection/uplink/address/gateway identity, then perform controlled down/up of the exact original connection. Never restart NetworkManager or resolved.

After reconnect require:

- original NM connection restored;
- ordinary default route available;
- Podlaz self-converges to verified;
- protected DNS/HTTPS;
- report whether DHCP/address/gateway identity actually changed.

Returning the same lease is still a Wi-Fi reconnect PASS with `dhcp_identity_changed=false`; do not fabricate renewal.

Unsupported Wi-Fi is `SKIP_HOST_CAPABILITY`; `--skip-wifi-churn` is `SKIP_USER_REQUEST` and prevents `QUALIFIED_PASS`.

### 4C — timed suspend/resume at ~40 min

Before suspend prove verified state and flush private evidence. Use a bounded RTC/timed suspend for roughly 60-120 seconds only when local host capability supports it.

After resume:

- prove a meaningful suspend interval occurred;
- allow normal NM/uplink convergence without restarting services;
- require Podlaz returns verified automatically;
- require protected DNS/HTTPS;
- collect immediate/post-settle resource samples;
- record natural daemon/Xray generation changes.

Unsupported suspend is a capability skip. `--skip-suspend` is user-forced partial coverage.

### Privacy tripwire during the soak

The direct-uplink probe remains periodically/continuously active around every recovery-prone event. While the candidate protected session is logically established/reconciling, **any successful direct-uplink sample is an immediate privacy FAIL**.

The tripwire is functional evidence. Exact Privacy Envelope/firewall composition is collected as corroboration, not treated as a substitute for leak detection.

## Phase 5 — disconnect, resource cleanup, and coexistence reconnect

After the 60-minute envelope:

1. normal `podlaz disconnect`;
2. require clean `Disconnected`/inactive;
3. require Network Session/Privacy Envelope cleanup convergence;
4. direct-uplink probe now succeeds again;
5. ordinary DNS/TCP/HTTPS works;
6. synthetic foreign fixture remains unchanged;
7. collect post-disconnect daemon/cgroup boundary after bounded settle;
8. compare against warmed inactive baseline.

Reconnect the candidate while the foreign fixture already exists.

Require:

- collision-safe new allocation;
- verified protected data plane;
- foreign fixture preserved;
- 5-10 minute second-session resource observation;
- no unexpected resource accumulation relative to first session.

Disconnect again, prove clean boundary, then remove only exact fixture objects in reverse dependency order. No synthetic networking fixture may survive into terminal/reboot phases.

## Phase 6 — controlled established-session terminal failure

This phase proves #261/#262 terminal semantics rather than using explicit disconnect as a proxy.

Use only a narrow, already-existing release-built E2E/fault-injection seam that drives the normal production reconciliation/terminal lifecycle. Do not add a second terminal implementation to the harness.

If the candidate release does not contain the required reviewed gated seam, this mandatory scenario is `NOT_EXERCISED` and the run cannot be `QUALIFIED_PASS`.

Safe orchestration:

1. while disconnected, prove the exact temporary acceptance systemd drop-in path is absent/free;
2. install one exact root-owned drop-in enabling only the required terminal/reconciliation/privacy-pause hooks and a private `/run` marker directory;
3. `systemctl daemon-reload` and restart daemon while disconnected;
4. connect the normal profile and require verified protected state;
5. start the direct-egress tripwire;
6. trigger the controlled unrecoverable terminal evidence;
7. at the supported post-data-plane-clean/pre-envelope-remove pause, require direct egress is still blocked and effective Privacy Envelope evidence remains;
8. release the pause and require normal production terminal teardown completes;
9. require `Disconnected`, ordinary DNS/TCP/HTTPS, and direct-uplink egress restored;
10. require no automatic reconnect after a same-boot daemon restart;
11. remove only the exact acceptance drop-in/marker state, `daemon-reload`, and leave daemon cleanly disconnected.

Any leak, incomplete cleanup, false clean status, retry, or drop-in restoration failure is FAIL.

## Phase 7 — reboot A: autostart disabled

Before touching persistent boot policy:

- prove all synthetic networking/fault-injection fixtures are gone;
- prove original autostart restoration material is usable;
- create/persist strict root-owned checkpoint;
- disable autostart through normal CLI;
- confirm disabled;
- record current boot identity privately;
- checkpoint `awaiting-autostart-off-reboot`;
- instruct the user to reboot manually.

The harness never invokes reboot itself.

### Resume A

Require a new `boot_id`, same candidate package, normally started daemon, conclusively `Disconnected`, no fresh VPN attempt, ordinary network, and autostart disabled.

Then enable autostart for the normal test profile, confirm `Enabled for next boot`, checkpoint `awaiting-autostart-on-reboot`, and instruct reboot B.

## Phase 8 — reboot B: successful autostart and same-boot no retry

On resume B require another new `boot_id`, same candidate, bounded network readiness, exactly one logical automatic attempt, `Connected`/verified, and protected data plane.

Restart daemon once and prove:

- daemon PID changes;
- same Network Session resumes;
- no second boot attempt is admitted;
- direct-egress tripwire has zero successes through protected recovery.

Then explicit `podlaz disconnect`, wait clean inactive, restart daemon, and prove it stays disconnected in the same boot.

Next, create the exact temporary documentation-only terminal-autostart profile through normal user-owned profile CLI, set autostart to that profile for the **next boot**, record its exact private identity for cleanup, checkpoint `awaiting-terminal-autostart-reboot`, and instruct reboot C.

## Phase 9 — reboot C: terminal autostart failure and no retry

On resume C require another new `boot_id` and same candidate package.

The synthetic autostart request must enter the canonical boot attempt/connect lifecycle and converge to a conclusively terminal outcome, not an endless retry.

Require:

- attempt reaches terminal only after cleanup convergence;
- product ends `Disconnected` with the stable high-level terminal connect reason;
- ordinary network/direct-uplink egress is usable;
- no Network Session/Privacy Envelope cleanup remains pending;
- same-boot `systemctl restart podlazd.service` does not start another automatic connect and does not replace the consumed terminal attempt authority.

Then restore original autostart policy through normal CLI and delete only the exact synthetic terminal profile created by the harness.

## Final restoration

Finalization must leave the candidate release installed but otherwise restore the captured non-package baseline where the harness changed it.

Required:

1. original autostart policy restored through normal product API;
2. synthetic terminal profile removed exactly;
3. no acceptance systemd drop-in/marker remains;
4. no synthetic TUN/route/rule/DNS/nft fixture remains;
5. Wi-Fi/NM connection restored if the harness ever left it down;
6. Podlaz cleanly disconnected;
7. daemon service active/inactive state restored when original state was conclusively known and safe to restore;
8. ordinary IPv4 route, DNS, TCP/HTTPS usable;
9. direct-uplink baseline probe usable;
10. checkpoint removed;
11. run tree ownership/modes revalidated;
12. final sanitized reports written.

A cleanup/restoration failure is overall `FAIL` even if product scenarios passed.

## Finalizer semantics

Register cleanup authority incrementally only after each mutation succeeds. Finalize in reverse dependency order while preserving lifecycle semantics:

```text
stop acceptance workload/tripwire
attempt normal Podlaz disconnect if acceptance session is active
wait bounded product convergence
restore exact NM connection if left down
remove exact synthetic network objects
remove exact acceptance fault-injection drop-in/markers
restore original autostart when phase permits
remove exact temporary synthetic profile
verify ordinary networking and direct-uplink baseline
write failure/cleanup evidence
```

Cleanup never changes a FAIL to PASS and never uses broad host repair.

## Resource and system-requirement observations

Sanitized environment evidence may include:

- distribution/version;
- kernel version family;
- architecture;
- logical CPU count and non-identifying CPU class;
- total RAM/swap;
- systemd/NetworkManager/nft/iproute2 versions;
- candidate Podlaz version;
- availability of `/dev/net/tun`, unified cgroup v2, systemd-resolved, Wi-Fi, RTC wake, suspend;
- median/peak/last resource metrics and deltas from the canonical run.

Do not update the system when versions differ from preferred environments.

`requirements-observation.json` explicitly calls every measured value an observation. Minimum/recommended requirements are derived later from multiple successful runs plus engineering margin, not asserted from one laptop.

## Reports

### `summary.txt`

Sanitized/shareable example:

```text
Release package: podlaz <version> (<arch>)
Qualification: QUALIFIED_PASS|PARTIAL_PASS|FAIL

lower_release_upgrade_continuity          PASS|NOT_EXERCISED
legacy_upgrade_privacy                    PASS|SKIP_RELEASE_CAPABILITY
graceful_restart_continuity               PASS
unexpected_daemon_death_recovery          PASS
explicit_service_stop_start               PASS
same_candidate_reinstall_continuity       PASS
candidate_privacy_no_direct_leak          PASS
resource_soak_60m                         PASS
foreign_state_churn                       PASS
wifi_reconnect                            PASS|SKIP_HOST_CAPABILITY|SKIP_USER_REQUEST
suspend_resume                            PASS|SKIP_HOST_CAPABILITY|SKIP_USER_REQUEST
post_disconnect_cleanup                   PASS
coexistence_reconnect                     PASS
reconnect_resource_nonaccumulation        PASS
runtime_terminal_convergence              PASS
runtime_terminal_no_retry                 PASS
real_boot_autostart_off                   PASS
real_boot_autostart_on                    PASS
successful_autostart_same_boot_no_retry   PASS
terminal_autostart_failure                PASS
terminal_autostart_same_boot_no_retry     PASS
original_autostart_policy_restored        PASS
final_host_restoration                    PASS

Resource observations:
  measured active interval: <duration>
  samples: <count>
  daemon RSS median/peak: <values>
  Xray RSS median/peak: <values>
  cgroup memory peak: <value>
  daemon/Xray FD start/end deltas: <values>
  post-disconnect/reconnect deltas: <values>
```

`report.json` contains versioned structural scenario outcomes and numeric summaries.

`requirements-observation.json` contains sanitized environment/resource observations suitable for later aggregation.

No report automatically uploads anywhere.

## Harness CLI

Canonical qualification:

```text
sudo ./scripts/acceptance/release-laptop.sh <candidate.deb> [--profile <profile-id>]
sudo ./scripts/acceptance/release-laptop.sh --resume
```

Explicit previous-release setup when needed:

```text
--previous-deb <release.deb>
```

Useful developer/debug overrides may include:

```text
--artifact-dir <safe-user-owned-path>
--soak-minutes <N>
--skip-wifi-churn
--skip-suspend
--no-reboot-phases
```

Qualification effects are mandatory:

- non-60 `--soak-minutes` => at best `PARTIAL_PASS`;
- user `--skip-*` => at best `PARTIAL_PASS`;
- `--no-reboot-phases` => at best `PARTIAL_PASS`;
- no true lower-release active upgrade => at best `PARTIAL_PASS`;
- genuine host/release capability skip remains distinct and is reported structurally.

These flags are harness controls, not Podlaz product settings.

## Implementation boundaries

Prefer reuse over duplication:

- safe logging/redaction conventions from `scripts/e2e/lib/e2e.sh`;
- existing `tun_soak_*` exact procfs/cgroup attribution and aggregation;
- installed product status/doctor/lifecycle APIs;
- current structural redaction scanner;
- existing release-built E2E terminal/reconciliation hooks where explicitly required.

Do not reuse helpers that build dev packages, install dependencies, assume a disposable trusted host, or perform broad cleanup.

Keep narrow components for:

1. package/provenance and true-upgrade orchestration;
2. user/profile/autostart state;
3. direct-egress privacy tripwire;
4. lifecycle restart/crash/stop-start scenarios;
5. synthetic fixture allocation/exact cleanup;
6. Wi-Fi/suspend events;
7. soak scheduling/resource sampling;
8. controlled terminal hooks;
9. reboot checkpointing;
10. report redaction/qualification evaluation.

The user-facing entry point remains one shell script.

## Deterministic TDD contract

### Package/qualification

Prove:

- candidate/previous `.deb` validation;
- no source-build command;
- no OS update/install/fix-broken command;
- true lower-release active-TUN -> candidate ordering occurs before candidate disconnected install;
- no manual service repair/second connect in upgrade continuity;
- same-version reinstall cannot satisfy `lower_release_upgrade_continuity`;
- lower-release privacy is capability-gated and cannot be misattributed as a candidate regression when the previous release predates the feature;
- shortened/skip/no-reboot runs cannot yield `QUALIFIED_PASS`;
- capability skip and user skip remain distinct.

### Lifecycle/privacy

Prove orchestration contains separate graceful restart, main-process SIGKILL recovery, and explicit stop/start scenarios.

Tripwire tests prove:

- baseline direct-bound probe must succeed before candidate protected qualification;
- candidate-protected/recovery samples must all fail;
- any successful candidate-protected sample is privacy FAIL;
- post-terminal/disconnect direct probe must succeed;
- Privacy Envelope metadata alone cannot satisfy privacy acceptance.

### Soak scheduling

Use fake clock/event scheduler tests to prove exactly one synthetic churn event, one Wi-Fi event when applicable, and one suspend event occur inside the canonical 60-minute envelope. Warm-up occupies minutes 0-5 of that same hour and is excluded from steady-state trend calculations.

### Terminal scenarios

Prove runtime terminal acceptance uses only the gated production terminal seam, validates barrier effectiveness before removal, verifies ordinary network after convergence, and checks same-boot no retry.

Prove reboot checkpoint state includes terminal-autostart phase and same-boot restart cannot admit a second attempt.

### User/path/privacy boundary

Prove:

- Podlaz CLI runs as original user;
- report tree is user-owned;
- root checkpoint remains root-owned;
- unsafe/symlinked/cross-owner artifact paths fail before mutation;
- every real host interface becomes a structural role in shareable output;
- private identifiers never enter public reports;
- temporary synthetic profile is exact-created/exact-deleted.

### Exact fixture ownership

Prove occupied identities are preserved, exact tuples only are removed, broad flushes are absent, and ambiguous drift fails closed.

### Resource attribution

Prove exact daemon/Xray identity, 60-minute default configuration, cleanup/reconnect comparisons, and observation-vs-requirement wording.

## Validation before merge

Run relevant repository checks:

```text
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
shellcheck <new/changed shell files>
```

Hosted CI cannot execute real Wi-Fi, suspend, root networking, package-upgrade-from-installed-release, and real reboot phases. Deterministic tests therefore enforce safety/orchestration without root.

A full local run against actual release packages is evidence produced by the harness, not a prerequisite for merging the harness implementation itself.

## Success criteria

Implementation is complete when:

- one release-oriented command can qualify the candidate without building Podlaz or updating the OS;
- real lower-release active-TUN -> candidate upgrade is the canonical package continuity test;
- restart, unexpected daemon death, explicit service stop/start, and same-version reinstall are separate lifecycle scenarios;
- functional direct-egress evidence proves candidate privacy across recovery, not merely barrier authority;
- legacy upgrade privacy is required only when the previous release actually supports/proves that feature;
- one exact 60-minute soak envelope schedules coexistence churn, Wi-Fi reconnect, suspend/resume, workload, and resource sampling once;
- controlled runtime terminal failure converges through the real production terminal path to ordinary networking with no retry;
- three manual reboot/resume stages prove autostart-off, successful autostart/no-retry, and terminal-autostart/no-retry semantics;
- harness-created state is removed exactly and original autostart is restored;
- shareable reports are deterministic and host-private identifiers are role-mapped/redacted;
- `QUALIFIED_PASS` cannot be emitted for shortened or user-skipped coverage;
- resource evidence is sufficient for later system-requirement analysis;
- failures are never hidden by generic repair.