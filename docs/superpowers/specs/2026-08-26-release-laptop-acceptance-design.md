# Release Laptop Acceptance Harness Design

Status: approved design for local release qualification on a maintainer laptop.

## Goal

Provide one release-oriented acceptance harness that exercises the installed Podlaz Debian package on a real laptop as deeply as practical without requiring a dedicated self-hosted E2E server.

The harness is not another networking implementation. It is a product-validation orchestrator over the lifecycle, package, coexistence, privacy, reconciliation, and autostart contracts implemented by Issues #259-#263.

Primary invocation:

```text
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<version>_linux_<arch>.deb
```

When profile selection is ambiguous:

```text
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<version>_linux_<arch>.deb --profile <profile-id>
```

The long first phase is followed by two explicit user-driven reboots. After each reboot the user resumes the same run with:

```text
sudo ./scripts/acceptance/release-laptop.sh --resume
```

No temporary boot service, timer, cron job, login hook, or auto-resume unit is installed. The user remains in control of every reboot.

## Validation scope

One coordinated run should cover, where host capabilities permit:

1. release `.deb` validation and install/reinstall;
2. clean TUN connect and protected data-plane verification;
3. daemon restart continuity while connected;
4. package replacement/reinstall continuity while connected;
5. coexistence with unrelated synthetic TUN/routing/DNS/firewall state;
6. safe route/netlink churn while Podlaz is active;
7. controlled NetworkManager Wi-Fi reconnect and DHCP/uplink re-observation;
8. one real timed suspend/resume while the protected session is active;
9. one-hour resource-soak observation with periodic traffic and diagnostics;
10. disconnect cleanup and ordinary-network restoration;
11. reconnect and second-session resource comparison;
12. a real reboot with autostart disabled;
13. a real reboot with autostart enabled;
14. same-boot no-retry after explicit disconnect from an autostarted session;
15. restoration of harness-created host state and the user's pre-test autostart policy;
16. a detailed private report plus sanitized shareable summaries.

The harness also collects environment/resource observations that can later support evidence-based system requirements. One laptop run is an observation, not universal proof of minimum requirements.

## Hard safety constraints

### Release package only

The harness tests only the `.deb` supplied by the user.

It must not:

- run `go build`, `go test`, `go install`, `make`, `goreleaser`, or another Podlaz build path;
- substitute Podlaz binaries from the repository working tree;
- build or install a development package from source;
- modify the release package to make validation pass.

The product under test is the installed release package: `/usr/bin/podlaz`, `/usr/bin/podlazd`, `/usr/lib/podlaz/xray`, the packaged service, policy, and package maintainer scripts.

Before installation the harness validates:

- input path is a regular file;
- package name is `podlaz`;
- package architecture matches `dpkg --print-architecture`;
- package version is readable;
- SHA-256 is recorded;
- when a sibling `SHA256SUMS` exists and contains the exact package entry, that digest is verified.

The tested release remains installed after acceptance. The harness does not downgrade back to the previously installed Podlaz version.

### No operating-system updates or dependency repair

The harness must not update the laptop or opportunistically modify its software environment.

It must not invoke:

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

The only package mutation is installation/reinstallation of the exact supplied Podlaz `.deb` via `dpkg -i`.

Unsatisfied dependencies are a test-environment failure. The harness reports them and stops; it never repairs them automatically.

### No generic network repair

A failed product scenario remains failed. The harness must not make a test pass by repairing Podlaz or the host out-of-band.

The success path must never invoke:

```text
podlaz recover --execute
ip route flush ...
ip rule flush ...
nft flush ruleset
systemctl restart NetworkManager
systemctl restart systemd-resolved
```

Normal product commands such as `podlaz connect`, `disconnect`, `status`, `doctor --tun`, and `autostart ...` are allowed.

If Podlaz does not self-heal after a scenario expected to self-heal, that scenario is FAIL. Final cleanup may still make a bounded attempt to return the laptop to normal operation, but cleanup cannot rewrite the verdict to PASS.

### Exact ownership for synthetic fixtures

The harness may create unrelated synthetic network state, but it owns only exact objects it created after proving each candidate free immediately before mutation.

No cleanup operation may delete a route, table, rule, interface, address, resolved link, or nftables object because it merely looks like a test value.

In particular:

- no `ip route flush table N` for an unproven namespace;
- no `ip rule flush`;
- no `nft flush ruleset`;
- no deletion of pre-existing historical Podlaz-looking values.

The private fixture manifest records enough identity to delete only the exact created object. If live state later becomes ambiguous, cleanup fails closed and reports the ambiguity rather than deleting by resemblance.

### Local laptop only for disruptive stages

Wi-Fi reconnect and suspend/resume must not run over SSH.

If a remote/SSH session is detected, disruptive stages are skipped before mutation and reported as `SKIP_REMOTE_SESSION`/`SKIP_HOST_CAPABILITY` rather than attempted.

## Original user and state boundary

The harness starts under `sudo`, but normal Podlaz CLI operations run as the original `SUDO_USER`, not root.

It derives the original user's home/state paths from the account database, not root's `$HOME`.

The harness never imports a profile URI. It uses a profile already present in the user's Podlaz profile store.

Profile selection:

- `--profile <profile-id>` is preferred and unambiguous;
- without it, auto-selection is allowed only when exactly one profile in the installed release client validates for `--mode tun`;
- zero valid TUN profiles is failure;
- multiple valid TUN profiles is failure with guidance to rerun using `--profile`.

Profile IDs/names, endpoints, user identities, subscription URLs, runtime configs, transaction IDs, and other sensitive lifecycle material are private evidence only.

## Pre-mutation baseline

The first run has two preflight layers.

### Layer A — before package mutation

Before `dpkg -i` the harness records what can be established safely from the currently installed environment:

- current installed Podlaz version, if any;
- current daemon service active/inactive state when conclusively observable;
- current Podlaz lifecycle state when the installed CLI is available;
- original autostart policy when the currently installed release supports it;
- ordinary host IPv4 route, DNS, and HTTPS usability;
- required host tools already present;
- supplied `.deb` package metadata and digest.

If the currently installed Podlaz can prove an active or ambiguous session, the harness stops before package mutation and asks the user to reach clean `Disconnected` first. It does not silently replace an existing session.

If Podlaz is not installed, absence is considered clean only if read-only inspection does not find obvious Podlaz runtime/process state that would make first install unsafe to classify.

Original autostart must be captured before package mutation whenever it exists, so package install/reinstall itself cannot silently erase policy without the test noticing. If the preinstalled version predates autostart and no persistent manifest exists, original policy is treated as disabled/absent.

### Layer B — after the release package is installed, before networking mutation

Using only the installed supplied release client:

- require product status to be conclusively `Disconnected`/inactive;
- require no cleanup-required Network Session authority;
- select/validate the test profile for TUN mode;
- prove daemon/socket/package health;
- capture warmed inactive resource baseline.

The selected profile is therefore validated by the exact release being qualified, not by an older installed client.

## Persistent acceptance checkpoint

Real reboot validation uses a small root-owned checkpoint that survives reboot, outside Podlaz daemon runtime/state, for example:

```text
/var/lib/podlaz-release-acceptance/
```

Requirements:

- root-owned directory mode `0700`;
- files mode `0600`;
- versioned strict schema and bounded size;
- atomic writes;
- no automatic startup integration;
- deleted after successful finalization or explicit abort cleanup.

Checkpoint content includes only resume authority for this acceptance run:

- schema version/run ID/current phase;
- package name/version/architecture/SHA-256;
- recorded Linux `boot_id` transitions;
- original user identity and selected profile ID, private only;
- accumulated structural results;
- original autostart restoration material;
- report directory;
- no synthetic networking fixture survives into reboot phases.

`--resume` is valid only when exactly one supported active checkpoint exists. It does not accept a new package/profile and cannot start a different run.

## Evidence layout and privacy

Create reports under the original user's state area, conceptually:

```text
~/.local/state/podlaz/release-acceptance/<run-id>/
  summary.txt
  report.json
  requirements-observation.json
  private/
    raw command output
    exact process/session identities
    network baseline and fixture manifests
    soak samples
    diagnostic output
```

`private/` is `0700`, private files `0600`. No evidence is uploaded automatically.

Shareable reports must not contain:

- profile ID/name;
- VPN endpoint/domain/IP;
- credential/user identity;
- egress public IP;
- local/private IP;
- SSID/BSSID;
- physical interface name when host-identifying;
- NetworkManager UUID;
- transaction/session ID;
- hostname/machine ID;
- generated config content.

Private raw evidence may contain host-specific values because it exists specifically for local diagnosis, but remains local and restrictive.

## Phase 1 — release package install/update

Record the previous installed version for context, then run exactly:

```text
dpkg -i <exact-input-deb>
```

Do not run a dependency repair command.

Prove:

- installed package version equals input version;
- installed product files come from the package;
- packaged service loads and reaches its daemon socket;
- daemon PID is valid;
- release CLI can read/validate the selected user profile;
- status is cleanly inactive before TUN mutation.

If the same candidate was already installed, this is a reinstall. If an older version was installed, it also tests normal disconnected upgrade.

## Phase 2 — initial protected TUN connection

Run installed `podlaz connect --mode tun <profile>` as the original user.

Require bounded convergence to product `Connected` plus verified TUN evidence through available daemon/operator surfaces.

Validate, where supported:

- active exact TUN/transaction lifecycle;
- Network Session authority;
- Privacy Envelope authority/presence without exposing identity publicly;
- protected DNS resolution;
- normal system resolution through the protected session;
- IPv4 TCP/TLS/HTTPS egress;
- read-only `doctor --tun` health;
- no cleanup-required state.

Probe response bodies such as public egress IP are discarded.

## Phase 3 — daemon replacement continuity

While connected/verified:

1. privately record daemon and session identity;
2. `systemctl restart podlazd.service`;
3. prove daemon PID changed;
4. do not call a second `connect`;
5. wait for the same logical Network Session to return `Connected`/verified;
6. require protection authority not to disappear across continuation;
7. recheck protected DNS/HTTPS;
8. require no manual recovery command.

Failure is a product failure even if explicit reconnect would work.

## Phase 4 — package replacement continuity

While still connected, reinstall the exact same supplied `.deb` using `dpkg -i`.

Require:

- package command succeeds;
- daemon PID changes as part of package lifecycle;
- no test-side service restart/start before the package-continuity verdict;
- no second `connect`;
- current Network Session returns verified;
- protected DNS/HTTPS work;
- exact recovery authority remains available if convergence is incomplete.

This tests actual release package lifecycle, not a source-built candidate.

## Phase 5 — synthetic foreign-state coexistence

Create product-neutral unrelated baseline state using documentation-only network values and exact ownership.

Possible fixture elements:

- one unrelated TUN device and point address;
- one verified-free numeric routing table with one exact documentation-prefix route;
- one/two selective policy rules;
- one unrelated nftables table;
- one dummy resolved link with documentation-only DNS/domain state when safe.

Rules:

- inventory before allocation;
- historical Podlaz candidate values may be occupied only if currently free and useful for collision coverage;
- if already occupied, preserve the pre-existing object and treat it as baseline;
- otherwise allocate another verified-free fixture identity;
- live collision check immediately before mutation;
- record exactly which objects this run created;
- never install a fixture default route carrying ordinary user traffic.

After fixture creation require Podlaz to remain/return verified. Perform safe fixture churn such as exact route replacement and fixture link down/up, then require fresh product convergence without foreign cleanup.

Store a normalized private structural fixture manifest. Prove the fixture remains structurally preserved after Podlaz disconnect and after a fresh Podlaz reconnect while the fixture still exists.

## Phase 6 — Wi-Fi / DHCP churn

If the underlying default uplink is a NetworkManager-managed Wi-Fi connection and the session is local, run one controlled reconnect.

Privately record:

- exact active NM connection UUID;
- interface identity;
- IPv4 address/gateway/default-route fingerprint;
- relevant NetworkManager/uplink identity.

Then:

```text
nmcli connection down <exact-original-uuid>
wait for down transition
nmcli connection up <exact-original-uuid>
```

Never restart NetworkManager or systemd-resolved.

After reconnect:

- require the original connection restored or explicitly fail cleanup;
- require an ordinary default route;
- require Podlaz to self-converge to verified;
- require protected DNS/HTTPS;
- record whether address/gateway/DHCP identity actually changed.

If the same lease returns, report Wi-Fi reconnect PASS but `dhcp_identity_changed=false`; do not fabricate a DHCP change or invoke a different DHCP client.

If suitable Wi-Fi is unavailable, report a host-capability skip.

## Phase 7 — one-hour resource soak

Reuse existing exact process/cgroup attribution concepts from the TUN resource-soak tooling, but remove dev-build/trusted-runner assumptions. The installed release package is the only product under test.

Default timing:

```text
warm-up:                    5 minutes
primary active observation: 60 minutes
sample interval:            60 seconds
doctor cadence:             10 minutes
post-disconnect settle:     bounded 1-2 minutes
reconnect observation:      5-10 minutes
```

The primary hour includes network events rather than only idle operation:

```text
00-05 min  warm-up
05-20 min  steady protected traffic/sampling
~20 min    synthetic foreign route/netlink churn
~30 min    Wi-Fi reconnect when supported
~40 min    real suspend/resume
40-60 min  post-resume stability/sampling
```

Every active sample attributes resources to the exact `podlazd` and its exact supervised Xray child.

Collect where available:

For `podlazd` and Xray:

- RSS;
- PSS;
- CPU time/derived utilization;
- threads/tasks;
- total FDs;
- regular/pipe/anon-inode FDs;
- socket FDs;
- TCP listen/established/other;
- UDP connected/unconnected/other;
- UNIX sockets;
- unclassified sockets.

For the service cgroup:

- `memory.current`;
- `memory.peak` when available;
- `pids.current`;
- CPU counters where available.

Shareable lifecycle context is structural only:

- connected/reconnecting/verified classification;
- daemon replacement count;
- Xray generation replacement count;
- transaction count;
- Network Session authority present/absent;
- Privacy Envelope authority present/absent.

### Bounded workload

Generate low-volume real traffic, not a throughput benchmark:

- periodic DNS resolution;
- periodic small HTTPS request with response body discarded;
- periodic status;
- read-only `doctor --tun` every ten minutes.

### Resource output

For key metrics record:

- min/median/max/last;
- warm-up-to-end delta;
- simple time slope/trend where meaningful;
- post-disconnect retained delta vs warmed inactive baseline;
- reconnect delta first vs second session;
- peak cgroup memory;
- peak RSS/PSS/tasks/threads/FDs/sockets.

Existing conservative lifecycle non-accumulation limits may remain pass/fail checks where they already represent tested invariants, especially cleanup and reconnect. Otherwise metrics are observations/warnings, not invented requirements.

`requirements-observation.json` records tested environment and resource high-water marks. It explicitly labels them observations so later runs across machines/releases can support real minimum/recommended requirements.

## Phase 8 — real suspend/resume

Around minute forty, when supported, perform one timed suspend with RTC wake for roughly 60-120 seconds.

Before suspend:

- prove protected verified state;
- record private session/daemon/Xray identities;
- flush private evidence to disk;
- ensure fixture mutation is not mid-operation.

After resume:

- confirm a meaningful suspend interval occurred;
- allow normal uplink/NM convergence without restarting services;
- require Podlaz to revalidate/reconcile and return verified automatically;
- require protected DNS/HTTPS;
- collect immediate and later resource samples;
- record natural daemon/Xray generation replacement if it occurred.

If the host cannot safely provide timed suspend/wake, report capability skip. If suspend occurred and Podlaz fails to recover automatically, record product failure.

## Phase 9 — disconnect, cleanup proof, reconnect

After the primary hour:

1. normal `podlaz disconnect`;
2. require clean `Disconnected`/inactive state;
3. require exact Network Session/Privacy Envelope cleanup convergence;
4. require ordinary host DNS/TCP/HTTPS;
5. prove synthetic foreign fixture still exists unchanged;
6. collect post-disconnect resource boundary after settle;
7. compare against warmed inactive baseline.

Reconnect the selected profile while the foreign fixture remains.

Require:

- collision-safe new session allocation;
- protected verified data plane;
- foreign fixture preserved;
- 5-10 minute second-session resource observation;
- no unexpected resource accumulation.

Disconnect again, repeat clean boundary checks, then remove only exact test fixture objects.

## Exact fixture cleanup

Cleanup uses only exact tuples created by this run, for example:

```text
ip route del <exact-destination> table <exact-test-table>
ip rule del <full-exact-selector/table/priority tuple>
ip address del <exact-cidr> dev <exact-test-interface>
ip link del <exact-test-interface after identity/kind recheck>
nft delete table <exact-family> <exact-test-table after exact identity check>
resolvectl revert <exact-dummy-link> before deleting that exact dummy link
```

Authoritative already-absent state may be idempotent success. Drifted/ambiguous identity is preserved and reported, not deleted by similarity.

The harness never manually removes Podlaz Privacy Envelope or product routing. Product state is cleaned through product lifecycle; product cleanup failure remains visible.

## Phase 10 — prepare real reboot A, autostart disabled

No synthetic fixture may survive into reboot phases.

Original autostart policy was captured in Layer A. Before changing it the harness proves restoration material is usable. If original policy is enabled, private material may read only the necessary daemon-owned manifest fields (profile ID/mode/generation context) and prove the profile still exists. Restoration later is performed through normal `podlaz autostart` CLI, never by overwriting the manifest file.

If restoration cannot be proven, abort before changing policy.

Prepare reboot A:

- Podlaz cleanly disconnected;
- disable autostart via CLI;
- confirm disabled;
- record current `boot_id`;
- persist checkpoint `awaiting-autostart-off-reboot`;
- instruct the user to reboot and rerun `--resume`.

The harness itself does not invoke reboot.

## Phase 11 — resume after reboot A

On `--resume`:

- strictly load checkpoint;
- require changed `boot_id`;
- require the same tested release package still installed;
- prove packaged daemon started normally;
- require Podlaz remains conclusively disconnected;
- require no fresh automatic VPN connection;
- require ordinary DNS/HTTPS;
- require autostart disabled.

Then enable TUN autostart for the selected test profile via normal CLI, confirm `Enabled for next boot`, record current `boot_id`, persist `awaiting-autostart-on-reboot`, and instruct another reboot.

## Phase 12 — resume after reboot B

On second `--resume`:

- require another new `boot_id`;
- require same release package installed;
- boundedly wait for daemon startup/network readiness;
- require one automatic TUN lifecycle reaches `Connected`/verified;
- require protected DNS/HTTPS;
- privately prove current-boot attempt authority represents one consumed logical attempt.

Restart `podlazd.service` once and prove:

- daemon PID changed;
- current Network Session returned verified;
- boot-attempt authority was not replaced/duplicated;
- no manual connect occurred.

Then explicit `podlaz disconnect`, wait clean inactive, restart daemon again, and prove it stays disconnected in the same boot. This is the real no-same-boot-retry proof.

## Restore original policy and final host state

At finalization:

1. restore original autostart through normal CLI;
2. if originally disabled, leave disabled;
3. if originally enabled, re-enable the exact saved profile/mode after proving it remains valid;
4. verify resulting policy structurally matches the baseline;
5. keep Podlaz cleanly disconnected (the first-run precondition was disconnected);
6. restore daemon service active/inactive runtime state when the original state was conclusively known and restoration is safe;
7. verify ordinary networking;
8. verify no synthetic TUN/routing/DNS/nft fixture remains;
9. remove acceptance checkpoint;
10. write final reports.

The release package remains installed.

Failure to restore original policy or fixture state makes the overall result FAIL. Do not silently claim success while the laptop differs from the captured non-package baseline.

## Failure/finalizer semantics

Register a finalizer only after each harness mutation succeeds. Finalize approximately in reverse acquisition order while respecting product lifecycle:

```text
stop acceptance workload
attempt normal Podlaz disconnect if acceptance session active
wait bounded product convergence
restore Wi-Fi connection if harness left it down
remove exact synthetic fixture
verify ordinary network
restore changed autostart policy when phase permits
write failure report
```

Cleanup activity never changes a scenario's FAIL to PASS.

A cleanup failure itself makes the overall run fail.

## Requirements/environment observation

Sanitized requirements evidence includes non-identifying facts useful for future support policy:

- distribution/version;
- kernel version family;
- architecture;
- logical CPU count and safe model class;
- total RAM/swap;
- systemd version;
- NetworkManager version;
- nft/iproute2 versions;
- Podlaz package version;
- availability of `/dev/net/tun`, unified cgroup v2, systemd-resolved, NM Wi-Fi, RTC wake, and suspend;
- measured peak/steady Podlaz resources.

The harness does not update the system when versions differ from preferred test hosts. Missing optional host capabilities become explicit skips/observations; fundamental TUN prerequisites fail preflight.

## Reports

### `summary.txt`

Short, sanitized and shareable:

```text
Release package: podlaz <version> (<arch>)
Result: PASS|FAIL

package_install                         PASS
autostart_baseline_preserved            PASS
initial_tun_connect                     PASS
daemon_restart_continuity               PASS
package_reinstall_continuity            PASS
foreign_coexistence                     PASS
safe_route_netlink_churn                PASS
wifi_reconnect                          PASS|SKIP
suspend_resume                          PASS|SKIP
resource_soak_60m                       PASS
post_disconnect_cleanup                 PASS
reconnect_resource_nonaccumulation      PASS
real_boot_autostart_off                 PASS
real_boot_autostart_on                  PASS
same_boot_disconnect_no_retry           PASS
original_autostart_policy_restored      PASS
final_ordinary_network                  PASS

Resource observations:
  active samples: <count>
  daemon RSS median/peak: <values>
  Xray RSS median/peak: <values>
  cgroup memory peak: <value>
  daemon FD start/end delta: <value>
  Xray FD start/end delta: <value>
  post-disconnect retained deltas: <values>
```

### `report.json`

Versioned structural scenario outcomes and numeric resource summaries for comparing runs.

### `requirements-observation.json`

Versioned sanitized environment/resource envelope intended for later aggregation. Measurements are explicitly observations, not universal requirements.

## Harness CLI

Minimum forms:

```text
sudo ./scripts/acceptance/release-laptop.sh <release.deb> [--profile <profile-id>]
sudo ./scripts/acceptance/release-laptop.sh --resume
```

Useful test-tool options may include:

```text
--artifact-dir <path>
--soak-minutes <N>       # bounded developer override; default 60
--skip-wifi-churn
--skip-suspend
--no-reboot-phases
```

Default release qualification is one-hour soak + disruptive stages where supported + two manual reboots.

These are test tooling options, not Podlaz product settings.

## Implementation boundaries

Prefer reuse over duplication:

- reuse safe conventions from `scripts/e2e/lib/e2e.sh`;
- reuse resource attribution under `scripts/e2e/lib/tun_soak_*` rather than implementing another procfs/cgroup parser;
- reuse installed product status/doctor/lifecycle commands;
- reuse structural redaction scanning;
- do not call existing dev-package build functions.

Keep explicit boundaries between:

1. package/product lifecycle operations;
2. synthetic fixture allocation/ownership;
3. Wi-Fi/suspend host operations;
4. resource sampling/report generation;
5. reboot checkpoint management.

The user-facing entry point remains one shell script even if narrow internal helpers are reused.

## TDD / deterministic contract tests

### Package safety

Prove:

- `.deb` package/name/arch/version validation;
- no Go/build command in production harness;
- no apt/apt-get update/install/upgrade/fix-broken command;
- `dpkg -i` targets only supplied release package;
- dependency failure does not trigger repair.

### User/profile boundary

Prove:

- original `SUDO_USER` is resolved and Podlaz CLI runs as that user;
- ambiguous profile selection fails before networking mutation;
- profile validation uses installed release client;
- active/unknown initial lifecycle fails before package/network mutation where observable;
- original autostart is captured before package mutation.

### Exact fixture ownership

Prove:

- occupied identities are never adopted;
- cleanup removes exact created tuples only;
- no route/rule/ruleset flush exists;
- pre-existing foreign objects survive deterministic models;
- ambiguous fixture drift fails closed.

### Self-healing scenarios

Prove script orchestration contains no second connect for daemon/package continuation and no `recover --execute`/NetworkManager restart/resolved restart in Wi-Fi/suspend success paths.

A scenario failure remains failed even if final cleanup later restores networking.

### Resource sampling

Prove exact daemon/Xray attribution, default one-hour schedule, sanitized aggregation, cleanup/reconnect comparisons, and observation-vs-requirement wording.

### Reboot/checkpoint safety

Prove:

- strict checkpoint schema/permissions;
- `--resume` rejects absent/stale/corrupt/ambiguous state;
- every reboot phase requires a new `boot_id`;
- no test systemd unit/cron/login hook is created;
- autostart restoration uses product CLI;
- no synthetic network fixture survives into reboot phases.

### Privacy

Prove documentation-only repository fixtures, shareable report redaction, private evidence permissions, and no automatic upload.

## Validation before merge

Run relevant repository checks:

```text
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
shellcheck <new/changed shell files>
```

Hosted CI cannot fully execute Wi-Fi, suspend, root networking, and real reboot phases. Deterministic tests therefore enforce safety/orchestration without root.

A full local run against an actual release `.deb` is release evidence produced by this tool, not a prerequisite for merging the harness implementation itself.

## Success criteria

Implementation is complete when:

- one command qualifies the supplied release package without building Podlaz or updating the OS;
- lifecycle, daemon/package replacement, coexistence, churn, Wi-Fi, suspend, cleanup, reconnect, and one-hour resources are coordinated safely;
- two manual reboots prove real autostart off/on behavior without a boot hook;
- explicit disconnect after successful autostart does not cause same-boot reconnect after daemon restart;
- harness-created state is exactly removed and ordinary networking is verified;
- original autostart policy is restored through normal product API;
- resource use is measured sufficiently for later requirement analysis;
- failures are not hidden by manual product/network repair;
- sanitized reports are shareable while raw evidence remains local/private;
- deterministic tests enforce no-build, no-system-update, exact ownership, redaction, checkpoint, and cleanup contracts.
