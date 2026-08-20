# Network Session Privacy Envelope

This document is the canonical engineering contract for the Podlaz session-scoped Privacy Envelope introduced by issue #261. It complements `state-and-security.md` and `packaged-tun-runtime.md`; transaction ownership rules in those documents remain authoritative for the replaceable TUN data plane.

## Purpose

A protected TUN Network Session has two lifetimes:

1. the **Privacy Envelope**, a session-scoped exact-owned nftables barrier that remains present while the session is protected; and
2. the **Data Plane Generation**, the replaceable Xray/TUN/address/routing/policy/DNS/session-firewall transaction.

The Privacy Envelope exists so a daemon crash, data-plane replacement, recovery, or transient loss of the old generation cannot create an interval where ordinary user traffic silently falls back to direct uplink egress.

The envelope is a separate nftables resource, but it is **not** an independent generic transaction state machine. Its authority and lifecycle are part of the durable Network Session record.

## Durable authority

`/run/podlaz/network-session-continuation.json` now stores the current-boot Network Session record. The record is private (`0600`), atomically replaced, bounded, and serialized through one per-path state-transition lock.

The durable record contains, as applicable:

- schema and owner;
- Linux boot ID and random Network Session ID;
- session intent: `resume`, `disconnect`, or `terminal`;
- the current connect request;
- Privacy Envelope protection state: `unarmed`, `arming`, `armed`, or `removing`;
- exact nftables family/table identity;
- composition version;
- exact TUN interface identity;
- exact pre-resolved VPN bootstrap IPv4 endpoint set;
- transient previous endpoint composition while an atomic replacement is in progress;
- protected-generation replacement authority containing the previous request and previous protection.

Reconnect intent and cleanup authority are deliberately separate. `disconnect` or `terminal` prevents automatic continuation, while Privacy Envelope and replacement metadata may remain until exact terminal convergence finishes.

Every state mutation uses one atomic `Load -> transition -> validate -> fsync/rename -> directory fsync` boundary. Independent `SetIntent` and `SetProtection` callers therefore cannot overwrite one another with stale JSON snapshots.

A generated table name, comment, historical Podlaz value, or matching rule shape is never ownership authority. Mutation requires the exact durable session record.

## Exact nftables resource

The Privacy Envelope uses a collision-safe table in family `inet` with a session-derived name under the reserved form:

```text
podlaz_pe_<12 lowercase hex>[_N]
```

Candidate allocation is read-only. An occupied candidate is skipped; Podlaz never adopts, rewrites, or deletes an occupied candidate merely because its name resembles a Podlaz table.

The canonical output chain is an `output` base chain with priority `-10` and policy `accept`. Its ordered rules allow only:

1. loopback egress;
2. egress through the exact Podlaz TUN interface;
3. each exact authorized VPN/bootstrap IPv4 endpoint;
4. DHCPv4 client control traffic (`UDP 68 -> 67`);
5. DHCPv6 client control traffic (`UDP 546 -> 547`);
6. the narrow IPv6 router/neighbor discovery control types needed to recover link connectivity;
7. a final reject for all other output traffic.

The composition intentionally contains no whole-uplink allow, arbitrary LAN allow, arbitrary UDP/53 allow, catch-all established/related conntrack allow, `0.0.0.0/0` or `::/0` bypass, or global firewall normalization.

`PrivacyEnvelopeExecutor` applies and verifies the complete exact composition. `Replace` changes one exact composition to another with one nftables batch and keeps the same family/table identity. `Remove` is authorized only from exact durable authority after the live table has been verified.

Podlaz never uses `nft flush ruleset` and never deletes an unknown table.

## Initial protected connect

A new protected TUN session follows this publication order:

1. build and apply the transaction-backed data plane;
2. verify the data plane and VPN connectivity;
3. persist exact Privacy Envelope authority in `arming` **before** the first envelope nft mutation;
4. apply the exact envelope;
5. verify the complete envelope composition;
6. persist `armed`;
7. repeat the critical VPN connectivity proof while the envelope is active;
8. commit the data-plane transaction and publish `CONNECTED`/active state.

If post-envelope verification fails, exact data-plane rollback runs before the envelope is deliberately removed. If data-plane rollback is incomplete, the envelope and durable authority remain fail-closed for recovery.

## Protected generation replacement

`handoff=replace-podlaz` is a one-shot lifecycle request, not part of Network Session identity.

Before destructive replacement, the session persists `Replacement` authority containing the previous request and previous protection. If the target transport endpoint differs, the Privacy Envelope is atomically widened:

```text
old endpoint -> old + new endpoints
```

Only after that widened barrier is exactly verified may the old Data Plane Generation be removed. After the new generation is built and verified, the envelope is atomically narrowed:

```text
old + new endpoints -> new endpoint
```

The narrowed composition is exactly verified before replacement metadata is committed away and the handoff policy is normalized back to ordinary `block`.

A failed replacement restores the previous exact barrier and previous session request. If the old data plane was already removed, Podlaz performs one bounded daemon-owned reconnect of the previous generation. That reconnect does not inherit cancellation/deadline from the failed HTTP request; explicit daemon stop can cancel it through the Network Session lifecycle fence.

For crash recovery, the only accepted live replacement compositions are exact persisted candidates derived from the previous protection, current protection, and transient previous endpoint composition. Recovery first verifies which candidate is live. If exactly one candidate verifies, it can atomically restore the previous barrier and then clear replacement metadata. If none or more than one candidate can be proven, recovery fails closed without mutation.

## Startup and `/recover`

There is one intent-aware startup orchestration path: `resumeNetworkSession`.

For `intent=resume` the ordering is:

1. reconcile the exact Privacy Envelope;
2. recover exact old Data Plane Generations;
3. run generic daemon recovery against the refreshed status;
4. reload the durable Network Session record;
5. reconnect using the **freshly reloaded** request;
6. release the startup mutation gate only after convergence.

Reloading after recovery is required because replacement reconciliation may restore the previous request, or an explicit stop may change intent while recovery is in flight.

A protected restart reuses the persisted concrete bootstrap IPv4 endpoint for the matching session instead of requiring broad direct DNS access before the tunnel is restored.

When the startup mutation gate is blocked, `/recover` does not run a separate generic mutation phase first. Its serialized follow-up executes the same intent-aware Network Session convergence under the lifecycle operation token.

## Explicit and terminal teardown

Explicit disconnect, service stop, and terminal health disposition persist non-resume intent before teardown mutation. An in-flight protected replacement may retain `Replacement` metadata under `disconnect` or `terminal`; that metadata is cleanup authority, not reconnect authorization.

Terminal convergence is ordered:

1. stop/disable automatic reconnect intent;
2. clean exact transaction-backed Data Plane Generations;
3. if a replacement transition is still durable, exact-converge its old/union/new barrier state without restarting a target generation;
4. verify the remaining exact Privacy Envelope composition;
5. persist `removing`;
6. remove only that exact table;
7. verify the exact table is absent;
8. clear protection authority;
9. verify the remaining host network through read-only route/TCP/resolver evidence;
10. clear the Network Session record and publish clean `DISCONNECTED`.

The post-Podlaz verifier does not require direct ISP routing. A foreign VPN or custom default route may remain authoritative baseline. It fails when Podlaz-owned `podlaz0` residue remains, route evidence is missing/unknown, or the bounded functional route/TCP/resolver proof cannot be established.

If data-plane cleanup, exact envelope removal, or post-network verification fails, durable terminal authority remains and clean disconnected state is not published.

## Crash boundaries

Deterministic regressions cover at least these durable boundaries:

- `armed` with old data-plane residue;
- `removing` while the exact table is still present;
- `removing` after the table is already absent;
- envelope removed but protection metadata not yet cleared;
- protection cleared but post-network verification not yet complete;
- replacement before widening;
- widened old+new composition;
- narrowing intent persisted before nft replacement;
- narrowed nft generation committed before state verification;
- target protection verified before replacement commit;
- failed atomic narrowing where the old+new composition remains live;
- caller cancellation after destructive replacement;
- explicit service stop while previous-generation fail-safe restore is running;
- concurrent shutdown intent and protection-state persistence.

Every ambiguous observation remains fail-closed. Recovery never infers ownership from a candidate name or from resemblance to historical Podlaz state.

## Status publication

The underlying Xray/TUN cleanup may become internally inactive before session-level terminal convergence finishes. Public status therefore applies a Network Session cleanup guard: durable session/protection authority prevents publication of a misleading clean `DISCONNECTED` boundary until envelope removal and terminal verification have converged.

## Validation

The default hosted CI gate for this implementation includes:

- `gofmt`;
- `go test ./...` including deterministic lifecycle, race, crash-boundary, exact-composition, and replacement regressions;
- `go vet ./...`;
- `govulncheck`;
- CLI contract smoke tests;
- Debian package build, metadata/content validation, local install/reinstall/purge;
- workflow and shell lint.

The repository also contains an installed-package #261 acceptance harness for environments that have a compatible disposable Linux runner. Availability of such a runner is operational infrastructure, not additional ownership authority. The production safety contract above does not depend on E2E hooks being enabled.

## Non-goals

The Privacy Envelope does not authorize:

- global firewall reset or normalization;
- cleanup of foreign VPNs, routes, policy rules, DNS links, nftables objects, or NetworkManager connections;
- a second generic transaction engine;
- speculative repair from ambiguous state;
- permanent boot autostart solely because current-boot continuation state exists;
- weakening TLS, DNS, route, or exact nftables verification to make recovery succeed.
