# Release Laptop Acceptance Harness Design

Status: approved design for local release qualification on a maintainer laptop.

## Goal

Provide one release-oriented acceptance harness that can exercise the installed Podlaz Debian package on a real laptop as deeply as practical without requiring a dedicated self-hosted E2E server.

The harness is not a replacement implementation of Podlaz networking. It is a local product-validation orchestrator over the already implemented lifecycle, package, networking, privacy, reconciliation, and autostart contracts from Issues #259-#263.

The primary workflow is:

```text
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<version>_linux_<arch>.deb
```

The harness performs a long first phase, then uses two explicit user-driven machine reboots to validate real boot-autostart behavior. After each reboot the user resumes the same acceptance run with:

```text
sudo ./scripts/acceptance/release-laptop.sh --resume
```

No temporary boot service or auto-resume unit is installed. The user remains in control of every reboot.

## Product validation scope

The harness should cover, in one coordinated run where host capabilities permit:

1. release package validation and installation/reinstallation;
2. clean TUN connect and protected data-plane verification;
3. daemon restart continuity while connected;
4. package replacement/reinstall continuity while connected;
5. collision-safe coexistence with unrelated synthetic TUN/routing/DNS/firewall state;
6. safe route/netlink churn while Podlaz is active;
7. controlled NetworkManager Wi-Fi reconnect and DHCP/uplink re-observation on a suitable local Wi-Fi laptop;
8. one real suspend/resume cycle while the protected session is active;
9. one-hour resource-soak observation with periodic traffic and diagnostics;
10. disconnect cleanup and ordinary-network restoration;
11. reconnect and second-session resource comparison;
12. a real reboot with autostart disabled;
13. a real reboot with autostart enabled;
14. same-boot no-retry behavior after explicit disconnect from an autostarted session;
15. restoration of test-created host state and the user's pre-test autostart policy;
16. generation of a detailed private report plus a sanitized summary suitable for sharing.

The harness should collect enough resource and environment evidence to support later, evidence-based system-requirement decisions. One laptop run is observation, not by itself a universal minimum-requirements proof.

## Hard safety constraints

### Release package only

The harness must test only the `.deb` passed by the user.

It must not:

- run `go build`, `go test`, `go install`, `make`, `goreleaser`, or another Podlaz build path;
- substitute binaries from the repository working tree;
- install a development package generated from source;
- modify the package to make the test pass.

The installed binaries under `/usr/bin`, `/usr/lib/podlaz`, and the packaged systemd unit are the product under test.

Before installation the harness validates at least:

- input path exists and is a regular file;
- package name is `podlaz`;
- package architecture matches `dpkg --print-architecture`;
- package version is readable;
- SHA-256 is recorded;
- if a sibling `SHA256SUMS` file is present and contains the package entry, the digest is verified against it.

The release package remains installed after acceptance. The harness does not downgrade to the previously installed Podlaz version at the end.

### No operating-system updates or dependency repair

The harness must not update the laptop or opportunistically change its software environment.

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

It must not install or update Go, Xray, NetworkManager, systemd, nftables, iproute2, Python, curl, or other tools.

The only package mutation performed by the harness is installation/reinstallation of the exact supplied Podlaz `.deb` via `dpkg -i`.

If dependencies are unsatisfied, the harness fails with a clear message and does not attempt to repair the system.

### No generic network repair

A failed product scenario must remain a failed product scenario. The acceptance harness must not make it pass by repairing Podlaz or the host out-of-band.

The success path must never invoke:

```text
podlaz recover --execute
ip route flush ...
ip rule flush ...
nft flush ruleset
systemctl restart NetworkManager
systemctl restart systemd-resolved
```

The harness may use normal product operations such as `podlaz connect`, `podlaz disconnect`, `podlaz status`, `podlaz doctor --tun`, and `podlaz autostart ...`.

If Podlaz fails to self-heal after a scenario that is expected to self-heal, the harness records a failure. Cleanup may still make a bounded best effort to return the laptop to a usable state, but cleanup activity is clearly separated from the scenario verdict and cannot rewrite a failure to PASS.

### Exact ownership for test fixtures

The harness may create synthetic unrelated network state, but it owns only objects it created and proved free immediately before creation.

No cleanup operation may delete a routing table, rule, interface, address, DNS link, or nftables object merely because its numeric/name value resembles a test candidate.

The harness must not use `ip route flush table N` for a table that was not proven fully test-owned.

For every fixture object it records enough private identity to delete exactly that object. If exact cleanup authority is lost or current live state no longer matches the fixture identity, cleanup fails closed and reports the ambiguity rather than deleting a candidate object.

### Local interactive laptop only for disruptive stages

Wi-Fi reconnect and suspend/resume are allowed because this harness targets a maintainer laptop, but they must not be run through an SSH session.

If an SSH session is detected, disruptive stages are skipped before mutation and reported as `SKIP_HOST_CAPABILITY` / `SKIP_REMOTE_SESSION` rather than attempted remotely.

## User identity and profile boundary

The script itself is started with `sudo`, but normal Podlaz CLI operations must run as the original user from `SUDO_USER`, not as root.

The harness derives the original user's home and user-owned Podlaz state path from the account database rather than assuming root's environment.

The harness never imports a profile URI as part of release acceptance. It uses a profile already present in the original user's Podlaz profile store.

Profile selection semantics:

- `--profile <profile-id>` explicitly selects the profile and is the preferred unambiguous form;
- without `--profile`, the harness may auto-select only when exactly one existing profile successfully passes `podlaz profile validate --mode tun`;
- zero valid TUN profiles is a preflight failure;
- more than one valid TUN profile is a preflight failure with guidance to rerun using `--profile`.

Profile IDs, profile names, endpoints, user identities, subscription URLs, and generated runtime configuration are private evidence and never appear in the sanitized report.

## Start-state preconditions

The first phase must not silently take over an unknown current Podlaz lifecycle.

Before any package or network mutation:

- Podlaz must be conclusively `Disconnected`/inactive;
- no cleanup-required current Network Session may be present;
- ordinary host networking must have a usable IPv4 path, DNS, and HTTPS baseline;
- the selected profile must validate for TUN mode;
- the host must provide required core tools already installed;
- the supplied `.deb` must pass package preflight.

If Podlaz is active or current lifecycle state is ambiguous, the harness exits before mutation and asks the user to reach a clean disconnected state first. It does not guess how to replace an already-active session.

This makes failure cleanup and resource baselines materially more trustworthy.

## Persistent acceptance checkpoint

Real reboot validation requires a small root-owned checkpoint that survives reboot.

Use a dedicated acceptance-only directory outside Podlaz daemon runtime/state, for example:

```text
/var/lib/podlaz-release-acceptance/
```

Requirements:

- directory mode `0700`, root-owned;
- checkpoint files mode `0600`;
- versioned schema;
- atomic write/replace with bounded content;
- contains only information required to resume the acceptance run;
- no automatic service, timer, cron entry, login hook, or systemd unit;
- deleted after successful finalization or explicit abort cleanup.

The checkpoint contains, at minimum:

- acceptance schema version and run ID;
- current phase;
- package name/version/architecture/SHA-256;
- original and expected Linux `boot_id` transitions;
- selected user identity and selected profile ID, private only;
- sanitized result state accumulated so far;
- original autostart policy restoration material;
- user report directory path;
- no host networking fixture survives into reboot phases.

`--resume` is valid only when exactly one supported active checkpoint exists. It never accepts a new package/profile and never starts a different test run.

## Evidence layout and privacy

The harness creates a private report directory under the original user's state area, for example:

```text
~/.local/state/podlaz/release-acceptance/<run-id>/
```

The exact root is derived from the original user's XDG/default state conventions rather than root's `$HOME`.

Layout is conceptually:

```text
<run>/
  summary.txt
  report.json
  requirements-observation.json
  private/
    raw command output
    process identities
    transaction/resource observations
    network baseline snapshots
    soak samples
    diagnostic output
```

`private/` is mode `0700`; private files are `0600`.

`summary.txt`, `report.json`, and `requirements-observation.json` are generated from sanitized structural data only. They must not contain:

- profile IDs/names;
- VPN endpoints/domains/IPs;
- user identity/UUID credentials;
- public IP returned by an egress service;
- local/private IP addresses;
- Wi-Fi SSID/BSSID;
- physical interface names where they identify the user's host;
- NetworkManager UUIDs;
- transaction IDs;
- generated runtime configuration content;
- hostname or machine-specific opaque identifiers.

The harness never uploads evidence automatically.

## Phase 1: package and baseline qualification

### Package install/update

Record the currently installed Podlaz version, if any, for report context only.

Install the supplied release package with:

```text
dpkg -i <exact-input-deb>
```

Then prove:

- installed package version equals the input package version;
- `/usr/bin/podlaz` and `/usr/bin/podlazd` come from the installed package;
- `podlazd.service` is loadable and reaches the packaged daemon socket;
- the daemon PID is valid;
- no dependency repair command was used.

If the candidate was already installed, this is a reinstall. If an older package was present, this also exercises the normal package upgrade path while disconnected.

## Phase 2: initial protected connection

Connect with the selected profile in TUN mode using the installed CLI as the original user.

Require bounded convergence to:

```text
Status: Connected
```

and current TUN health equivalent to `verified` through the available daemon/operator evidence.

Protected data-plane validation includes, where supported by current commands:

- exact active TUN/transaction lifecycle state;
- Privacy Envelope presence/authority without exposing the table identity publicly;
- DNS resolution through the active protected session;
- normal system DNS resolution;
- IPv4 TCP/TLS/HTTPS egress;
- `podlaz doctor --tun` or equivalent read-only detailed health evidence;
- no confirmed cleanup-required state.

Probe response bodies such as the public egress IP are discarded rather than stored in sanitized artifacts.

## Phase 3: daemon replacement continuity

While connected and verified:

1. record the exact current daemon PID and current private session identity;
2. run the packaged `systemctl restart podlazd.service`;
3. prove the daemon PID changed;
4. do not invoke a second `podlaz connect`;
5. require the protected session to return to `Connected`/verified;
6. prove Privacy Envelope/session protection authority did not disappear during the expected continuation path;
7. require protected DNS/HTTPS to work again;
8. require no manual recovery command.

Failure is a product failure even if a later explicit reconnect would work.

## Phase 4: package replacement continuity

While the VPN remains connected and verified, reinstall the exact same supplied `.deb` with `dpkg -i`.

This validates package-maintainer-script and daemon-replacement behavior without compiling another candidate.

Require:

- package command succeeds;
- daemon PID changes as part of package lifecycle;
- no test-side `systemctl start/restart` occurs before the package-continuity verdict;
- no second `podlaz connect` occurs;
- current Network Session returns to protected verified state;
- protected DNS/HTTPS work;
- exact recovery authority remains present if convergence is not complete.

## Phase 5: synthetic foreign-state coexistence

Create a product-neutral synthetic baseline while Podlaz is active, using documentation-only network values and exact test ownership.

The fixture may include:

- one unrelated TUN device;
- one unrelated point address;
- one private numeric routing table with one exact documentation-prefix route;
- one or two selective policy rules;
- one unrelated nftables table;
- one dummy resolved link with documentation-only DNS/domain state when systemd-resolved permits it safely.

Selection rules:

- read-only inventory first;
- prefer historical Podlaz candidate identities only when they are actually free and using them improves collision coverage;
- if a historical identity is already occupied, preserve it and treat the existing occupant as foreign baseline rather than touching it;
- otherwise allocate a verified-free test identity;
- perform a live collision check immediately before every fixture mutation;
- record which objects were created by this run.

The fixture must not carry the host's default route or ordinary user traffic.

After creation, require Podlaz to remain or return verified. Then perform safe fixture churn such as exact route replacement and fixture link down/up, requiring fresh Podlaz observation without destructive foreign cleanup.

Before later disconnect/reconnect coexistence validation, capture a structural hash/normalized manifest of the fixture. After Podlaz disconnect and after a fresh Podlaz connect while the fixture remains present, prove the unrelated fixture is still structurally unchanged except for the specific churn the harness itself intentionally performed.

## Phase 6: Wi-Fi / DHCP churn

When the current underlying default uplink is a NetworkManager-managed Wi-Fi connection and the session is local, perform one controlled reconnect.

Before mutation privately record:

- active NetworkManager connection UUID;
- Wi-Fi interface identity;
- IPv4 address/gateway/default-route fingerprint;
- relevant NetworkManager identity used by Podlaz revalidation.

Then:

```text
nmcli connection down <exact-original-uuid>
wait for down transition
nmcli connection up <exact-original-uuid>
```

Do not restart NetworkManager or systemd-resolved.

After reconnection:

- require the original connection to be restored or fail cleanup explicitly;
- require an ordinary default route to return;
- require Podlaz to reach verified again automatically;
- require protected DNS/HTTPS;
- record whether DHCP/address/gateway identity actually changed.

If the same lease/identity is returned, the report says the Wi-Fi reconnect was exercised but a changed-DHCP-identity case was not observed. The harness must not fabricate a DHCP identity change or use an unrelated DHCP client to force one.

If no suitable NetworkManager Wi-Fi uplink exists, report this stage as host-capability skip rather than failing the product.

## Phase 7: one-hour resource soak

Resource observation uses the existing process/cgroup attribution concepts from the TUN resource-soak infrastructure, adapted for the installed release package and a normal laptop.

Default timing:

```text
warm-up:                   5 minutes
primary observation:      60 minutes
sample interval:           60 seconds
read-only doctor cadence: 10 minutes
post-disconnect settle:    bounded 1-2 minutes
reconnect observation:     5-10 minutes
```

The one-hour primary window includes controlled networking events rather than running only an idle VPN.

Suggested placement:

```text
00-05 min  warm-up
05-20 min  steady protected traffic/resource samples
~20 min    synthetic foreign route/netlink churn
~30 min    Wi-Fi/NetworkManager reconnect when supported
~40 min    real suspend/resume
40-60 min  post-resume stability/resource samples
```

Every sample attributes resources to exact `podlazd` and its exact supervised Xray child, not to arbitrary same-name processes.

Collect at least, where the kernel exposes them reliably:

For `podlazd`:

- RSS;
- PSS when readable;
- CPU time / CPU utilization derivable from successive samples;
- threads;
- tasks;
- total file descriptors;
- regular-file FDs;
- pipe FDs;
- anon-inode FDs;
- socket FDs;
- TCP listen/established/other sockets;
- UDP connected/unconnected/other sockets;
- UNIX sockets;
- unclassified socket FDs.

For Xray:

- the same process-level metrics where attributable.

For `podlazd.service` cgroup:

- `memory.current`;
- `memory.peak` if available;
- `pids.current`;
- cgroup CPU usage/counters where available.

Lifecycle context alongside samples includes only sanitized state/classification in shareable output:

- connected/reconnecting/verified state;
- whether a daemon replacement occurred;
- whether an Xray generation replacement occurred;
- transaction count as a structural number;
- Network Session authority present/absent;
- Privacy Envelope authority present/absent.

### Traffic workload

Generate a bounded low-volume workload sufficient to keep real data-plane activity present without becoming a benchmark:

- periodic DNS resolution;
- periodic small HTTPS request with response body discarded;
- periodic product status;
- read-only `doctor --tun` every ten minutes.

The harness is not a throughput or latency benchmark and does not saturate the user's network.

### Resource conclusions

The one-hour run records:

- min/median/max/last values;
- growth delta from warm-up to end;
- simple time trend/slope where statistically meaningful;
- post-disconnect retained delta against the warmed inactive baseline;
- reconnect delta between first and second session;
- peak cgroup memory;
- peak process RSS/PSS;
- peak tasks/threads/FDs/sockets.

Existing conservative lifecycle leak limits may be reused as warnings or acceptance checks where they already represent tested Podlaz invariants, especially post-disconnect and reconnect non-accumulation.

The harness must not derive a universal minimum RAM/CPU requirement from one machine. Instead it writes `requirements-observation.json` containing the tested environment class and measured high-water marks so multiple runs can later support a documented minimum/recommended requirement.

## Phase 8: suspend/resume

At approximately forty minutes into the active soak, perform one real timed suspend when supported.

Preferred mechanism is a bounded RTC wake such as `rtcwake` with a short sleep interval, approximately 60-120 seconds.

Before suspend:

- prove VPN is protected/verified;
- record the current private Network Session/daemon/Xray resource identities;
- flush private report files to disk;
- ensure no synthetic fixture cleanup is in progress.

After resume:

- confirm the system actually crossed a meaningful suspend interval;
- wait for NetworkManager/uplink convergence without restarting services;
- require Podlaz to move through its normal revalidation/reconciliation path and return verified automatically;
- require protected DNS/HTTPS;
- collect immediate and later resource samples to detect retained workers/sockets after resume;
- record daemon/Xray replacement if it occurred naturally.

If the host cannot provide a safe timed suspend/wake mechanism, report a host-capability skip. If suspend occurs but Podlaz does not recover automatically, record a product failure.

## Phase 9: disconnect, cleanup proof, and reconnect

After the one-hour active window:

1. invoke normal `podlaz disconnect`;
2. require clean product `Disconnected`/inactive state;
3. require exact Podlaz Network Session/Privacy Envelope cleanup to converge;
4. require ordinary host DNS/TCP/HTTPS to work;
5. verify the synthetic foreign fixture is still present and structurally unchanged;
6. collect a post-disconnect daemon/cgroup boundary sample after a bounded settle period;
7. compare against the warmed inactive baseline for retained FDs/tasks/sockets/memory.

Then reconnect the selected profile while the foreign fixture still exists.

Require:

- collision-safe new session allocation;
- protected verified data plane;
- foreign fixture preserved;
- short 5-10 minute second-session resource sample window;
- no unexpected accumulation compared with the first session.

Disconnect again and repeat the clean boundary checks.

Only after Podlaz is cleanly inactive may the harness remove its own synthetic fixture.

## Fixture cleanup

Cleanup removes only objects proven to have been created by this exact run.

Examples:

```text
ip route del <exact-destination> table <exact-test-table>
ip rule del <full-exact-selector/table/priority tuple>
ip address del <exact-cidr> dev <exact-test-interface>
ip link del <exact-test-interface after link-kind/identity recheck>
nft delete table <exact-test-family> <exact-test-table after exact identity check>
resolvectl revert <exact-test-dummy-link> before deleting that exact dummy link
```

Never use table-wide or ruleset-wide flushing as a convenience cleanup primitive.

If an exact object is already absent, treat absence as idempotent only when the observation is authoritative. If an object has drifted to an ambiguous identity, preserve it and report manual follow-up rather than deleting by resemblance.

## Phase 10: reboot checkpoint A — autostart disabled

All synthetic network fixtures are removed before reboot testing begins.

The harness captures the user's current autostart policy privately before changing it. Restoration is performed later through the normal `podlaz autostart` CLI, never by overwriting the daemon manifest file.

If the original autostart policy is enabled, private checkpoint material may read the daemon-owned manifest sufficiently to retain the original profile ID and mode. Before changing policy the harness proves that profile still exists in the original user's profile store and can be used to restore policy later. If restoration material cannot be proven, the harness aborts before changing autostart.

Prepare reboot A:

- ensure Podlaz is cleanly disconnected;
- disable future-boot autostart through the normal CLI;
- confirm `Autostart: Disabled`;
- record current `boot_id`;
- persist checkpoint phase `awaiting-autostart-off-reboot`;
- print an explicit instruction to reboot the laptop and rerun with `--resume`.

The harness itself does not invoke `reboot` and does not create an automatic continuation service.

## Phase 11: resume after reboot A

On `--resume`:

- load and strictly validate the checkpoint;
- require the current `boot_id` to differ from the recorded one;
- prove the supplied release package version is still installed;
- prove packaged `podlazd` started normally according to installed service behavior;
- require Podlaz to remain conclusively disconnected;
- require no fresh automatic VPN connection;
- require ordinary host DNS/HTTPS;
- require autostart policy still disabled.

Then configure the selected profile for future-boot TUN autostart through the normal CLI, confirm `Enabled for next boot`, record current `boot_id`, persist phase `awaiting-autostart-on-reboot`, and ask the user to reboot again.

## Phase 12: resume after reboot B

On the second `--resume`:

- require another new `boot_id`;
- require the same supplied release package version installed;
- wait boundedly for packaged daemon startup and boot network readiness;
- require one automatic TUN lifecycle to reach `Connected`/verified;
- require protected DNS/HTTPS;
- privately inspect current boot-attempt authority enough to prove one logical attempt was consumed and no second attempt was admitted;
- record daemon/session identity.

Then restart `podlazd.service` once and require:

- daemon PID changed;
- the same current-boot Network Session returned verified;
- automatic-connect attempt authority was not replaced or duplicated;
- no manual `connect` was used.

Finally invoke explicit `podlaz disconnect`, wait for clean inactive state, restart the daemon again, and prove it remains disconnected in the same boot. This is the real-machine no-same-boot-retry proof.

## Restore original user policy and final host state

At the end of the final resume phase:

1. restore the user's original autostart policy using normal CLI operations;
2. if original policy was disabled, leave it disabled;
3. if original policy was enabled, re-enable the exact original profile ID/mode proven in the private checkpoint;
4. confirm the resulting policy matches the saved structural expectation;
5. ensure Podlaz is cleanly disconnected unless the future product contract explicitly changes this acceptance design;
6. restore the daemon service active/inactive runtime state when doing so is safe and the initial state was conclusively known;
7. confirm ordinary networking works;
8. confirm no synthetic TUN/routing/DNS/nftables fixture remains;
9. remove the root-owned persistent acceptance checkpoint directory;
10. write final sanitized reports.

The tested release package remains installed.

If original autostart restoration fails, final result is failure and the checkpoint is retained only as long as needed to provide private recovery information. The harness must not silently claim success while user policy differs from the captured baseline.

## Failure handling and finalizer semantics

Each first-phase mutation has a corresponding exact finalizer action registered only after the mutation succeeds.

Finalization order should generally be the reverse of acquisition while respecting Podlaz privacy/lifecycle boundaries:

```text
stop active acceptance workload
attempt normal Podlaz disconnect if an acceptance session is active
wait for bounded Podlaz convergence
restore Wi-Fi connection if the harness left it down
remove exact synthetic fixture objects
verify ordinary network
restore test-changed autostart policy when phase permits
write failure report
```

Do not remove Podlaz Privacy Envelope manually in cleanup. Product-owned networking is cleaned through the product lifecycle; if that fails, preserve the failure evidence instead of masking it with direct nft/ip cleanup.

The harness may clean only its own unrelated fixture directly.

A cleanup failure changes the overall result to FAIL even if the original scenario passed.

## System/environment observations

The sanitized requirements observation records non-identifying environment facts useful for future support policy, such as:

- distribution family/version;
- kernel release family/version;
- architecture;
- CPU logical count and non-identifying model class where safe;
- total RAM and swap availability;
- systemd version;
- NetworkManager version;
- nft/iproute2 versions;
- packaged Podlaz version;
- whether `/dev/net/tun`, unified cgroup v2, systemd-resolved, NetworkManager Wi-Fi, RTC wake, and suspend were available;
- peak/steady Podlaz resource measurements.

No automatic system update is performed when the host differs from preferred test versions. Unsupported/missing host capabilities are reported as observation or stage skip unless they are fundamental requirements for the selected TUN scenario.

## Reports

### Sanitized summary

`summary.txt` should be short and shareable, for example:

```text
Release package: podlaz <version> (<architecture>)
Result: PASS|FAIL

package_install                         PASS
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
  daemon RSS median/peak: <sanitized numeric values>
  Xray RSS median/peak: <values>
  cgroup memory peak: <value>
  daemon FD start/end delta: <value>
  Xray FD start/end delta: <value>
  post-disconnect retained deltas: <structural values>
```

### Structured report

`report.json` contains versioned structured outcomes and numeric resource summaries suitable for comparing runs.

### Requirements observation

`requirements-observation.json` contains a versioned, sanitized environment/resource envelope intended for later aggregation across machines/releases. It explicitly labels measurements as observations rather than universal requirements.

## CLI surface of the harness

Minimum supported forms:

```text
sudo ./scripts/acceptance/release-laptop.sh <release.deb> [--profile <profile-id>]
sudo ./scripts/acceptance/release-laptop.sh --resume
```

Useful non-product options may include:

```text
--artifact-dir <path>     override local report location
--soak-minutes <N>        developer override with a bounded minimum; default 60
--skip-wifi-churn         skip disruptive Wi-Fi reconnect explicitly
--skip-suspend            skip suspend/resume explicitly
--no-reboot-phases        finish after phase 1-9 and report boot checks not run
```

Default release qualification remains the full one-hour + suspend/Wi-Fi + two-manual-reboot path where host capabilities allow it.

The harness options are test tooling, not Podlaz product settings.

## Implementation boundaries

Prefer reuse over duplication:

- reuse existing `scripts/e2e/lib/e2e.sh` conventions where safe for local execution;
- reuse existing resource attribution code under `scripts/e2e/lib/tun_soak_*` rather than implementing a second procfs/cgroup parser;
- reuse current package/lifecycle status and diagnostic commands;
- reuse existing structural redaction scanning where applicable;
- do not call dev-package build functions from existing E2E harnesses.

The release-laptop orchestrator must have a clear boundary between:

1. package/product lifecycle operations;
2. exact synthetic fixture ownership;
3. disruptive host operations such as Wi-Fi/suspend;
4. resource sampling/report generation;
5. reboot checkpoint management.

The public entry point remains one shell script even if focused internal helpers are reused.

## Test strategy

Implementation follows TDD for new behavior.

Deterministic tests must prove at least:

### Package safety

- script requires a `.deb` and validates package name/architecture/version;
- production script contains no Go/build command;
- production script contains no apt/apt-get update/install/upgrade/fix-broken command;
- `dpkg -i` targets only the user-supplied package path;
- package dependency failure does not trigger dependency installation.

### User boundary

- root invocation resolves `SUDO_USER` and runs Podlaz profile/lifecycle commands as that user;
- ambiguous profile auto-selection fails before mutation;
- selected profile must validate for TUN;
- active/unknown initial Podlaz state fails before package/network mutation.

### Exact fixture ownership

- fixture allocator never adopts occupied address/table/rule/nft identities;
- cleanup deletes exact created tuples only;
- no `ip route flush`/`ip rule flush`/`nft flush ruleset` exists;
- pre-existing foreign objects remain untouched in deterministic fixture models;
- cleanup of drifted/ambiguous fixture identity fails closed.

### Product self-healing contract

- daemon restart and package reinstall scenarios contain no second `connect` in their success path;
- Wi-Fi/suspend success paths contain no `recover --execute`, NetworkManager restart, or resolved restart;
- failure remains failure even if finalizer later restores ordinary networking.

### Resource sampling

- exact daemon/Xray attribution is used;
- one-hour default yields the expected sample schedule;
- samples are sanitized before shareable aggregation;
- post-disconnect and reconnect comparisons are generated;
- one machine's observation is not labeled a universal requirement.

### Checkpoint/reboot safety

- checkpoint schema/permissions are strict;
- `--resume` rejects absent, stale, corrupt, or ambiguous checkpoint state;
- each reboot phase requires a changed Linux `boot_id`;
- no test systemd unit/cron/login hook is created;
- autostart is restored through CLI semantics;
- no synthetic networking fixture survives into a reboot phase.

### Privacy

- documentation/example values only in repository fixtures;
- safe reports reject private profile/network/host identifiers;
- raw private evidence is never automatically uploaded;
- checkpoint/private evidence modes remain restrictive.

## Validation before merge

At minimum run the normal repository checks applicable to a script/test/docs change:

```text
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
shellcheck on the new/changed shell files
```

The laptop harness itself cannot be fully executed by hosted CI because it requires root networking, NetworkManager Wi-Fi, suspend, and real reboot boundaries. Deterministic contract tests must therefore cover safety and orchestration invariants without root.

A full local run against an actual release `.deb` is release evidence, not a prerequisite for merging the harness implementation itself when the maintainer is implementing the tool specifically to make that evidence possible.

## Success criteria

The design is implemented when:

- one command validates the supplied installed release package without building Podlaz or updating the OS;
- normal lifecycle, daemon replacement, package replacement, coexistence, network churn, Wi-Fi reconnect, suspend/resume, cleanup, reconnect, and one-hour resource observation are coordinated safely;
- two explicit user reboots prove real autostart-off and autostart-on behavior without any temporary boot hook;
- same-boot explicit disconnect after successful autostart does not auto-reconnect after daemon restart;
- all harness-created host objects are removed exactly and ordinary networking is verified at the end;
- original autostart policy is restored through normal product APIs;
- resource use is measured sufficiently to support later requirement analysis;
- failures are not hidden by manual Podlaz/network repair;
- sanitized reports are safe to share while raw evidence remains private on the laptop;
- deterministic tests enforce the no-build, no-system-update, exact-ownership, redaction, checkpoint, and cleanup contracts.
