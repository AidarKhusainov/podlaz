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
assign the product OS address. `podlazd` owns the exact `198.18.0.1/32` address
on the transaction-bound Xray link and the surrounding route bypass, policy
rules, DNS, nftables, transaction files, rollback, and recovery.

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

The deliberate contract for this PR is:

- Xray creates and owns the `podlaz0` link lifecycle and packet ingestion;
- Xray applies only the link name and MTU supported by the pinned schema;
- podlazd owns `198.18.0.1/32` plus Linux route, policy-rule, DNS, nftables,
  transaction, rollback, and recovery state;
- the fixed address is selected from non-globally-routed benchmarking space;
  all host IPv4 addresses and routes are inspected for overlap before mutation,
  and a conflict fails closed without a random fallback;
- podlazd commits only after exact address/link verification, static resolved
  verification, an uncached interface-scoped IPv4 query, normal system
  resolution, route lookup, routed TCP, and DNS-result route verification pass;
- VM or self-hosted validation must provide evidence that this pinned-schema split works on the target Linux hosts before the PR leaves draft.

If a future pinned Xray release adds supported `gateway`/`dns` fields, this contract must be revisited in the issue/PR with Xray schema evidence, generated JSON tests, and VM validation evidence.

## Hard TUN dependencies

Packaged TUN mode relies on these host components and declares them as Debian dependencies:

- `iproute2` for route, policy rule, route verification, and recovery operations around the Xray-owned TUN link;
- `nftables` for firewall and kill-switch state;
- `systemd-resolved` for per-link DNS apply, verify, and rollback;
- `polkitd | policykit-1` for the packaged daemon authorization boundary.

`network-manager` remains optional diagnostics only and is not a hard package dependency.

## Preflight contract

Before active podlaz replacement, controlled handoff cleanup, opening a TUN transaction, or applying host-networking changes, daemon-side connect checks that:

- the Xray helper resolves to an executable;
- packaged helpers under `/usr/lib/podlaz` match the running helper architecture when ELF metadata can be inspected;
- `ip`, `nft`, and `resolvectl` are available in the daemon execution environment;
- the pinned Xray helper accepts a minimal native `tun` inbound config through `xray test -config`;
- the server bypass resolves to a concrete IPv4 route outside `podlaz0`;
- `198.18.0.1/32` does not overlap any assigned host IPv4 address or route in any
  inspected table, and no foreign or ambiguous `podlaz0` address state exists.

The unsupported-Xray preflight uses a temporary, redaction-safe config with only `protocol: tun` and pinned-schema `name`/`MTU`/`userLevel` settings. It does not use profile-derived runtime config and it runs before active replacement, automatic podlaz-owned recovery, handoff mutation, and transaction creation. Unsupported Xray TUN support must not disconnect active podlaz TUN, run recovery, stop an external VPN connection, or leave a transaction artifact.

For non-interactive connect policies, the daemon first performs an ownership-aware IPv4 address/route collision classification before active-podlaz replacement, controlled handoff, transaction creation, or any host-network mutation. That early gate may disregard only an exact active podlaz address, a uniquely validated stale podlaz address routed to canonical recovery, or state on the concrete NetworkManager device explicitly authorized by `stop-known`; unrelated overlap and incomplete inventory block without disconnecting or stopping anything. The daemon then runs controlled recovery when the refreshed host snapshot contains unambiguous podlaz-owned stale TUN, route, policy-rule, nftables, or transaction state, performs any authorized handoff, recollects the snapshot, and repeats the authoritative collision check without allowances. Foreign VPN interfaces, foreign route-only DNS owners, ambiguous resources, and failed or incomplete recovery remain blockers. `--handoff=ask` stays mutation-free.

The generated profile runtime config path is recorded in transaction rollback metadata after the transaction is opened and before the daemon starts Xray with the generated config. If the daemon is interrupted after generated config write, recovery knows how to remove the generated config.

Missing or non-executable helpers, missing hard TUN commands, unsupported Xray TUN config, and unavailable server bypass are setup/runtime-unavailable failures. They must fail before route, DNS, nftables, firewall, handoff, active-podlaz replacement, or transaction mutation starts.

User-facing CLI output must not present these failures as a daemon crash or raw internal server error. It must state that TUN mode cannot start and that no network changes were applied.

## Transaction and rollback contract

A failed TUN connect is allowed to start Xray before host network apply because
the pinned native Xray TUN implementation owns packet ingestion and creates the
`podlaz0` link. After start, podlazd records the link name, ifindex, TUN type,
and appearance-after-child evidence before assigning `198.18.0.1/32`. The same
identity is revalidated before address apply, verification, rollback, and
recovery. Xray start alone is not a committed VPN connection. The connection is committed only after host network apply, host network verify, connectivity verify, and active-state commit all succeed.

The transaction's structured `desired_plan` is durably written before the daemon starts Xray or mutates host networking. During apply, each exact address, route, policy-rule, DNS, and nftables ownership step is validated and atomically persisted immediately after its mutation and before the next resource executor runs. A persistence failure stops the sequence while retaining the already-mutated step in the in-memory rollback boundary. Normal in-process rollback continues to use recorded applied steps. Desired intent never creates route, policy-rule, DNS, or nftables cleanup authority. A `planned` record contributes no host-network cleanup tuples. An `applying` record without durable applied/rollback ownership is preserved as an ambiguous crash window and performs no route, rule, DNS, or nftables mutation. `applied`, `verifying`, and inactive `committed` records clean only their durable applied/rollback multiset. The sole narrow syscall/persistence fallback is the TUN address in `applying`: a previously persisted bound name, ifindex, TUN kind, exact CIDR, and appearance-after-tracked-child proof becomes only a cleanup candidate and must pass fail-closed host identity and address inspection before removal. Desired-only main-table server bypass state is never deleted by assumption.

`desired_plan` records intent and validates the shape of an applied target; it is not proof that podlaz created a route or policy rule. An executor that finds an exact object already present returns no ownership step, so a valid `verifying` or `committed` transaction may contain desired routes/rules while its durable network `applied_steps` and matching rollback categories are empty. Mixed transactions own only the exact objects represented by durable applied steps. For every state that permits durable ownership, rollback route and policy-rule tuples must equal those applied ownership tuples as exact multisets, including duplicate count.

A `planned` transaction is before host-network mutation and follows a non-mutating network path. Desired routes or rules alone contribute no cleanup tuple; a planned record that already contains network applied steps or route/rule rollback tuples is internally inconsistent and is rejected. `applying` is the fail-closed crash window: when a desired route or policy-rule category has no durable applied proof, fallback cannot distinguish a pre-mutation record from a mutation that crashed before ownership persistence and must refuse network cleanup. Later states such as `applied`, `verifying`, and `committed` use the durable applied/rollback multiset without inventing ownership from desired intent.

Low-level address/route/rule/DNS/firewall composition executors do not perform hidden rollback when an apply method returns an error after mutating state. They report the partial non-zero applied `Step` to the transaction persistence sink before the next mutation or before returning the error. The transaction boundary accepts only an exact owner/target match, persists that ownership, and chooses the cleanup timing:

- the direct transaction helper immediately performs one bounded fail-safe rollback before returning;
- the production full-tunnel runner first collects and persists diagnostics, then invokes rollback using the same partial plan.

This is the boundary that guarantees `network-apply` diagnostics precede the first DNS, nftables, route, rule, or TUN rollback command. A child executor that did not mutate state returns a zero `Step`, which is not added to rollback ownership.

When a failed connect successfully rolls back every podlaz-owned mutation it applied, the transaction is first persisted as terminal `rolled_back` and then removed in a separate filesystem operation. A surviving `rolled_back` file is therefore stale lifecycle metadata only: rollback has already relinquished route and policy-rule ownership, and its historical tuples must never authorize another deletion. Cleanup validates only the stale record's schema, owner, and terminal state before removing the metadata after host/process safety checks. A terminal `committed` record remains a restart-recovery candidate because it may represent an interrupted active session after daemon restart. A committed transaction that is proven to be the exact current live lifecycle transaction is not cleanup-required: read-only status and doctor remain healthy, startup/recover publication filters its exact transaction candidate, and explicit recovery performs no mutation against it.

Transaction files in cleanup-required states such as `planned`, `applying`, `applied`, `verifying`, `rolling_back`, or `failed` continue to block connect until automatic or explicit daemon-owned recovery completes. An inactive committed transaction also blocks as a restart-recovery candidate; a proven live committed transaction does not. Invalid or unreadable transaction files are blockers because their ownership and cleanup state cannot be proven safely.

After every connect attempt, disconnect, or recovery execution, the daemon refreshes the startup recovery scan before publishing status/doctor/recover state. A successful rollback or recovery removes its completed transaction candidate, so an immediate subsequent TUN connect observes the reconciled host and persisted state rather than a phantom stale blocker.

Rollback order for failed or disconnected native TUN sessions is:

1. remove podlaz-owned nftables/firewall state;
2. revert podlaz-owned systemd-resolved per-link DNS state;
3. remove podlaz-owned routes and policy rules created by the transaction;
4. remove the exact daemon-owned TUN address after link-name, ifindex, kind, and ownership revalidation;
5. stop the Xray child process when it was started by the transaction;
6. remove generated runtime config;
7. remove the terminal transaction file only after rollback succeeds.

The installed-package fallback has an additional fail-closed identity and snapshot boundary. Normal daemon recovery may change transaction ownership, so a manifest captured while `podlazd` or an owned Xray can still mutate state is non-authoritative. Fallback first proves `podlazd` inactive and every transaction-owned Xray absent, then atomically creates the authoritative exact route/rule manifest from the final transaction records. If daemon stop, Xray termination, identity inspection, or the post-quiescence snapshot fails, fallback performs no route/rule mutation and preserves generated configuration, transaction metadata, and package executables for a later safe retry. Network cleanup failure after process quiescence also preserves generated configuration and transaction metadata until complete cleanup can be proven, and package purge remains refused until process quiescence is confirmed.

## DNS verification contract

Before applying DNS, podlaz runs a scoped `resolvectl revert podlaz0` to discard stale podlaz-owned per-link state. An already-missing link, including the exact `No such device` response, is idempotent. Podlaz never restarts `systemd-resolved` automatically and never edits `/etc/resolv.conf`.

The kernel may expose the newly-created `podlaz0` before `systemd-resolved` registers it. DNS apply therefore retries only the transient missing-link response for the `dns`, `domain`, and `default-route` commands for up to roughly two seconds. Permission errors, unsupported commands, and other unexpected failures are not retried.

`systemd-resolved` can expose recently applied per-link settings with a delay. DNS verification therefore polls for up to roughly two seconds before failing on transient missing target link, planned DNS server, route-only `~.` domain, or `+DefaultRoute`. `Current Scopes` is derived lookup state and is not an apply-success requirement. The target link must appear exactly once; duplicate `podlaz0` sections are ambiguous and fail closed. A foreign route-only DNS owner remains an immediate hard failure.

Rollback uses `resolvectl revert <link>` for the podlaz-owned link. The exact missing-device response is successful idempotent cleanup in runtime rollback, stale cleanup, doctor inspection, and transaction recovery. Other unexpected errors remain failures.

## Diagnostics and logs

Failures during `network-apply`, `network-verify`, or later connectivity verification run a short bounded redacted diagnostic subset while the failed applied state still exists. The latest report is atomically persisted before the first rollback command when persistence succeeds. Diagnostic collection and persistence are best-effort: they never replace the original failure and never prevent the separate bounded rollback context from running.

The report keeps `schema_version`, stable `failure_phase`, primary classification, bounded evidence, warnings/errors, safe report path, and `rollback_status`. It is first written with `rollback_status: pending`, then atomically finalized to `completed` or `failed`. After rollback and daemon restart, `doctor --tun` may read this replacement-only report as historical evidence according to the `/run` retention contract.

Daemon connect failures are logged as sanitized phase summaries. The log line includes the requested mode, failure phase, transaction id when a transaction exists, rollback status, and broad classification. It intentionally does not include raw command output, profile servers, share URIs, private keys, tokens, private domains, or private IP addresses.

User-facing errors include the stable phase, classification and safe report path when available, then point to `podlaz doctor --tun --verbose`. Daemon logs use structured, low-cardinality fields rather than raw diagnostic text.

## CI gates

Package validation checks the Xray helper file, executable bit, architecture, service environment, declared dependencies, third-party notice file, and absence of obsolete TUN helper artifacts for both `amd64` and `arm64` packages.

The issue-specific installed-package convergence gate performs a clean purge, builds the branch `.deb`, installs and reinstalls the same package, extracts the built package, and compares SHA-256 hashes of packaged, installed, and running `podlazd` plus packaged/installed `podlaz`. It verifies the running systemd `MainPID` and tested commit identity.

The same dedicated run must accept packaged `Current Scopes: none` and complete the real missing-link rollback scenario. Before every disconnect or fault-release boundary it snapshots a private exact route/policy-rule manifest from the active transaction, including arbitrary main-table bypass tuples. Immediately after production rollback or disconnect it runs strict tri-state verification against that manifest; route/rule residue or an inspection error prevents `resources_absent=pass`. The private manifest remains outside the artifact directory.

The scenario timeout leaves a separate cleanup window. On every outcome the workflow attempts daemon recovery, runs strictly scoped fallback cleanup, removes E2E sentinels, and purges the package only after daemon/Xray absence is proven. It directly verifies no podlaz-owned state remains. Artifacts contain only normalized verdicts, classifications, lifecycle events, commit identity, and hashes; upload occurs only after cleanup assertions and scanning against configured secrets and actual host network values succeed.
