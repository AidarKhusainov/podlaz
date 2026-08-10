# Committed TUN uplink revalidation

This document defines the current-health contract for a committed TUN session.
It complements the durable transaction and ownership rules in
`state-and-security.md`; read-only verification grants no new mutation or cleanup
authority. Terminal cleanup after a proved verification failure reuses the exact
rollback authority already recorded by the committed transaction.

## Durable transaction state versus current health

`committed` remains a historical transaction fact: network apply, exact canonical
state verification, connectivity verification, and active-state commit all
succeeded for the network state verified before commit.

A committed transaction is not permanent proof that the same host networking is
still valid. Suspend/resume, DHCP renewal, Wi-Fi roaming, Ethernet changes, or
route replacement may change the underlying uplink while the Xray process and
`podlaz0` survive.

The daemon therefore publishes a separate current TUN health value:

- `verified`: the current network generation is proved usable;
- `revalidating`: a changed/currently suspect generation is being reproved;
- `degraded`: current evidence is incomplete, or a terminal verification
  failure/timeout has been published while bounded lifecycle cleanup is being
  entered;
- `cleanup-required`: exact lifecycle cleanup could not converge, or active
  durable ownership no longer matches the live runtime strongly enough to treat
  the session as safely owned.

The status API exposes a positive `network_generation` together with the current
health state and a stable classification. Human `podlaz status` output keeps the
transaction state separate and returns the diagnostic unhealthy exit code when
an active TUN is no longer `verified`.

Generation 1 has its own TOCTOU boundary. Connect-time verification proves the
pre-commit observation only. After commit, the daemon synchronously publishes
generation 1 as `revalidating`, releases lifecycle mutation authority, then
queues the generation-1 proof through the same coordinator used for later
revalidation. That proof collects a fresh observation, runs the canonical
composition and connectivity verifier against that exact observation, and only
then publishes `verified`. If verification fails or times out, the new
fingerprint is never treated as healthy merely because connect previously
committed; the terminal failure path described below captures diagnostics and
enters normal bounded disconnect/rollback.

## Event model

Events are hints to collect new authoritative evidence. Their payload is never
the network state of record.

The Linux daemon watches:

- systemd-logind `PrepareForSleep(false)` as a post-resume hint;
- rtnetlink link changes;
- rtnetlink IPv4/IPv6 address changes;
- rtnetlink IPv4/IPv6 route changes.

Event bursts are coalesced. At most one revalidation runs at a time and a burst
that arrives while it is running schedules one fresh follow-up observation.
This prevents event storms from creating an unbounded probe queue.

Both edge-driven sources use subscribe-then-resync behavior. Once the logind
subscription or rtnetlink multicast bind is successfully established, the source
queues one `source-resync` hint. The same happens after a source failure and
successful reconnect. This fresh snapshot closes the gap for resume/DHCP/route
changes that may have occurred while the watcher was unavailable.

## Uplink fingerprint

A fresh snapshot is reduced to an underlying-uplink fingerprint containing only
state that can invalidate the committed generation:

- underlying default-route interface name and kernel ifindex;
- default-route gateway;
- relevant global IPv4 addresses on that interface;
- active NetworkManager connection identity when NetworkManager supplies
  authoritative evidence for that interface;
- the server-bypass route interface and gateway.

NetworkManager detection and active-connection inspection are distinct evidence.
`nmcli` inspection runs with `LC_ALL=C` and explicit terse escaping. Active
connection rows are parsed with escape-aware field separation so escaped `:` and
`\` characters inside values cannot shift `NAME,UUID,TYPE,DEVICE,STATE` columns.
Malformed escaping or an unexpected field count makes active-connection evidence
unknown. If NetworkManager itself is detected but `connection show --active`
cannot be inspected or parsed successfully, the active connection identity is
unknown and fingerprint derivation fails closed. An empty identity is
authoritative only when the active connection inventory was successfully
inspected and contains no activated connection for the underlying uplink.

Podlaz-owned `podlaz0`, policy-routing, `systemd-resolved` link state, and
nftables state are deliberately excluded. Those resources can change as a
result of Podlaz itself and therefore must not create a self-triggered network
generation loop.

If the fresh observation is ambiguous or incomplete, the previous `verified`
health is invalidated. Inspection failure is never treated as evidence that the
old generation is still healthy. Ambiguous observation/ownership evidence also
does not authorize automatic mutation: foreign or unproved state remains
fail-closed for explicit recovery rather than being deleted by the terminal
verification-failure path.

For ordinary duplicate link/address/route hints, an unchanged fingerprint while
current health is already `verified` is discarded without network probes and the
generation does not advance. Post-resume and source-resync are deliberately
different: they invalidate freshness of the old proof, so the same fingerprint
is reproved without incrementing the generation. A material fingerprint change
advances the generation once and starts read-only verification. A degraded
generation may be reproved again without artificially incrementing the generation.

## Read-only revalidation

Revalidation itself performs no repair and no privileged networking mutation. It:

1. proves that the daemon's active runtime still names one committed TUN
   transaction;
2. proves the supervised Xray child identity against durable transaction
   metadata;
3. reconstructs the complete persisted desired verification plan while keeping
   cleanup authority limited to exact rollback ownership;
4. collects a fresh snapshot and uplink fingerprint;
5. runs the shared canonical TUN composition verifier against current desired
   state;
6. runs the existing bounded connectivity verifier for route selection,
   resolved-link readiness/scoped query, normal system resolution, and routing
   of resolved IPv4 results through the TUN;
7. runs an explicit bounded DNS A query over UDP to a planned resolver;
8. runs an explicit bounded DNS A query over TCP to a planned resolver;
9. proves generic TCP/443 reachability using the shared diagnostic target catalog;
10. performs a validated TLS handshake on TCP/443;
11. performs a bounded HTTPS request with normal certificate validation and the
    shared `tundiag.NetworkClient` transport.

The explicit wire/data-plane stages reuse `internal/tundiag.NetworkClient`
(`DNSUDP`, `DNSTCP`, `TCP`, `TLS`, and `HTTPS`) and the shared target catalog.
They are not shell or `curl` probes. Every stage inherits the revalidation
context and also has its own bounded target timeout. Cancellation or deadline at
any layer prevents publication of `verified`.

The shared canonical verifier is exact rather than subset-based. On the
podlaz-owned resolved link, the observed DNS-server set and route-only domain set
must exactly match the plan and `+DefaultRoute` must be present; extra DNS
servers/domains are not accepted. For the podlaz-owned nftables table, chain
cardinality/name/type/hook/numeric priority/policy and ordered rule cardinality/
content must exactly match the plan; extra rules/chains or base-chain drift fail
closed. Connect-time verification and revalidation use this same verifier.

A route or policy rule that was already present before Podlaz connected remains
part of the read-only desired verification projection even when it correctly has
no rollback entry. Desired state can prove what must still be true; it never
creates deletion authority. The same distinction applies to the server-bypass
route used to identify the server route to inspect.

No NetworkManager mutation, IPv6 firewall policy change, nftables repair, route
repair, route-cache flush, DNS repair, or TUN recreation is performed by the
verification phase. IPv6 policy/leak enforcement remains evidence-gated by the
later #245 phase; this PR does not create an IPv6 bypass or change firewall
policy. Any future repair must be justified by deterministic failure evidence
and must preserve the existing exact-ownership and transaction boundaries.

## Terminal verification failure and cleanup

A fresh observation whose ownership is proven but whose canonical/data-plane
verification fails, or whose bounded verification reaches its deadline, is a
terminal current-health failure for that committed session. The daemon does not
leave that session indefinitely active as merely `degraded`.

The required ordering is:

1. the read-only verifier returns a typed terminal outcome containing the exact
   verification cause, failed generation, current plan projection, and stable
   health classification;
2. the lifecycle revalidation token is released;
3. the coordinator clears the completed active trigger so cleanup cannot cancel
   or requeue the verification that just finished;
4. before host cleanup, the daemon runs the existing bounded pre-rollback TUN
   diagnostic pipeline and atomically attempts to persist the latest private,
   redacted report with `rollback_status=pending`; a nested
   `TunVerificationError` phase such as `dns-udp`, `dns-tcp`, `tcp-443`, `tls`,
   or `https` is retained as the report `failure_phase`;
5. the daemon invokes the normal lifecycle `Disconnect` path with the existing
   bounded rollback context. That path may mutate only resources authorized by
   exact durable transaction ownership and performs the normal supervised Xray
   shutdown/convergence sequence;
6. successful cleanup finalizes the report with `rollback_status=completed` and
   the active TUN becomes inactive; failed or timed-out cleanup finalizes the
   report with `rollback_status=failed` and current health is published as
   `cleanup-required` so durable ownership remains actionable by recovery.

Diagnostic persistence is best-effort and must never suppress cleanup. If the
report itself cannot be persisted, the existing diagnostic contract records the
internal diagnostic failure and exposes no false report path, while lifecycle
cleanup still runs.

A cancellation caused by a higher-priority user disconnect/recovery or daemon
shutdown is different from a verification timeout. `context.Canceled` produces a
non-terminal revalidation outcome: it does not schedule another automatic
Disconnect. The lifecycle operation that published cancellation already owns the
cleanup decision. Daemon shutdown terminally cancels event processing and then
runs its existing bounded disconnect path. This prevents recursive/double
lifecycle cleanup while preserving mutation precedence.

## Serialization and cancellation

Revalidation is serialized with connect, disconnect, and recovery through the
same lifecycle operation boundary. Cancellation is intentionally outside that
boundary.

Generation-1 post-commit proof follows that rule too: connect publishes
`revalidating` and enqueues the proof while it still owns lifecycle mutation
authority, but the coordinator does not run the verifier until that authority is
released. Consequently disconnect/recovery can cancel an in-flight generation-1
proof before acquiring mutation authority, and daemon shutdown terminally
cancels it before bounded disconnect. A successful disconnect clears pending
initial-proof state, so a mutation-requeued `initial` trigger becomes a no-op
instead of reproving a session that no longer exists.

A lifecycle mutation declares mutation intent before cancelling an active
revalidation. A trigger already consumed while mutation is pending waits for the
complete mutation queue to become idle instead of returning success and being
lost. If mutation interrupts an already-running revalidation, the active trigger
is merged back into the bounded pending queue before its context is cancelled.
After mutation, the retained trigger produces one fresh authoritative snapshot
and the normal fingerprint/generation decision.

The caller's own context bounds how long it can wait for an uncooperative
revalidation to release the lock. Daemon shutdown first stops new event delivery
and terminally cancels active probes, then performs disconnect using a bounded
shutdown context; shutdown cancellation intentionally does not requeue work.

This ordering prevents a long network probe from making disconnect or shutdown
wait for the full probe timeout, while still preventing revalidation from racing
with privileged lifecycle mutations or losing network evidence that arrived at
the mutation boundary.

## Stable failure classifications

Current TUN health uses the following machine-readable classifications:

- `uplink_revalidating`;
- `uplink_changed`;
- `uplink_fingerprint_unavailable`;
- `ownership_invalid`;
- `owned_state_invalid`;
- `connectivity_failed`;
- `revalidation_timeout`;
- `revalidation_interrupted`.

The classification describes the current evidence/failure layer. It does not
create cleanup authority. Automatic terminal cleanup is authorized only by the
already-proven committed transaction and its durable exact rollback metadata.

## Test contract

Unit tests cover the properties that can be made deterministic without sleeping
a CI host:

- generation 1 verifies the exact fresh post-commit observation before
  `verified` publication and fails closed on verifier failure;
- disconnect and daemon shutdown cancel an in-flight generation-1 proof before
  privileged disconnect mutation proceeds;
- explicit DNS UDP success followed by DNS TCP failure cannot publish
  `verified`;
- successful DNS followed by TCP/443 failure cannot publish `verified`;
- successful TCP/443 followed by TLS failure cannot publish `verified`;
- successful TLS followed by HTTPS failure cannot publish `verified`;
- cancellation and deadline propagate through every explicit DNS UDP, DNS TCP,
  TCP/443, TLS, and HTTPS layer;
- verification failure returns a terminal typed outcome and enters bounded
  normal lifecycle disconnect only after revalidation authority is released;
- revalidation timeout returns the same terminal-cleanup outcome, while
  `context.Canceled` from a higher-priority lifecycle operation does not schedule
  a recursive second disconnect;
- the bounded diagnostic report is attempted before cleanup and preserves the
  failed verification layer;
- cleanup failure publishes `cleanup-required` and finalizes diagnostic rollback
  status as `failed`;
- the coordinator clears the completed active trigger before terminal cleanup,
  so cleanup cancellation cannot requeue the completed revalidation;
- post-resume signal classification and same-generation reproving;
- source failure/reconnect schedules authoritative source-resync and reproof;
- link/address/route rtnetlink trigger classification;
- duplicate event-storm coalescing;
- a trigger consumed during lifecycle mutation runs after mutation;
- an in-flight trigger cancelled by lifecycle mutation is requeued;
- cancellation before lifecycle-lock acquisition;
- bounded mutation wait when a probe ignores cancellation;
- unchanged ordinary uplink hints do not advance the generation;
- gateway/address/interface/ifindex/NetworkManager identity changes alter the
  fingerprint;
- failed or malformed NetworkManager active-connection inspection is unknown
  rather than authoritative absence, and escaped terse fields retain column
  identity;
- Podlaz-owned state cannot alter the fingerprint;
- ambiguous fingerprint evidence fails closed without acquiring new mutation
  authority;
- changed generation reuses the canonical verifier and connectivity pipeline;
- canonical resolved verification rejects extra DNS state and canonical
  nftables verification rejects extra rules/chain metadata drift;
- timeout/cancellation remove stale `verified` health;
- ownership mismatch is represented as cleanup-required rather than repaired;
- complete desired routes/rules remain verifiable even when pre-existing state
  correctly grants no rollback ownership;
- current health is rendered independently from transaction commit state.

A target-host suspend/resume acceptance run is still required before treating a
packaged build as production-validated for a specific laptop/kernel/network
stack. That run should capture before/after status, generation, daemon logs, the
fresh route/address/DNS snapshot, watcher reconnect/resync behavior, and the
read-only verifier result. It must also prove explicit DNS UDP/TCP, TCP/443,
TLS, and HTTPS success after the post-resume generation is invalidated and before
that generation returns to `verified`; and it must exercise one controlled
terminal verification failure or timeout to prove that the report is captured
before bounded disconnect and that successful cleanup converges to inactive.
Public evidence must remain structural/redacted as defined by `e2e.md`.
