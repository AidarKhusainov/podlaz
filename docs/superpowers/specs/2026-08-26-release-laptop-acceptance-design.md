# Release Laptop Acceptance Harness Design

Status: approved design, revised after three review passes for full local release qualification.

> Current intended qualification path: when a lower released Podlaz version is already installed, preserve that installation until the harness itself establishes the lower-release active TUN session and upgrades it to the supplied candidate. Do not pre-install the candidate in a disconnected state and erase the real upgrade boundary.

## Goal

Provide one release-oriented acceptance harness that exercises a real Podlaz Debian release package on a maintainer laptop as deeply as practical without requiring a dedicated self-hosted E2E server.

The harness is a product-validation orchestrator over the lifecycle, package, coexistence, privacy, reconciliation, resource-lifecycle, durable-recovery, and boot-autostart contracts implemented by Issues #259-#263. It is not another networking implementation and must not repair product failures out of band.

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

A persistent multi-reboot run can be safely abandoned from any supported checkpoint phase with:

```text
sudo ./scripts/acceptance/release-laptop.sh --abort
```

No temporary boot service, timer, cron job, login hook, or auto-resume unit is installed. The user remains in control of every reboot.

## Full qualification scope

A full `QUALIFIED_PASS` run exercises, where a scenario is genuinely applicable to the host/release boundary:

1. candidate `.deb` metadata, digest, architecture, and Debian version-order validation;
2. real lower-release active-TUN -> candidate package upgrade continuity;
3. candidate protected TUN/data-plane verification;
4. graceful daemon restart continuity;
5. unexpected daemon-main-process death and automatic systemd recovery;
6. a separate #259 durable-rollback interruption after exact transaction authority has entered `rolling_back` and before host cleanup completes;
7. explicit service stop -> start negative reconnect semantics;
8. same-candidate reinstall while active as an additional package-lifecycle regression;
9. functional plus deterministic local privacy/no-direct-egress verification across candidate-protected recovery windows;
10. a warmed candidate-inactive resource baseline;
11. a distinct pre-connect #260 collision scenario that occupies historical preferred resources before candidate allocation;
12. one 60-minute active-session resource-soak envelope containing:
    - the preserved pre-existing #260 collision fixture;
    - a separate #262 foreign-state appearance/churn event while already connected;
    - controlled NetworkManager Wi-Fi reconnect/DHCP re-observation when applicable;
    - one real timed suspend/resume when supported;
    - periodic protected traffic, privacy checks, diagnostics, and resource sampling;
13. disconnect cleanup, ordinary-network restoration, coexistence preservation, and second-session resource comparison;
14. controlled established-session terminal failure through the production terminal path;
15. real reboot A with autostart disabled;
16. real reboot B with successful autostart enabled;
17. same-boot explicit-disconnect no-retry after successful autostart;
18. real reboot C with a controlled terminal autostart failure;
19. terminal-autostart same-boot no-retry after daemon restart;
20. restoration of the original autostart policy and all harness-created state;
21. detailed private evidence plus deterministic sanitized reports.

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
INCONCLUSIVE
```

Overall result:

```text
QUALIFIED_PASS
PARTIAL_PASS
FAIL
```

Rules:

- any product, cleanup, restoration, privacy-safety, durable-authority, or evidence-integrity `FAIL` makes the overall result `FAIL`;
- `QUALIFIED_PASS` requires the canonical 60-minute soak, a real lower-release upgrade boundary, all mandatory candidate lifecycle/privacy/terminal scenarios, the #259 durable-rollback interruption, the #260 pre-connect collision proof, and all three reboot phases;
- a user-forced skip, shortened soak, `--no-reboot-phases`, or missing real lower-release upgrade evidence makes the best possible result `PARTIAL_PASS`;
- `SKIP_HOST_CAPABILITY`, `SKIP_RELEASE_CAPABILITY`, and `SKIP_REMOTE_SESSION` are not equivalent to a user-forced skip. A genuinely inapplicable conditional scenario may be skipped without failing the product, but the report preserves missing coverage explicitly;
- a previous release cannot be required retroactively to provide a feature that did not exist in that release. In particular, Privacy Envelope continuity across a legacy upgrade is gated only when the installed lower release can prove that capability; candidate-protected recovery windows are always gated;
- core candidate scenarios such as restart/crash semantics, durable rollback authority, deterministic privacy proof, terminal convergence, and boot-attempt semantics are not converted into capability skips merely because they are inconvenient to run;
- `INCONCLUSIVE` is never positive proof. For a mandatory safety scenario such as candidate no-direct-leak or durable rollback ownership, inconclusive evidence is a scenario `FAIL` because the claimed property was not proved;
- `PASS` is never used as an ambiguous overall result.

`PARTIAL_PASS` means useful validation completed without observed product failure, but the run is not a complete release qualification.

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
- readable Debian package version;
- SHA-256 recorded;
- sibling `SHA256SUMS`, when present and containing the exact filename, must match.

The candidate remains installed after the run. The harness never downgrades back at finalization.

### Debian lower-release ordering is authoritative

The real upgrade scenario is eligible only when the previous installed/supplied package is strictly lower than the candidate under Debian version semantics.

Before any lower-release setup or TUN mutation, require the equivalent of:

```text
dpkg --compare-versions <previous-version> lt <candidate-version>
```

Rules:

- `previous < candidate` => eligible for `lower_release_upgrade_continuity`;
- `previous == candidate` => same-version reinstall only; it cannot satisfy the lower-release upgrade requirement;
- `previous > candidate` => this is a downgrade boundary and must not be run as release qualification;
- string/lexicographic comparison is forbidden;
- the exact compared versions and structural result are private/report-safe evidence; no version-order assumption is inferred from filenames alone.

For the intended current laptop path, installed `0.2.29` and candidate `0.2.30` naturally satisfy this gate.

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

Final cleanup/abort may make bounded exact attempts to return the laptop to a usable state, but cleanup is recorded separately and cannot change a failed scenario to PASS.

### Exact ownership for test fixtures

Synthetic network objects are owned only when the harness proved the candidate identity free immediately before creation and persisted the exact planned/acquired tuple in its private mutation ledger.

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

A separate temporary documentation-only profile may be created through the normal user CLI solely for terminal-autostart failure testing. It uses only reserved/example endpoint and credential values and must follow the crash-safe dynamic-identity acquisition protocol defined below. Its exact returned profile identity is kept private, and finalization/abort deletes only that exact synthetic profile. If exact deletion/restoration authority is lost, overall result is FAIL.

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
- previous package metadata/digest if supplied;
- Debian `previous < candidate` comparison result when a previous release exists.

If Podlaz is active or lifecycle state is ambiguous at this initial boundary, stop before mutation and ask the user to reach a clean disconnected state. The one exception is that the harness itself later creates the lower-release active session for the upgrade test.

Original autostart is captured before the first `dpkg -i`. If the installed lower release predates autostart and no persistent manifest exists, baseline policy is absent/disabled.

### Layer B — direct-egress tripwire baseline

Before connecting any VPN, prove one or more reviewed small direct-egress targets are reachable while explicitly bound to the real physical/default uplink. Response bodies are discarded.

This establishes that later bound-uplink probes are capable of detecting direct egress. Public reports never expose the real interface name or returned public IP.

Baseline target reachability is diagnostic prerequisite evidence, not sufficient proof for later blocked-state conclusions because an external target may become unavailable independently.

## Deterministic candidate privacy proof

Candidate no-direct-leak qualification is based on two independent evidence classes:

1. **functional direct-uplink tripwire**, and
2. **deterministic local protection evidence** from the exact candidate-owned Privacy Envelope/packet path.

The functional rule is strict:

```text
bound direct probe SUCCESS while candidate session is logically protected/recovering
=> immediate privacy FAIL
```

A failed/timeout external probe is **not** by itself PASS evidence.

For a failed direct probe to contribute positive no-leak evidence, the same protected/recovery interval must also have locally conclusive proof that the exact persisted Privacy Envelope authority is present and its effective firewall/packet-path composition still blocks ordinary direct traffic while allowing only the intended protected/bootstrap exceptions.

The local proof must reuse the existing exact ownership/composition model already implemented by the candidate. It must not infer ownership from a table name alone and must not create a second firewall policy in the harness.

At minimum, private verification binds:

- the current Network Session identity/epoch;
- its exact persisted Privacy Envelope ownership description;
- the exact live nftables/firewall composition corresponding to that authority;
- the effective ordinary-uplink blocked path versus explicit narrow exceptions;
- the current protected/recovery lifecycle interval.

When supported by existing counters/evidence, packet counter changes may be recorded as additional corroboration, but implementation must not change production firewall rules merely to make the test observable.

Verdicts for each protected recovery window:

```text
direct probe succeeds
=> FAIL

direct probe fails + exact local protection proof succeeds
=> PASS evidence for that interval

direct probe fails + exact local protection proof unavailable/ambiguous
=> INCONCLUSIVE => mandatory privacy scenario FAIL
```

Multiple independent pre-resolved targets may improve diagnostics but cannot replace deterministic local proof.

After deliberate successful teardown/terminal convergence, the same direct-uplink path must become usable again, proving the tripwire is not permanently broken.

Legacy previous-release privacy remains capability-gated; candidate-protected windows always use the strict combined proof above.

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
- resume/abort must prove path identity still matches the checkpoint.

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

## Persistent reboot checkpoint and resumability

Use a dedicated root-owned acceptance directory outside Podlaz daemon state, for example:

```text
/var/lib/podlaz-release-acceptance/
```

Checkpoint requirements:

- strict/versioned/bounded schema;
- durable atomic replacement with restrictive permissions;
- exactly one active run;
- package digest/version/arch;
- current phase and expected `boot_id` transition;
- original user identity and private selected/synthetic profile IDs;
- original autostart restoration material;
- report path identity;
- exact acceptance drop-in/marker ownership when planned/acquired;
- exact synthetic fixture ownership when planned/acquired;
- accumulated structural outcomes;
- a crash-atomic mutation ledger sufficient to run `--resume` or `--abort` without guessing.

A durable checkpoint commit means: write a bounded temporary file with the final mode/owner, flush file contents, atomically rename it over the checkpoint, and flush the containing directory so a power loss cannot silently reorder a persistent mutation ahead of its write-ahead authority.

No synthetic network fixture is intentionally carried across reboot phases. Checkpoint fields for such fixtures exist so resume/abort can distinguish `planned`, `acquiring`, `acquired`, `releasing`, `released`, `already-absent`, and unexpected residual/ambiguous states.

`--resume` accepts no new package/profile arguments and fails closed on absent, stale, corrupt, ambiguous, permission-invalid, or path-identity-mismatched checkpoint state.

## Crash-atomic mutation acquisition protocol

Any harness mutation that can survive shell death, power loss, suspend failure, or reboot must use write-ahead authority. The pattern is not `mutate -> remember`; it is:

```text
persist exact planned identity + baseline + state=acquiring
→ durable checkpoint commit
→ perform one mutation
→ read-only verify exact resulting identity/state
→ persist state=acquired
→ durable checkpoint commit
```

This applies at least to:

- acceptance systemd drop-ins and marker directories/files;
- synthetic TUN/address/route/rule/DNS/nftables fixtures;
- NetworkManager down/up intent when a connection could be left down after shell death;
- persistent autostart policy changes;
- any temporary acceptance-owned file that changes daemon behavior across replacement;
- the synthetic terminal-autostart profile, using the dynamic-identity protocol below.

When resuming/aborting an `acquiring` entry with a deterministic planned identity, reconcile read-only before taking authority:

```text
live == recorded baseline
=> mutation did not take effect; mark released/not-acquired

live == exact planned result
=> mutation took effect; promote to acquired and continue cleanup/resume

anything else
=> ambiguous; fail closed and retain checkpoint/evidence
```

Removal/restoration is symmetric:

```text
persist state=releasing + exact expected live identity + exact restoration target
→ durable checkpoint commit
→ perform one exact release/restore mutation
→ verify exact restored/absent result
→ persist state=released
→ durable checkpoint commit
```

No cleanup authority is inferred from names, ranges, product labels, or remembered shell variables that were not already durably recorded.

### Dynamic identity acquisition for the synthetic profile

The temporary terminal-autostart profile has a product-returned ID, so it cannot rely on `create -> persist returned ID`.

Before profile creation:

1. acquire an exclusive harness lock for the original user's Podlaz profile-state mutation boundary;
2. read and persist a bounded baseline inventory/fingerprint of existing profiles without secrets;
3. choose a run-unique documentation-safe synthetic profile marker/name and canonical documentation-only field fingerprint that is proven absent;
4. persist `state=acquiring`, the unique marker/fingerprint, and the baseline inventory digest;
5. durable-commit the checkpoint.

Then invoke the normal user-owned profile API exactly once. After creation:

- verify the returned ID resolves to exactly the planned marker/fingerprint;
- prove the baseline profiles are unchanged;
- persist the exact returned profile ID and `state=acquired` durably;
- release the exclusive lock.

If the shell dies after product creation but before the ID is committed, resume/abort reacquires the same user-state lock and computes an exact baseline diff using the planned unique marker/fingerprint:

- zero matching additions => creation did not complete;
- exactly one matching addition and unchanged baseline => adopt that exact returned/stored ID as acquired authority;
- more than one match, baseline drift that prevents exact attribution, or any structural mismatch => fail closed and retain checkpoint/evidence.

The protocol never deletes a profile merely because its name resembles the test marker.

## Checkpoint-driven `--abort`

`--abort` is the canonical safe-abandon operation for every supported persistent/resumable phase. It is not an alias for broad recovery and does not claim that failed product scenarios passed.

`--abort` loads the exact checkpoint/mutation ledger and first reconciles any `planned/acquiring/releasing` entries using the crash-atomic rules above. It performs only operations for which the run has durable exact authority.

Bounded abort order:

1. stop acceptance workloads/tripwires owned by the current run;
2. if an acceptance-started Podlaz session is still active and the product API is reachable, request normal `podlaz disconnect` and wait boundedly for product convergence;
3. restore the exact original NetworkManager connection only if the ledger proves the harness acquired the exact down/up mutation;
4. release only exact synthetic network fixtures proven acquired by this run;
5. release only the exact acceptance systemd drop-in and private hook markers proven acquired by this run, then `systemctl daemon-reload` when required;
6. restore the original autostart policy through normal Podlaz CLI using checkpointed restoration material and the releasing protocol;
7. delete only the exact temporary synthetic terminal profile proven acquired by the dynamic-identity protocol;
8. verify ordinary route/DNS/TCP/HTTPS and direct-uplink baseline usability;
9. revalidate report tree/checkpoint ownership and write an abort/cleanup result;
10. remove the persistent checkpoint only after all required restoration checks pass.

If any restoration object is missing-but-ambiguous, identity has drifted, product cleanup is incomplete, original autostart cannot be reconstructed exactly, an acquiring/releasing entry cannot be reconciled, or ordinary networking cannot be verified, abort is `ABORT_CLEANUP_FAILED`:

- the checkpoint is retained;
- private evidence is retained;
- no unknown object is deleted;
- the command exits non-zero with the exact failed restoration category;
- the user is not told that baseline was restored.

A successful abort may finish as `ABORTED_CLEAN` but never as `QUALIFIED_PASS`.

The normal in-process shell finalizer remains useful for failures before reboot, but it is not considered sufficient protection for persistent multi-reboot phases.

## Phase 1 — real lower-release -> candidate package continuity

This is the canonical #259 package boundary and is required for `QUALIFIED_PASS`.

Preferred path: a lower released Podlaz package is already installed. Do **not** install the candidate while disconnected first.

Precondition: Debian version ordering has already proved `lower < candidate`.

Sequence:

1. using the installed lower release, validate/select the normal TUN profile with capabilities available in that release;
2. connect the lower release in TUN mode;
3. prove a real protected active baseline with version-appropriate status plus structural TUN/data-plane checks;
4. determine whether the lower release can prove an effective Privacy Envelope/no-direct-egress contract. If yes, run the combined privacy proof as a gated upgrade invariant; if not, record `legacy_upgrade_privacy=SKIP_RELEASE_CAPABILITY` and do not pretend the older release had a future feature;
5. record old daemon/core/session identities privately;
6. run `dpkg -i <exact-candidate.deb>` while TUN is active;
7. do not issue test-side `systemctl start/restart` and do not issue a second `connect` before the continuity verdict;
8. require installed package version becomes the candidate and daemon PID changes through package lifecycle;
9. require the same logical user connection returns to candidate `Connected`/verified automatically;
10. require candidate Network Session/Privacy Envelope is effective; from that proof onward every protected/recovery interval uses the strict combined privacy proof;
11. require protected DNS/TCP/TLS/HTTPS again;
12. if the lower release advertised/proved privacy capability in step 4, require no direct leak across the full supported upgrade window; otherwise pre-candidate observations are diagnostic only and cannot be attributed as a candidate regression;
13. require exact recovery authority remains actionable if convergence cannot complete.

If the candidate was already installed, a true lower-release upgrade may be prepared only through explicit `--previous-deb`, after disconnected-state, Debian version-order, and compatibility preflight. That path is deliberate test setup, never an implicit downgrade. If safe setup cannot be proven, mark `NOT_EXERCISED` and cap result at `PARTIAL_PASS`.

A same-version candidate reinstall later is additional coverage and never substitutes for this phase.

## Phase 2 — candidate protected baseline and lifecycle intent matrix

After the upgrade boundary, require the candidate itself to validate the selected profile and expose clean current candidate semantics.

Require:

- `Connected` and TUN health `verified`;
- exact Network Session/transaction authority;
- effective Privacy Envelope composition;
- protected DNS and system resolution;
- IPv4 TCP/TLS/HTTPS;
- read-only `doctor --tun` healthy evidence;
- no cleanup-required state;
- combined candidate privacy proof passes while the protected session is established.

If Phase 1 was not exercised, install the candidate while disconnected, then explicitly connect it here; such a run is necessarily `PARTIAL_PASS` at best.

### 2A — graceful restart

While protected/verified:

1. start the combined privacy observation window;
2. `systemctl restart podlazd.service`;
3. prove daemon PID changed;
4. no second `connect`;
5. require same logical session returns verified;
6. require strict combined no-direct-leak proof across protected recovery;
7. recheck protected DNS/HTTPS.

This exercises restart intent (`RestartKillSignal=SIGUSR1`).

### 2B — unexpected daemon death

While protected/verified:

1. start combined privacy observation;
2. kill only the service main daemon process with controlled `SIGKILL`/systemd main-process kill semantics, leaving systemd `Restart=on-failure` responsible for recovery;
3. prove a replacement daemon starts automatically without test-side service start;
4. no second `connect`;
5. require protected session returns verified;
6. require strict combined no-direct-leak proof across recovery;
7. verify protected data plane again.

This proves ordinary crash continuation only. It does not substitute for the durable rollback-boundary test below.

### 2C — #259 durable rolling-back interruption

This is a separate mandatory acceptance scenario for the failure class #259 was created to prevent.

Use only the existing release-built gated rollback pause seam (`PODLAZ_E2E_TUN_ROLLBACK_PAUSE` and its reviewed companion settings). Do not implement a second rollback mechanism in the harness. If the candidate package does not contain the required gated seam, record `NOT_EXERCISED` and do not emit `QUALIFIED_PASS`.

Safe sequence:

1. begin from a verified candidate session, then normal-disconnect if necessary so hook installation itself occurs from a clean inactive boundary;
2. use the crash-atomic write-ahead protocol to acquire one exact root-owned systemd drop-in enabling only the rollback-pause hook and one exact private marker directory;
3. `systemctl daemon-reload` and start/restart the daemon from the inactive boundary so the candidate process inherits the gated hook;
4. connect the normal profile and require verified protected state plus combined privacy proof;
5. write-ahead and arm the exact rollback-pause marker;
6. record the current daemon PID and initiate the normal `systemctl restart podlazd.service` asynchronously so restart intent drives ordinary teardown/rollback;
7. wait for the hook's exact ready marker;
8. before killing anything, read the transaction authority and require exactly relevant Podlaz-owned transaction state is durably `rolling_back` with non-empty exact rollback ownership for the surviving host/core resources;
9. persist private structural evidence that `rolling_back` authority was observed before process death;
10. `SIGKILL` only the old daemon main process while it is paused inside that durable rollback boundary;
11. do **not** issue `systemctl start`, `systemctl reset-failed`, a second `connect`, `recover --execute`, `ip`, `nft`, or resolver repair before the scenario verdict;
12. require systemd/product lifecycle itself starts the replacement daemon and startup recovery consumes/continues the exact durable authority rather than guessing from reserved resources;
13. require the same logical connection automatically converges back to `Connected`/verified when continuation is possible, with no ownership loss or anonymous residue;
14. require strict combined privacy proof throughout every candidate-protected recovery interval;
15. require no cleanup-required state remains after successful convergence;
16. release the exact pause marker/drop-in with the crash-atomic releasing protocol, run `systemctl daemon-reload`, and verify no acceptance hook state remains.

If the original `systemctl restart` client returns non-zero because the deliberate SIGKILL interrupted its stop job, that return code is diagnostic only. Product success is decided by automatic service/session convergence. The harness must not repair the service to manufacture a PASS.

A lost/ambiguous `rolling_back` transaction, missing exact rollback authority, manual-repair requirement, direct leak, or failure to automatically converge is FAIL.

### 2D — explicit service stop -> start

While protected/verified:

1. `systemctl stop podlazd.service`;
2. require deliberate product teardown converges and ordinary host networking works;
3. direct-uplink probe becomes usable only after protection/session teardown is complete;
4. `systemctl start podlazd.service`;
5. require daemon becomes ready but remains `Disconnected`;
6. prove no continuation/autoconnect from the prior ordinary session.

Then explicitly reconnect the candidate TUN session for the same-candidate reinstall test.

### 2E — same-candidate reinstall

While the candidate session is verified, reinstall the exact same candidate `.deb` with `dpkg -i`.

Require daemon replacement through package lifecycle, no second connect, automatic return to verified, protected data plane, and strict combined privacy proof during recovery.

This is an additional regression only; Phase 1 remains the real upgrade qualification.

## Phase 3 — candidate inactive resource baseline

The warmed inactive baseline is acquired explicitly and exactly once after the full candidate lifecycle matrix, including durable rollback interruption, and before collision/coexistence allocation.

Sequence:

1. normal `podlaz disconnect` after candidate lifecycle tests;
2. require conclusive `Disconnected` and no cleanup-required Network Session/Privacy Envelope state;
3. require ordinary route/DNS/TCP/HTTPS and direct-uplink baseline usability;
4. wait a bounded settle/warm interval sufficient for daemon/cgroup resource stabilization;
5. discover the exact current packaged daemon identity;
6. capture `warmed_inactive_candidate_baseline` for daemon/cgroup metrics;
7. persist this boundary before any synthetic collision fixture is created.

This baseline is the reference for post-disconnect retained deltas and later reconnect non-accumulation checks. The report must never refer to a warmed inactive baseline unless this acquisition succeeded.

## Phase 4 — pre-connect #260 historical-collision fixture

This is a distinct qualification scenario. It proves collision avoidance with foreign state that exists **before** candidate allocation.

At the inactive candidate boundary, create exact-owned Fixture A that deliberately occupies historical preferred Podlaz candidates when and only when those identities are currently free:

- historical TUN address candidate `198.18.0.1/32` on a documentation-safe synthetic unrelated link;
- historical routing table `51820` with an exact documentation-prefix route;
- historical policy priorities `9999` and `10000` using selective documentation-safe rules that do not carry ordinary host traffic;
- optional unrelated DNS/nftables structure when safe and useful, but no fixture default route.

Every deterministic fixture identity is write-ahead recorded as `acquiring` before mutation. If any historical candidate is already occupied by pre-existing host state, preserve it and treat that exact existing occupancy as collision evidence; do not replace/delete it. Fixture A owns only objects it actually created.

Immediately before each mutation, recheck the candidate identity is free. After mutation, verify the exact object and transition its ledger entry to `acquired` durably.

Then connect candidate TUN while Fixture A is present.

Require:

- candidate reaches `Connected`/verified;
- exact persisted candidate TUN address is not the occupied historical address;
- exact persisted candidate routing table is not occupied table `51820`;
- exact persisted candidate priorities do not collide with occupied `9999/10000`;
- server bootstrap/protected data plane works;
- Fixture A and any pre-existing occupant remain structurally unchanged;
- no foreign cleanup/stop occurs;
- strict candidate privacy proof passes.

This scenario is the authoritative `preexisting_collision_avoidance` evidence for #260. A later reconnect cannot substitute for it.

Fixture A remains present through the 60-minute soak and the later coexistence reconnect so preservation is tested across a longer lifecycle.

## Phase 5 — 60-minute active resource-soak orchestration envelope

All scheduled active network-change sub-scenarios execute inside one measured active-session envelope. The #260 Fixture A already exists before entry; a separate #262 Fixture B appears during the active session.

Canonical total primary active interval is exactly 60 wall-clock minutes:

```text
00-05 min  warm-up (samples retained but excluded from steady-state trend)
05-20 min  steady protected workload with pre-existing Fixture A
~20 min    create/churn separate active-appearance Fixture B (#262)
~30 min    Wi-Fi/NM reconnect when applicable
~40 min    real timed suspend/resume when supported
40-60 min  post-resume/post-churn stability
```

The five-minute warm-up is included in the 60-minute interval; canonical qualification is not 65 minutes.

`--soak-minutes N` may exist for debugging, but any value other than canonical 60 makes the overall result at best `PARTIAL_PASS`.

### Laptop-safe positive resource attribution

The laptop harness must **not** reuse the current isolated-runner `tun_soak_process.py` discovery policy wholesale.

Reusable evidence is positive and exact:

- daemon PID from the exact `podlazd.service` MainPID boundary;
- `/usr/bin/podlazd` executable identity, process start-time identity, and exact service cgroup;
- exactly one relevant committed Podlaz TUN transaction for active-session attribution;
- exact transaction-owned Xray child PID;
- `/usr/lib/podlaz/xray` executable identity;
- exact parent relationship to the proved daemon;
- exact same Podlaz service cgroup;
- exact durable runtime `config_ref` present in that child's command line;
- procfs/cgroup metric parsing and aggregation once those identities are proved.

The current `FOREIGN_CORE_NAMES`/foreign-process-name veto is an isolated-runner contamination policy and is **not** part of laptop qualification. A running `tailscaled`, OpenVPN, WireGuard userspace helper, another Xray, or other foreign VPN/core process is valid host baseline and must not fail resource attribution merely by product/process name.

Foreign processes are neither adopted nor measured as Podlaz. If an unexpected process is actually inside the Podlaz service cgroup or otherwise makes the positive Podlaz identity/cgroup attribution structurally ambiguous, attribution fails because of the concrete ownership ambiguity—not because its executable name appears in a blacklist.

Existing isolated E2E behavior should remain unchanged. Implementation may factor shared positive identity/metric primitives or introduce a laptop-safe attribution mode, but must not weaken controlled-runner isolation as a side effect.

### Continuous workload and sampling

Default sample interval: 60 seconds. Read-only `doctor --tun` cadence: about every 10 minutes.

Generate bounded low-volume traffic:

- protected DNS resolution;
- small HTTPS request with body discarded;
- product status;
- periodic `doctor --tun`;
- privacy observation around every recovery-prone event.

Attribute resources to exact current `podlazd` and exact supervised Xray child, not same-name processes.

Collect, where available:

For daemon and Xray:

- RSS/PSS;
- CPU time and derived interval utilization;
- threads/tasks;
- total FDs;
- regular/pipe/anon-inode FDs;
- socket FDs;
- TCP listen/established/other;
- UDP connected/unconnected/other;
- UNIX/unclassified sockets.

For `podlazd.service` cgroup:

- `memory.current`;
- raw `memory.peak` when available;
- `pids.current`;
- cumulative CPU counters where available.

### Resource metric scope

The report distinguishes measured-window metrics from monotonic/lifetime counters.

For the exact 60-minute active window:

- `soak_memory_current_sampled_peak_bytes = max(memory.current samples inside the 60-minute interval)`;
- median/last `memory.current` use only samples from that interval;
- daemon/Xray RSS/PSS peaks are maxima of samples from that interval;
- CPU consumption/utilization is computed from counter deltas bounded by the interval, not from raw cumulative counters;
- warm-up samples are retained but excluded from steady-state slope/trend calculations where already specified.

Because `memory.current` is sampled, its maximum is explicitly a **sampled active-window peak**, not a claim about an unobserved instantaneous kernel maximum.

Raw cgroup `memory.peak` is reported separately as `service_cgroup_lifetime_memory_peak_bytes` (or an equivalently explicit schema name). It is a service/cgroup lifetime high-water mark and may include package upgrade, restart, crash, durable rollback interruption, or reinstall activity that occurred before the measured hour. It must never be labelled simply `60-minute soak peak`.

Likewise, cumulative `cpu.stat` usage and process CPU ticks may be stored as raw structural evidence, but only interval deltas are attributed to the measured soak.

Do not reset `memory.peak` merely to make reporting convenient; such a cgroup mutation is unnecessary for the acceptance goal.

Record min/median/max/last for appropriate sampled metrics, warm-up-to-end delta, simple trend/slope where meaningful, and later post-disconnect/reconnect deltas. Existing conservative non-accumulation invariants may remain pass/fail checks; new thresholds are observations until justified by repeated evidence.

### 5A — separate #262 active foreign-state appearance/churn at ~20 min

Fixture B is separate from pre-existing collision Fixture A.

Create product-neutral synthetic surrounding state using only verified-free documentation-safe identities, for example one unrelated link/address, one exact route in another private numeric table, and selective policy rule(s). It must not use the historical candidates already reserved for Fixture A and must not carry ordinary host traffic.

Rules:

- inventory/read-only allocation first;
- write-ahead each deterministic Fixture B identity before mutation;
- live collision check immediately before every mutation;
- verify and durably mark each created object `acquired`;
- no foreign/global cleanup.

After Fixture B appears, require Podlaz fresh observation/reconciliation and verified protected state. Perform one exact Fixture B route replacement or link transition, require convergence again, then optionally remove/re-add an exact Fixture B element if the planned #262 observation benefits from a second surrounding-state transition.

Any Fixture B cleanup is exact-owned only and uses the crash-atomic releasing protocol. Podlaz itself must not delete Fixture B.

This sub-scenario proves `new surrounding routing layer appears while active`; it does not substitute for the pre-connect #260 collision test.

### 5B — Wi-Fi / DHCP reconnect at ~30 min

Only on a local NetworkManager-managed Wi-Fi uplink.

Privately record exact NM connection/uplink/address/gateway identity. Before bringing the connection down, durably record the exact planned down/up mutation, the original connection identity, and the restoration target as `acquiring`. Then perform controlled down/up of the exact original connection. Never restart NetworkManager or resolved.

After reconnect require:

- original NM connection restored;
- ledger state reflects verified restoration/acquisition outcome;
- ordinary default route available;
- Podlaz self-converges to verified;
- protected DNS/HTTPS;
- combined deterministic privacy proof across the protected recovery interval;
- report whether DHCP/address/gateway identity actually changed.

Returning the same lease is still a Wi-Fi reconnect PASS with `dhcp_identity_changed=false`; do not fabricate renewal.

Unsupported Wi-Fi is `SKIP_HOST_CAPABILITY`; `--skip-wifi-churn` is `SKIP_USER_REQUEST` and prevents `QUALIFIED_PASS`.

### 5C — timed suspend/resume at ~40 min

Before suspend prove verified state and durably flush private evidence/checkpoint state. Use a bounded RTC/timed suspend for roughly 60-120 seconds only when local host capability supports it.

After resume:

- prove a meaningful suspend interval occurred;
- allow normal NM/uplink convergence without restarting services;
- require Podlaz returns verified automatically;
- require protected DNS/HTTPS;
- require combined deterministic privacy proof for the post-resume protected recovery interval;
- collect immediate/post-settle resource samples;
- record natural daemon/Xray generation changes.

Unsupported suspend is a capability skip. `--skip-suspend` is user-forced partial coverage.

### Privacy semantics throughout soak

Any successful bound direct-uplink probe while candidate protection is logically active/recovering is immediate FAIL.

Any failed/timeout probe is classified only with contemporaneous exact local Privacy Envelope/packet-path evidence. External timeout alone is never treated as `blocked` or PASS.

## Phase 6 — disconnect, resource cleanup, and coexistence reconnect

After the 60-minute envelope:

1. normal `podlaz disconnect`;
2. require clean `Disconnected`/inactive;
3. require Network Session/Privacy Envelope cleanup convergence;
4. direct-uplink probe succeeds again;
5. ordinary DNS/TCP/HTTPS works;
6. Fixture A remains unchanged; Fixture B is either still exact-owned/preserved or has already been exact-cleaned according to its declared lifecycle;
7. collect post-disconnect daemon/cgroup boundary after bounded settle;
8. compare against `warmed_inactive_candidate_baseline`.

Reconnect the candidate while Fixture A already exists.

Require:

- collision-safe allocation remains non-colliding with Fixture A;
- verified protected data plane;
- Fixture A preserved;
- 5-10 minute second-session resource observation;
- no unexpected resource accumulation relative to first session and warmed inactive baseline.

Disconnect again and prove clean boundary.

Then release only exact Fixture B residuals (if any) and Fixture A acquired objects in reverse dependency order using the crash-atomic releasing protocol. Pre-existing historical occupants are never removed. No synthetic networking fixture may survive into terminal/reboot phases.

## Phase 7 — controlled established-session terminal failure

This phase proves #261/#262 terminal semantics rather than using explicit disconnect as a proxy.

Use only a narrow, already-existing release-built E2E/fault-injection seam that drives the normal production reconciliation/terminal lifecycle. Do not add a second terminal implementation to the harness.

If the candidate release does not contain the required reviewed gated seam, this mandatory scenario is `NOT_EXERCISED` and the run cannot be `QUALIFIED_PASS`.

Safe orchestration:

1. while disconnected, prove the exact temporary acceptance systemd drop-in path is absent/free;
2. persist the exact planned drop-in/marker identity and baseline as `acquiring`, then durable-commit the checkpoint;
3. install the exact root-owned drop-in enabling only the required terminal/reconciliation/privacy-pause hooks and create the exact private `/run` marker directory;
4. verify both exact live identities and durably mark them `acquired`;
5. `systemctl daemon-reload` and restart daemon while disconnected;
6. connect the normal profile and require verified protected state;
7. start the combined privacy observation;
8. trigger the controlled unrecoverable terminal evidence;
9. at the supported post-data-plane-clean/pre-envelope-remove pause, require direct egress is not successful **and** exact local Privacy Envelope/packet-path proof remains conclusive;
10. release the pause and require normal production terminal teardown completes;
11. require `Disconnected`, ordinary DNS/TCP/HTTPS, and direct-uplink egress restored;
12. require no automatic reconnect after a same-boot daemon restart;
13. release only the exact acceptance drop-in/marker state through `releasing -> verify -> released`, run `systemctl daemon-reload`, and leave daemon cleanly disconnected.

Any leak, inconclusive protection evidence, incomplete cleanup, false clean status, retry, or drop-in restoration failure is FAIL.

## Phase 8 — reboot A: autostart disabled

Before touching persistent boot policy:

- prove all synthetic networking/fault-injection fixtures are gone;
- prove original autostart restoration material is usable;
- persist strict root-owned checkpoint and mutation ledger;
- durably record `autostart-disable` as `acquiring`, including exact original policy and intended disabled target;
- disable autostart through normal CLI;
- verify disabled, then durably mark that policy mutation `acquired`;
- record current boot identity privately;
- checkpoint `awaiting-autostart-off-reboot`;
- instruct the user to reboot manually.

The harness never invokes reboot itself.

### Resume A

Require a new `boot_id`, same candidate package, normally started daemon, conclusively `Disconnected`, no fresh VPN attempt, ordinary network, and autostart disabled.

Before enabling autostart for the normal test profile, durably record the exact intended profile/mode policy transition and current disabled baseline as `acquiring`. Then enable via normal CLI, verify `Enabled for next boot`, durably mark the mutation `acquired`, checkpoint `awaiting-autostart-on-reboot`, and instruct reboot B.

`--abort` is valid before or after this reboot and must restore the original policy from the checkpoint.

## Phase 9 — reboot B: successful autostart and same-boot no retry

On resume B require another new `boot_id`, same candidate, bounded network readiness, exactly one logical automatic attempt, `Connected`/verified, protected data plane, and combined privacy proof for any protected restart/recovery window.

Restart daemon once and prove:

- daemon PID changes;
- same Network Session resumes;
- no second boot attempt is admitted;
- combined deterministic privacy proof passes.

Then explicit `podlaz disconnect`, wait clean inactive, restart daemon, and prove it stays disconnected in the same boot.

Next acquire the temporary terminal-autostart profile using the dynamic-identity protocol:

1. acquire the exclusive user profile-state lock;
2. persist baseline inventory digest, run-unique documentation-safe marker/fingerprint, and `state=acquiring`, then durable-commit;
3. create the profile once through normal user-owned CLI;
4. verify the exact returned/new profile is the unique planned addition and baseline profiles are unchanged;
5. durably persist its exact profile ID and `state=acquired`, then release the lock;
6. before changing autostart, durably record the exact synthetic profile/mode target plus current policy as another `acquiring` entry;
7. set autostart to that profile for the **next boot** through normal CLI;
8. verify the exact new policy and durably mark it `acquired`;
9. record current boot identity;
10. checkpoint `awaiting-terminal-autostart-reboot`;
11. instruct reboot C.

At this point `--abort` is especially important: abandoning the run must reconcile any in-flight acquisition, restore the original policy, and delete only the exact synthetic profile.

## Phase 10 — reboot C: terminal autostart failure and no retry

On resume C require another new `boot_id` and same candidate package.

The synthetic autostart request must enter the canonical boot attempt/connect lifecycle and converge to a conclusively terminal outcome, not an endless retry.

Require:

- attempt reaches terminal only after cleanup convergence;
- product ends `Disconnected` with the stable high-level terminal connect reason;
- ordinary network/direct-uplink egress is usable;
- no Network Session/Privacy Envelope cleanup remains pending;
- same-boot `systemctl restart podlazd.service` does not start another automatic connect and does not replace the consumed terminal attempt authority.

Then restore original autostart policy and delete the exact synthetic profile using the crash-atomic `releasing -> mutate -> verify -> released` protocol for each object. Update the checkpoint only through durable commits.

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
10. no mutation-ledger entry remains unresolved in `acquiring` or `releasing`;
11. run tree ownership/modes revalidated;
12. final sanitized reports written;
13. persistent checkpoint removed only after all required restoration checks pass.

A cleanup/restoration failure is overall `FAIL` even if product scenarios passed.

## In-process finalizer semantics

Register cleanup authority through the same write-ahead mutation ledger used by `--abort`. Finalize in reverse dependency order while preserving lifecycle semantics:

```text
stop acceptance workload/privacy observers
reconcile any acquiring/releasing entries
attempt normal Podlaz disconnect if acceptance session is active
wait bounded product convergence
restore exact NM connection if acquired/left down
release exact Fixture B then exact Fixture A created objects
release exact acceptance fault-injection drop-in/markers
restore original autostart when changed
remove exact temporary synthetic profile
verify ordinary networking and direct-uplink baseline
write failure/cleanup evidence
```

Cleanup never changes a FAIL to PASS and never uses broad host repair.

For any mutation that can survive shell exit or reboot, checkpoint-driven `--abort` plus write-ahead authority is the authoritative fallback; a `trap` alone is not considered sufficient.

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
- warmed inactive daemon/cgroup baseline;
- median/last sampled active resource metrics;
- sampled 60-minute `memory.current`/RSS/PSS peaks;
- service-cgroup lifetime `memory.peak` high-water evidence as a separately named metric;
- post-disconnect retained deltas;
- reconnect deltas and non-accumulation evidence.

Do not update the system when versions differ from preferred environments.

`requirements-observation.json` explicitly calls every measured value an observation. Minimum/recommended requirements are derived later from multiple successful runs plus engineering margin, not asserted from one laptop.

## Reports

### `summary.txt`

Sanitized/shareable example:

```text
Release package: podlaz <version> (<arch>)
Qualification: QUALIFIED_PASS|PARTIAL_PASS|FAIL

previous_is_strictly_lower                 PASS|NOT_EXERCISED
lower_release_upgrade_continuity           PASS|NOT_EXERCISED
legacy_upgrade_privacy                     PASS|SKIP_RELEASE_CAPABILITY
candidate_baseline                         PASS
graceful_restart_continuity                PASS
unexpected_daemon_death_recovery           PASS
durable_rollback_crash_recovery            PASS
explicit_service_stop_start                PASS
same_candidate_reinstall_continuity        PASS
candidate_privacy_no_direct_leak           PASS
warmed_inactive_candidate_baseline         PASS
preexisting_collision_avoidance            PASS
resource_soak_60m                          PASS
active_foreign_state_churn                 PASS
wifi_reconnect                             PASS|SKIP_HOST_CAPABILITY|SKIP_USER_REQUEST
suspend_resume                             PASS|SKIP_HOST_CAPABILITY|SKIP_USER_REQUEST
post_disconnect_cleanup                    PASS
coexistence_reconnect                      PASS
reconnect_resource_nonaccumulation         PASS
runtime_terminal_convergence               PASS
runtime_terminal_no_retry                  PASS
real_boot_autostart_off                    PASS
real_boot_autostart_on                     PASS
successful_autostart_same_boot_no_retry    PASS
terminal_autostart_failure                 PASS
terminal_autostart_same_boot_no_retry      PASS
original_autostart_policy_restored         PASS
final_host_restoration                     PASS

Resource observations:
  measured active interval: <duration>
  samples: <count>
  warmed inactive daemon/cgroup: <values>
  daemon RSS median/sampled peak: <values>
  Xray RSS median/sampled peak: <values>
  60m sampled cgroup memory.current peak: <value>
  service cgroup lifetime memory.peak high-water: <value>
  daemon/Xray FD start/end deltas: <values>
  post-disconnect/reconnect deltas: <values>
```

`report.json` contains versioned structural scenario outcomes, actual soak duration, skip reason classes, privacy-evidence classifications, mutation-ledger states, and numeric resource summaries.

`requirements-observation.json` contains sanitized environment/resource observations suitable for later aggregation.

No report automatically uploads anywhere.

## Harness CLI

Canonical qualification:

```text
sudo ./scripts/acceptance/release-laptop.sh <candidate.deb> [--profile <profile-id>]
sudo ./scripts/acceptance/release-laptop.sh --resume
sudo ./scripts/acceptance/release-laptop.sh --abort
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
- no successful durable-rollback interruption proof => no `QUALIFIED_PASS`;
- no successful pre-connect historical-collision proof => no `QUALIFIED_PASS`;
- genuine host/release capability skip remains distinct and is reported structurally;
- mandatory privacy or durable-authority evidence that is ambiguous/inconclusive => scenario FAIL, not partial success.

These flags are harness controls, not Podlaz product settings.

## Implementation boundaries

Prefer reuse over duplication, but preserve the laptop/coexistence boundary:

- safe logging/redaction conventions from `scripts/e2e/lib/e2e.sh`;
- **positive exact** podlazd/Xray procfs+cgroup identity semantics, metric parsing, socket/FD parsing, and aggregation from `tun_soak_*`;
- do **not** reuse the isolated-runner `FOREIGN_CORE_NAMES`/foreign-product-name veto or trusted-host isolation policy for laptop attribution;
- installed product status/doctor/lifecycle APIs;
- current structural redaction scanner;
- existing release-built exact Privacy Envelope ownership/composition evidence;
- existing release-built #259 rollback-pause and terminal/reconciliation hooks where explicitly required.

Do not reuse helpers that build dev packages, install dependencies, assume a disposable trusted host, reject foreign VPNs merely by process name, or perform broad cleanup.

Keep narrow components for:

1. package/provenance and Debian version-order / true-upgrade orchestration;
2. user/profile/autostart state;
3. combined functional + deterministic local privacy proof;
4. lifecycle restart/crash/durable-rollback/stop-start scenarios;
5. crash-atomic mutation ledger and dynamic profile acquisition;
6. warmed inactive baseline and laptop-safe positive resource attribution;
7. pre-connect #260 Fixture A allocation/exact cleanup;
8. active #262 Fixture B appearance/churn/exact cleanup;
9. Wi-Fi/suspend events;
10. soak scheduling/resource sampling and metric-scope reporting;
11. controlled terminal hooks;
12. reboot checkpointing and `--abort` restoration;
13. report redaction/qualification evaluation.

The user-facing entry point remains one shell script.

## Deterministic TDD contract

### Package/qualification

Prove:

- candidate/previous `.deb` validation;
- Debian version ordering uses `dpkg --compare-versions` semantics or an exact equivalent, never lexical comparison;
- equal/newer previous packages cannot satisfy `lower_release_upgrade_continuity`;
- no source-build command;
- no OS update/install/fix-broken command;
- true lower-release active-TUN -> candidate ordering occurs before candidate disconnected install;
- no manual service repair/second connect in upgrade continuity;
- same-version reinstall cannot satisfy lower-release upgrade continuity;
- lower-release privacy is capability-gated and cannot be misattributed as a candidate regression when the previous release predates the feature;
- shortened/skip/no-reboot runs cannot yield `QUALIFIED_PASS`;
- capability skip and user skip remain distinct.

### Lifecycle/privacy/durable rollback

Prove orchestration contains separate graceful restart, ordinary main-process SIGKILL recovery, durable-`rolling_back` interruption, and explicit stop/start scenarios.

Durable rollback tests prove:

- rollback-pause hook is acquired only from a clean inactive boundary;
- restart drives the transaction to durable `rolling_back` before the injected kill;
- exact non-empty Podlaz-owned rollback authority is observed before daemon death;
- SIGKILL occurs only after that proof;
- no manual `systemctl start/reset-failed`, second connect, recover, route, DNS, or firewall repair occurs before verdict;
- replacement daemon automatically uses exact durable authority and converges or retains actionable exact ownership without anonymous residue;
- this scenario cannot be satisfied by the ordinary active-daemon crash test.

Privacy tests prove:

- baseline direct-bound target reachability before candidate protected qualification;
- a successful direct-bound probe during candidate protection is immediate FAIL;
- failed/timeout external probe alone is never PASS;
- failed probe + exact matching Privacy Envelope/packet-path composition is positive interval evidence;
- ambiguous/missing local protection evidence makes mandatory privacy scenario FAIL;
- post-terminal/disconnect direct probe succeeds;
- metadata/table-name presence alone cannot satisfy privacy acceptance.

### Crash-atomic mutation ledger

Deterministic crash-point tests cover at least:

```text
before acquiring record commit
acquiring committed / before mutation
mutation succeeded / before acquired commit
acquired committed
releasing committed / before release mutation
release succeeded / before released commit
released committed
```

For deterministic identities prove resume/abort distinguishes exact baseline, exact planned live result, and ambiguous drift without deletion by resemblance.

For synthetic profile acquisition prove:

- exclusive user-state lock + baseline inventory + run-unique marker/fingerprint are durably committed before product create;
- crash after create but before ID commit can recover authority only from exactly one matching baseline addition;
- zero matches is treated as not acquired;
- multiple matches or baseline ambiguity fail closed;
- returned/adopted profile ID is verified before exact deletion authority exists.

Checkpoint durability tests cover temp-write, restrictive ownership/mode, file flush, atomic rename, and containing-directory flush semantics through a filesystem abstraction/fault model rather than relying on timing.

### Candidate inactive resource baseline

Prove the harness performs candidate disconnect -> bounded settle -> exact daemon identity -> `warmed_inactive_candidate_baseline` capture before any #260 collision fixture is created.

Report/resource comparison code must reject a missing baseline rather than silently comparing against zero/another phase.

### Laptop-safe resource attribution and metric scope

Prove:

- exact daemon/Xray positive attribution still succeeds when an unrelated foreign VPN/core process is present outside the Podlaz service cgroup;
- another foreign `xray` process cannot be mistaken for the transaction-owned child;
- an unexpected process inside the Podlaz service cgroup remains a concrete ambiguity and fails attribution;
- existing isolated-runner foreign-process veto behavior is unchanged;
- 60-minute cgroup memory peak is `max(memory.current samples)` and is explicitly labelled sampled-window peak;
- raw `memory.peak` is reported only as service/cgroup lifetime high-water evidence;
- cumulative CPU counters are converted to interval deltas before soak attribution.

### #260 collision scenario

Deterministically prove:

- historical address/table/priorities are occupied before candidate connect when free;
- pre-existing occupants are preserved rather than replaced;
- candidate persisted allocation differs from every occupied historical identity;
- later reconnect cannot be used as substitute evidence for `preexisting_collision_avoidance`;
- Fixture A survives candidate connect/disconnect and is exact-cleaned only when owned by the harness.

### Soak/#262 scheduling

Use fake clock/event scheduler tests to prove exactly one active Fixture B appearance/churn event, one Wi-Fi event when applicable, and one suspend event occur inside the canonical 60-minute envelope. Warm-up occupies minutes 0-5 of that same hour and is excluded from steady-state trend calculations.

Fixture A must already exist before the soak starts; Fixture B must not exist until its scheduled active-appearance event.

### Terminal scenarios

Prove runtime terminal acceptance uses only the gated production terminal seam, validates combined privacy evidence before envelope removal, verifies ordinary network after convergence, and checks same-boot no retry.

Prove reboot checkpoint state includes terminal-autostart phase and same-boot restart cannot admit a second attempt.

### Checkpoint/abort safety

For every resumable phase prove:

- checkpoint schema is strict, durable-atomic, root-owned, and path-bound;
- persistent mutations use write-ahead `acquiring` state before mutation and verified `acquired` state afterward;
- release/restoration uses `releasing` before mutation and verified `released` afterward;
- `--abort` first reconciles in-flight ledger states rather than assuming mutation success/failure;
- `--abort` can restore original autostart through product API;
- `--abort` deletes only exact synthetic profile/drop-in/fixture identities owned by the run;
- partial/ambiguous restoration retains checkpoint/evidence and exits non-zero;
- checkpoint is removed only after successful restoration verification;
- successful abort is `ABORTED_CLEAN`, never qualification success;
- no test boot service/cron/login hook exists.

### User/path/privacy boundary

Prove:

- Podlaz CLI runs as original user;
- report tree is user-owned;
- root checkpoint remains root-owned;
- unsafe/symlinked/cross-owner artifact paths fail before mutation;
- every real host interface becomes a structural role in shareable output;
- private identifiers never enter public reports;
- temporary synthetic profile uses crash-safe exact acquisition and exact deletion.

### Exact fixture ownership

Prove occupied identities are preserved, exact tuples only are removed, broad flushes are absent, ambiguous drift fails closed, and fixture authority survives harness crash points because planned/acquired identities are write-ahead durable.

## Validation before merge

Run relevant repository checks:

```text
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
shellcheck <new/changed shell files>
```

Hosted CI cannot execute real Wi-Fi, suspend, root networking, package-upgrade-from-installed-release, durable host rollback interruption, and real reboot phases. Deterministic tests therefore enforce safety/orchestration without root, while the laptop harness supplies the real-host evidence.

A full local run against actual release packages is evidence produced by the harness, not a prerequisite for merging the harness implementation itself.

## Success criteria

Implementation is complete when:

- one release-oriented command can qualify the candidate without building Podlaz or updating the OS;
- Debian version semantics prove the previous release is strictly lower before lower-release upgrade evidence is accepted;
- real lower-release active-TUN -> candidate upgrade is the canonical package continuity test;
- graceful restart, ordinary unexpected daemon death, durable `rolling_back` interruption, explicit service stop/start, and same-version reinstall are separate lifecycle scenarios;
- durable rollback interruption proves exact transaction-owned recovery authority before injected process death and automatic convergence afterward without manual repair;
- candidate no-direct-leak requires both functional direct-probe evidence and deterministic exact local Privacy Envelope/packet-path proof; external timeout alone can never false-pass;
- every persistent acceptance mutation has crash-atomic write-ahead acquisition/release authority, including the product-returned synthetic profile identity;
- checkpoint-driven `--abort` can reconcile in-flight `acquiring/releasing` states and fails closed on ambiguity;
- a warmed candidate-inactive baseline is explicitly acquired before coexistence/soak resource comparisons;
- laptop resource attribution uses positive exact Podlaz daemon/Xray ownership and permits unrelated foreign VPN/core processes outside the Podlaz cgroup;
- the existing isolated-runner foreign-process-name veto is not weakened;
- the 60-minute resource report distinguishes sampled-window `memory.current` peak from lifetime `memory.peak` high-water evidence and uses CPU interval deltas;
- a pre-connect #260 Fixture A actually occupies historical candidates and forces different candidate allocation;
- a separate active #262 Fixture B appears/churns inside the one exact 60-minute soak envelope;
- Wi-Fi reconnect and suspend/resume run once inside that same measured envelope when applicable;
- controlled runtime terminal failure converges through the real production terminal path to ordinary networking with no retry;
- three manual reboot/resume stages prove autostart-off, successful autostart/no-retry, and terminal-autostart/no-retry semantics;
- harness-created state is removed exactly and original autostart is restored;
- shareable reports are deterministic and host-private identifiers are role-mapped/redacted;
- `QUALIFIED_PASS` cannot be emitted for shortened, user-skipped, missing durable-rollback evidence, false-collision, missing-baseline, ambiguous-privacy, ambiguous mutation authority, or incomplete-restoration coverage;
- resource evidence is sufficient for later system-requirement analysis;
- failures are never hidden by generic repair.
