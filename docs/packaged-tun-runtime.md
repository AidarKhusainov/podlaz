# Packaged TUN runtime helper

The Debian package is responsible for making the current TUN architecture self-contained for end users. A clean package install must not require users to install Xray manually before running TUN mode.

## Bundled helper

Packaged helper inputs are pinned in `packaging/runtime-helpers.env`.

The package build installs:

```text
/usr/lib/podlaz/xray
/usr/share/doc/podlaz/third-party/xray-LICENSE
```

Xray release archives are downloaded from the pinned release asset names and verified with pinned SHA-256 checksums before packaging.

The packaged systemd unit points the daemon at the bundled helper with an absolute path:

```ini
Environment=PODLAZ_XRAY_PATH=/usr/lib/podlaz/xray
```

TUN mode uses Xray packet ingestion through its native `tun` inbound. Xray
owns creation and lifetime of `podlaz0`; the pinned helper deliberately does not
assign the product OS address. Before the first host-network mutation, `podlazd`
selects and persists the exact collision-free session IPv4 `/32`, routing table,
and policy-rule priorities. It owns only those exact transaction-backed
identities plus the surrounding DNS, nftables, transaction, rollback, and
recovery state.

## Native TUN privilege contract

The pinned Linux Xray TUN implementation opens `/dev/net/tun`, creates the named TUN interface, sets MTU through netlink, and brings the link up. Linux requires `CAP_NET_ADMIN` for interface configuration, firewall administration, and routing administration.

The packaged contract is therefore explicit:

- `podlazd` runs as `root:podlaz` with `CAP_NET_ADMIN` in the bounding set for podlaz-owned routes, DNS, nftables, and recovery operations;
- proxy-only Xray runs as the dedicated `podlaz-xray:podlaz-xray` identity without extra ambient capabilities;
- native TUN Xray also runs as `podlaz-xray:podlaz-xray`, but receives only `CAP_NET_ADMIN` as an ambient capability so it can create and configure the Xray-owned `podlaz0` link;
- the unit keeps `NoNewPrivileges=yes` and does not rely on file capabilities or a setuid helper;
- VM or self-hosted validation must prove the actual Linux capability handoff with the packaged unit before the PR leaves draft.

## Pinned Xray TUN schema

The pinned Xray release currently accepts only native TUN packet-ingestion settings in the generated inbound: interface `name`, uppercase `MTU`, and `userLevel`. It does not accept `gateway`, `dns`, lowercase `mtu`, `autoSystemRoutingTable`, or `autoOutboundsInterface` in the pinned `v26.3.27` schema. podlazd therefore must not invent unsupported Xray JSON fields.

Current upstream documentation describes a newer public schema with lowercase `mtu`, `gateway`, `dns`, and automatic routing fields. This PR intentionally follows the pinned source version, not the moving documentation page. If the pinned helper is upgraded, the generated JSON tests and this contract must be updated using source evidence for the new tag.

The deliberate contract is:

- Xray creates and owns the `podlaz0` link lifecycle and packet ingestion;
- Xray applies only the link name and MTU supported by the pinned schema;
- podlazd selects a bounded, collision-free session IPv4 `/32`, numeric routing
  table, and ordered policy-rule priorities from the authoritative read-only host
  baseline, then persists those exact identities before mutation;
- historical `198.18.0.1/32`, table `51820`, and priorities `9999`/`10000` are
  preferred candidates only. Unrelated occupation causes deterministic
  reallocation, not ownership inference or foreign cleanup;
- incomplete authoritative allocation evidence, candidate exhaustion, or lack of
  a concrete safe server bootstrap/data-plane path fails closed before mutation;
- podlazd commits only after exact allocated address/link/routing verification,
  static resolved verification, an uncached interface-scoped IPv4 query, normal
  system resolution, route lookup, routed TCP, and DNS-result route verification
  pass;
- VM or self-hosted validation must provide evidence that this pinned-schema split works on the target Linux hosts before the PR leaves draft.

If a future pinned Xray release adds supported `gateway`/`dns` fields, this contract must be revisited in the issue/PR with Xray schema evidence, generated JSON tests, and VM validation evidence.

## Hard TUN dependencies

Packaged TUN mode relies on these host components and declares them as Debian dependencies:

- `iproute2` for route, policy rule, route verification, and recovery operations around the Xray-owned TUN link;
- `nftables` for firewall and kill-switch state;
- `systemd-resolved` for per-link DNS apply, verify, and rollback;
- `polkitd | policykit-1` for the packaged daemon authorization boundary.

`network-manager` remains optional diagnostics only and is not a hard package dependency. When NetworkManager is present, current-health fingerprinting treats its active-connection inventory as authoritative only when that inventory was successfully inspected; a failed active-connection query is unknown evidence rather than an empty connection identity.

## Preflight contract

Before active Podlaz replacement, opening a TUN transaction, or applying host-networking changes, daemon-side connect checks that:

- the Xray helper resolves to an executable;
- packaged helpers under `/usr/lib/podlaz` match the running helper architecture when ELF metadata can be inspected;
- `ip`, `nft`, and `resolvectl` are available in the daemon execution environment;
- the pinned Xray helper accepts a minimal native `tun` inbound config through `xray test -config`;
- the server bypass resolves to a concrete usable IPv4 route, which may traverse unrelated host routing/TUN state;
- authoritative IPv4 address, route, and policy-rule evidence is sufficient to
  choose a verified-free session allocation without mutating unowned state.

The unsupported-Xray preflight uses a temporary, redaction-safe config with only `protocol: tun` and pinned-schema `name`/`MTU`/`userLevel` settings. It does not use profile-derived runtime config and it runs before active replacement, automatic podlaz-owned recovery, handoff mutation, and transaction creation. Unsupported Xray TUN support must not disconnect active podlaz TUN, run recovery, stop an external VPN connection, or leave a transaction artifact.

For non-interactive connect policies, the daemon recovers only exact durable
Podlaz transaction state that requires cleanup, recollects the authoritative host
snapshot, and allocates the new Network Session around the actual baseline.
Unrelated TUN devices, custom routes/rules, route-only DNS links, NetworkManager
VPN connections, and unrelated nftables state are not blockers merely because
they exist and are never stopped/deleted as a prerequisite to coexistence. The
historical address/table/priorities are reallocated when occupied. A true blocker
exists when exact Podlaz recovery remains incomplete, required allocation evidence
is unknown/malformed, the bounded candidate space is exhausted, or the concrete
server bootstrap/data-plane plan cannot be installed safely without colliding
with or mutating unowned state. `--handoff=ask` stays mutation-free; `stop-known`
does not broaden new-session authority to deactivate a foreign NetworkManager
VPN.

The generated profile runtime config path is recorded in transaction rollback metadata after the transaction is opened and before the daemon starts Xray with the generated config. If the daemon is interrupted after generated config write, recovery knows how to remove the generated config.

Missing or non-executable helpers, missing hard TUN commands, unsupported Xray TUN config, unavailable server bootstrap, and incomplete/exhausted resource allocation are setup/preflight failures. They must fail before route, DNS, nftables, firewall, active-podlaz replacement, or transaction-backed host-network mutation starts.

User-facing CLI output must not present these failures as a daemon crash or raw internal server error. It must state that TUN mode cannot start and that no network changes were applied.

## Transaction and rollback contract

A failed TUN connect is allowed to start Xray before host network apply because
the pinned native Xray TUN implementation owns packet ingestion and creates the
`podlaz0` link. Before Xray or host-network mutation, the transaction already
contains the immutable session allocation in desired state. After Xray start,
podlazd records the link name, ifindex, TUN type, and appearance-after-child
evidence before assigning the exact allocated IPv4 `/32`. The same identity and
allocated CIDR are revalidated before address apply, verification, rollback, and
recovery. Xray start alone is not a committed VPN connection. The connection is committed only after host network apply, host network verify, connectivity verify, and active-state commit all succeed.

The transaction's structured `desired_plan` is durably written before the daemon starts Xray or mutates host networking. During apply, each exact address, route, policy-rule, DNS, and nftables ownership step is validated and atomically persisted immediately after its mutation and before the next resource executor runs. A persistence failure stops the sequence while retaining the already-mutated step in the in-memory rollback boundary. Normal in-process rollback continues to use recorded applied steps. Desired intent never creates route, policy-rule, DNS, or nftables cleanup authority. A `planned` record contributes no host-network cleanup tuples. An `applying` record without durable applied/rollback ownership is preserved as an ambiguous crash window and performs no route, rule, DNS, or nftables mutation. `applied`, `verifying`, and inactive `committed` records clean only their durable applied/rollback multiset. The sole narrow syscall/persistence fallback is the TUN address in `applying`: a previously persisted bound name, ifindex, TUN kind, exact allocated CIDR, and appearance-after-tracked-child proof becomes only a cleanup candidate and must pass fail-closed host identity and address inspection before removal. Desired-only main-table server bypass state is never deleted by assumption.

`desired_plan` records intent and validates the shape of an applied target; it is not proof that podlaz created a route or policy rule. A new-session route/rule add is exclusive: if the allocated identity became occupied after the snapshot, apply fails rather than treating the object as owned. Recovery/legacy idempotent execution may accept an already-present exact object but does not create a new ownership step from that presence alone. Mixed transactions own only the exact objects represented by durable applied steps. For every state that permits durable ownership, rollback route and policy-rule tuples must equal those applied ownership tuples as exact multisets, including duplicate count.

A `planned` transaction is before host-network mutation and follows a non-mutating network path. Desired routes or rules alone contribute no cleanup tuple; a planned record that already contains network applied steps or route/rule rollback tuples is internally inconsistent and is rejected. `applying` is the fail-closed crash window: when a desired route or policy-rule category has no durable applied proof, fallback cannot distinguish a pre-mutation record from a mutation that crashed before ownership persistence and must refuse network cleanup. Later states such as `applied`, `verifying`, and `committed` use the durable applied/rollback multiset without inventing ownership from desired intent.

Low-level address/route/rule/DNS/firewall composition executors do not perform hidden rollback when an apply method returns an error after mutating state. They report the partial non-zero applied `Step` to the transaction persistence sink before the next mutation or before returning the error. The transaction boundary accepts only an exact owner/target match, persists that ownership, and chooses the cleanup timing:

- the direct transaction helper immediately performs one bounded fail-safe rollback before returning;
- the production full-tunnel runner first collects and persists diagnostics, then invokes rollback using the same partial plan.

This is the boundary that guarantees `network-apply` diagnostics precede the first DNS, nftables, route, rule, or TUN rollback command. A child executor that did not mutate state returns a zero `Step`, which is not added to rollback ownership.

When a failed connect successfully rolls back every podlaz-owned mutation it applied, the transaction is first persisted as terminal `rolled_back` and then removed in a separate filesystem operation. A surviving `rolled_back` file is therefore stale lifecycle metadata only: rollback has already relinquished route and policy-rule ownership, and its historical tuples must never authorize another deletion. Cleanup validates only the stale record's schema, owner, and terminal state before removing the metadata after host/process safety checks. A terminal `committed` record remains a restart-recovery candidate because it may represent an interrupted active session after daemon restart. A committed transaction that is proven to be the exact current live lifecycle transaction is not cleanup-required merely because it exists: read-only status and doctor filter its exact transaction candidate, and explicit recovery performs no mutation against it while current health remains valid or revalidation is in progress. Terminal current-health verification failure is handled separately by the exact lifecycle disconnect contract below.

Transaction files in cleanup-required states such as `planned`, `applying`, `applied`, `verifying`, `rolling_back`, or `failed` continue to block connect until automatic or explicit daemon-owned recovery completes. An inactive committed transaction also blocks as a restart-recovery candidate; a proven live committed transaction does not. Invalid or unreadable transaction files are blockers because their ownership and cleanup state cannot be proven safely.

After every connect attempt, disconnect, or recovery execution, the daemon refreshes the startup recovery scan before publishing status/doctor/recover state. A successful rollback or recovery removes its completed transaction candidate, so an immediate subsequent TUN connect observes the reconciled host and persisted state rather than a phantom stale blocker.

Rollback order for failed or disconnected native TUN sessions is:

1. remove podlaz-owned nftables/firewall state;
2. revert podlaz-owned systemd-resolved per-link DNS state;
3. remove exact transaction-owned routes and policy rules for the persisted session allocation;
4. remove the exact daemon-owned allocated TUN address after link-name, ifindex, kind, and ownership revalidation;
5. stop the Xray child process when it was started by the transaction;
6. remove generated runtime config;
7. remove the terminal transaction file only after rollback succeeds.

The installed-package fallback has an additional fail-closed identity and snapshot boundary. Normal daemon recovery may change transaction ownership, so a manifest captured while `podlazd` or an owned Xray can still mutate state is non-authoritative. Fallback first proves `podlazd` inactive and every transaction-owned Xray absent, then atomically creates the authoritative exact route/rule manifest from the final transaction records. If daemon stop, Xray termination, identity inspection, or the post-quiescence snapshot fails, fallback performs no route/rule mutation and preserves generated configuration, transaction metadata, and package executables for a later safe retry. Network cleanup failure after process quiescence also preserves generated configuration and transaction metadata until complete cleanup can be proven, and package purge remains refused until process quiescence is confirmed.

## Current-health revalidation contract

The packaged daemon must not treat a committed TUN transaction as permanent
current-health proof. It publishes current TUN health independently as
`verified`, `revalidating`, `degraded`, or `cleanup-required`, together with a
positive `network_generation` and stable failure classification where applicable.
`revalidating` and `degraded` are transient active states while proof is in
progress or a terminal verification outcome is being handed off; a proved
verification failure or deadline is not allowed to leave an active committed
session indefinitely in `degraded`.

After commit, generation 1 is initialized from a fresh authoritative snapshot and
the exact captured observation is run through the same canonical composition and
connectivity verifier used for later revalidation. It is not published as
`verified` solely because connect-time verification previously passed. This
closes the commit-to-publication TOCTOU window.

The underlying-uplink fingerprint contains the default-route interface and
ifindex, gateway, relevant global IPv4 addresses, authoritative NetworkManager
active-connection identity when NetworkManager is present, and the server-bypass
next hop. Detection of NetworkManager and successful inspection of active
connections are separate snapshot facts: failure of `connection show --active`
is unknown evidence and cannot be treated as an empty connection identity.
Podlaz-owned TUN, policy-routing, DNS and nftables state is excluded from the
fingerprint to avoid self-triggered generation loops.

The daemon subscribes to post-resume logind events and rtnetlink link/address/
route events. Events are hints only. Both event sources follow subscribe-then-
resync semantics: after the initial successful subscription and after every
reconnect, a coalesced `source-resync` schedules a fresh snapshot. Resume and
source-resync reprove the same generation even when the fingerprint is unchanged.

Connect, disconnect, and recovery have mutation priority, but that priority must
not consume network evidence. A trigger consumed while lifecycle mutation is
pending waits for mutation-idle; an in-flight trigger cancelled by mutation is
requeued before cancellation. The post-mutation pass always starts with a fresh
authoritative snapshot. Shutdown cancellation is terminal and does not requeue.

The verification phase of revalidation is strictly read-only. It reuses the
canonical verifier, which requires exact per-link resolved DNS state and exact
podlaz-owned nftables table composition, including chain metadata and ordered
rule cardinality. It does not apply or repair routes, DNS, firewall,
NetworkManager, TUN state, or foreign resources, and it does not expand rollback
authority beyond durable applied/rollback ownership.

When that read-only proof has established the current owned session but required
verification fails or reaches its bounded deadline, the daemon returns a typed
terminal outcome instead of restoring old evidence. After revalidation authority
is released, it first runs the existing bounded redacted pre-rollback diagnostic
pipeline and attempts to persist the report with `rollback_status=pending`, then
invokes the normal transaction-backed lifecycle `Disconnect` under the existing
bounded rollback timeout. Successful cleanup converges to inactive state and
finalizes the report as `completed`. Cleanup failure finalizes it as `failed` and
leaves current health `cleanup-required` with durable ownership available for
recovery. This is fail-safe disposition, not repair: ambiguous observation or
foreign ownership never gains cleanup authority.

`context.Canceled` from an explicit user disconnect/recovery or daemon shutdown is
not a terminal verification outcome. The higher-priority lifecycle operation owns
cleanup, so cancellation does not schedule a recursive second automatic
disconnect.

## DNS verification contract

Before applying DNS, podlaz runs a scoped `resolvectl revert podlaz0` to discard stale podlaz-owned per-link state. An already-missing link, including the exact `No such device` response, is idempotent. Podlaz never restarts `systemd-resolved` automatically and never edits `/etc/resolv.conf`.

The kernel may expose the newly-created `podlaz0` before `systemd-resolved` registers it. DNS apply therefore retries only the transient missing-link response for the `dns`, `domain`, and `default-route` commands for up to roughly two seconds. Permission errors, unsupported commands, and other unexpected failures are not retried.

`systemd-resolved` can expose recently applied per-link settings with a delay. DNS verification therefore polls for up to roughly two seconds before failing on transient missing target link, exact planned DNS-server set, exact `~.` route-only domain set, or `+DefaultRoute`. Extra DNS servers or domains on the owned link fail closed. `Current Scopes` is derived lookup state and is not an apply-success requirement. The target link must appear exactly once; duplicate `podlaz0` sections are ambiguous and fail closed. A foreign route-only DNS owner is tolerated as baseline only when the concrete Podlaz DNS plan can still be installed and verified without mutating that foreign link; otherwise it is a genuine plan/verification conflict.

Rollback uses `resolvectl revert <link>` for the podlaz-owned link. The exact missing-device response is successful idempotent cleanup in runtime rollback, stale cleanup, doctor inspection, and transaction recovery. Other unexpected errors remain failures.

## Diagnostics and logs

Failures during `network-apply`, `network-verify`, connect-time connectivity
verification, or terminal post-commit revalidation run a short bounded redacted
diagnostic subset while the relevant failed state still exists. The latest
report is atomically persisted before the first rollback command when persistence
succeeds. Diagnostic collection and persistence are best-effort: they never
replace the original failure and never prevent the separate bounded rollback
context from running.

For terminal post-commit revalidation specifically, diagnostic capture happens
after read-only revalidation authority is released and before automatic lifecycle
`Disconnect`. The nested verification phase identifies the failed layer, while
`rollback_status` starts as `pending` and is finalized only after normal cleanup.
Explicit user/shutdown cancellation does not enter this terminal diagnostic-plus-
disconnect path because that cancellation is owned by the lifecycle operation
already in progress.

The report keeps `schema_version`, stable `failure_phase`, primary classification, bounded evidence, warnings/errors, safe report path, and `rollback_status`. It is first written with `rollback_status: pending`, then atomically finalized to `completed` or `failed`. After rollback and daemon restart, `doctor --tun` may read this replacement-only report as historical evidence according to the `/run` retention contract.

Daemon connect failures are logged as sanitized phase summaries. The log line includes the requested mode, failure phase, transaction id when a transaction exists, rollback status, and broad classification. It intentionally does not include raw command output, profile servers, share URIs, private keys, tokens, private domains, or private IP addresses.

User-facing errors include the stable phase, classification and safe report path when available, then point to `podlaz doctor --tun --verbose`. Daemon logs use structured, low-cardinality fields rather than raw diagnostic text.

## CI gates

Package validation checks the Xray helper file, executable bit, architecture, service environment, declared dependencies, third-party notice file, and absence of obsolete TUN helper artifacts for both `amd64` and `arm64` packages.

The issue-specific installed-package convergence gate performs a clean purge, builds the branch `.deb`, installs and reinstalls the same package, extracts the built package, and compares SHA-256 hashes of packaged, installed, and running `podlazd` plus packaged/installed `podlaz`. It verifies the running systemd `MainPID` and tested commit identity.

The same dedicated run must accept packaged `Current Scopes: none` and complete the real missing-link rollback scenario. Before every disconnect or fault-release boundary it snapshots a private exact route/policy-rule manifest from the active transaction, including arbitrary main-table bypass tuples. Immediately after production rollback or disconnect it runs strict tri-state verification against that manifest; route/rule residue or an inspection error prevents `resources_absent=pass`. The private manifest remains outside the artifact directory.

For collision-aware session allocation, the same self-hosted workflow runs an
installed-package #260 coexistence scenario. It creates only sanitized synthetic
baseline objects that occupy historical `198.18.0.1/32`, table `51820`, and
priorities `9999`/`10000` together with unrelated TUN, DNS, and nftables state.
The candidate must connect without product-specific foreign VPN control, persist
a different exact allocation, verify the protected data plane, disconnect, and
prove that the synthetic foreign baseline remained structurally present until the
fixture itself is explicitly removed.

For current-health changes, target-host acceptance must additionally prove that a packaged active TUN survives a real suspend/resume boundary without silently retaining stale `verified` evidence: status must show the same or advanced generation only after post-resume revalidation, watcher reconnect/resync must not lose the transition, and a controlled verification failure/deadline must persist bounded redacted diagnostics before automatic bounded disconnect. Successful cleanup must converge to inactive; forced cleanup failure must remain `cleanup-required`; explicit user/shutdown cancellation must not produce duplicate cleanup. Evidence must remain redacted and structural.

The scenario timeout leaves a separate cleanup window. On every outcome the workflow attempts daemon recovery, runs strictly scoped fallback cleanup, removes E2E sentinels, and purges the package only after daemon/Xray absence is proven. It directly verifies no podlaz-owned state remains. Artifacts contain only normalized verdicts, classifications, lifecycle events, commit identity, and hashes; upload occurs only after cleanup assertions and scanning against configured secrets and actual host network values succeed.