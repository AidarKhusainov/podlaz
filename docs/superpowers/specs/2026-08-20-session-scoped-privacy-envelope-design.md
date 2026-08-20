# Session-Scoped Persistent Privacy Envelope Design

Status: approved design for Issue #261.

This document refines the privacy-recovery part of `2026-08-18-resilient-network-session-design.md`. It defines the implementation contract for preserving privacy while a protected TUN session is being recovered, and for returning cleanly to the remaining host network after an explicit or terminal teardown.

## Goal

Once Podlaz has published a protected `CONNECTED` state, ordinary non-exempt direct egress must not become available until the Network Session intentionally removes its privacy protection during explicit or terminal teardown.

The protection must survive replacement of individual data-plane generations, unexpected daemon restart, service/package restart, and bounded recovery. It must remain exactly owned and recoverable without taking ownership of unrelated host firewall, routing, DNS, TUN, or VPN state.

## Architecture

A Podlaz Network Session owns two different kinds of state:

```text
Podlaz Network Session
|
+-- Privacy Envelope
|   +-- exact-owned nftables resource
|   +-- durable session-backed identity / authority
|   +-- protection state
|   +-- survives data-plane generation replacement
|
+-- Data Plane Generation
    +-- Xray / TUN
    +-- TUN address
    +-- routes
    +-- policy rules
    +-- DNS
    +-- normal session firewall
```

The Privacy Envelope is a physically separate nftables resource, but it is **not** a second independent generic transaction state machine. Its lifecycle is controlled by the Network Session.

Existing data-plane transactions continue to own individual TUN generations and can be rolled back or replaced while the Privacy Envelope remains armed.

## Why a separate nftables resource

The current TUN firewall is part of the TUN transaction and is rolled back with that transaction. Keeping it alive slightly longer does not close the startup/recovery leak window because recovery can remove the firewall before reconnect begins.

Splitting the existing `inet podlaz` table into persistent and transient chains would also blur ownership: the current nftables executor verifies the complete table exactly and rollback deletes that whole table. Partial lifecycle ownership inside one table would make verification and cleanup substantially harder to reason about.

A routing blackhole or prohibit rule is not the primary privacy primitive. Routing is deliberately dynamic and collision-aware after Issue #260 and will be reconciled further by Issue #262. A dedicated firewall resource gives the Network Session a stable, exact, independently observable privacy boundary.

Therefore:

> Separate exact-owned nftables resource: yes. Separate independent lifecycle engine: no.

## Durable exact ownership

Before the first Privacy Envelope nftables mutation, Podlaz persists exact authority sufficient to reconstruct and prove ownership of that exact resource. The persisted record includes at least:

- schema / role version;
- Podlaz owner marker;
- Network Session identity;
- current boot identity where relevant to session continuation semantics;
- exact nftables family and table identity;
- exact expected composition or sufficient metadata to reconstruct and verify it exactly;
- protection state (`unarmed`, `arming`, `armed`, or `removing` internally as needed);
- exact bootstrap/control endpoint allowances required by the protected session.

Recognition is not ownership. A family/table/chain/comment/name that resembles Podlaz does not grant mutation authority.

The Privacy Envelope identity must be session-specific or otherwise collision-safe. A globally fixed table name is not sufficient as the ownership contract. If a candidate resource already exists without exact durable Podlaz ownership, Podlaz must select another verified-safe identity or fail closed when the bounded allocation space is exhausted.

Podlaz must never:

- flush the global nftables ruleset;
- delete an unknown or ambiguous table;
- restore a foreign firewall snapshot wholesale;
- infer deletion authority from conventional names alone.

## Initial connect ordering

Privacy-first recovery begins only after Podlaz has actually built and verified a working protected data plane.

The required ordering is:

```text
build Data Plane Generation
        |
        v
verify VPN data plane
        |
        v
persist exact Privacy Envelope authority
        |
        v
install Privacy Envelope
        |
        v
verify exact Privacy Envelope composition
        |
        v
re-verify critical VPN path with Privacy Envelope active
        |
        v
mark Network Session protected
        |
        v
publish CONNECTED
```

The post-envelope verification is mandatory. Podlaz must not verify connectivity, add the Privacy Envelope, and then immediately publish `CONNECTED`; the new rules themselves could block the Xray/server bootstrap path.

If envelope arming or the post-envelope data-plane verification fails, Podlaz must converge through exact cleanup without publishing a protected connected state.

## Privacy Envelope policy

The envelope blocks ordinary direct user egress outside the intended protected data plane while keeping only the minimum control plane needed to preserve or restore the VPN.

Conceptually allowed traffic may include:

- loopback;
- traffic through the currently active Podlaz TUN;
- exact VPN transport/bootstrap endpoint(s);
- narrowly defined system link-control traffic that is required for the underlying uplink to recover.

The implementation must not broadly permit:

- the whole physical uplink;
- arbitrary external UDP/53;
- arbitrary LAN access without an explicit product requirement;
- all established direct connections;
- catch-all exceptions added merely for convenience.

In particular, a generic `ct state established,related accept` must not allow ordinary direct Internet connections opened before protection to continue carrying user traffic after the Privacy Envelope is armed.

The exact DHCP, IPv6, link-local, resolver-bootstrap, or other control-plane exceptions must be justified by Linux behavior and deterministic tests. The governing rule remains: allow only recovery/control traffic, never general direct Internet traffic.

## Endpoint allowance updates

If recovery requires a different exact VPN/bootstrap endpoint, Podlaz must not remove the Privacy Envelope to recreate it.

Updates must be atomic from the packet-filter perspective or use an overlap sequence:

```text
old exact endpoint allowed
        +
new exact endpoint allowed
        |
        v
verify new transport path
        |
        v
remove old endpoint allowance
```

A short overlap between two exact VPN control endpoints is acceptable. An intermediate permissive state is not.

## Transient recovery and data-plane replacement

After `CONNECTED`, temporary failure transitions the Network Session into internal recovery/reconnect while the Privacy Envelope remains armed.

```text
CONNECTED
   |
   v
problem detected
   |
   v
RECOVERING / RECONNECTING
   |
   +-- Privacy Envelope: KEEP ARMED
   |
   +-- rollback exact old Data Plane Generation
   +-- observe current host network
   +-- build new generation
   +-- apply and verify
   +-- verify protected path with envelope active
   |
   v
CONNECTED
```

There must be no lifecycle boundary between generations where ordinary direct egress becomes allowed.

This is the protection contract for the future reconciliation scenarios developed further by Issue #262: Wi-Fi roaming, DHCP renewal, suspend/resume, uplink replacement, DNS reconstruction, route changes, Xray restart, or another routing/VPN layer changing the host baseline.

## Daemon restart and package/service replacement

Startup orchestration must not pass the Privacy Envelope to generic data-plane rollback as though it were another recoverable TUN transaction.

The intended flow is:

```text
podlazd starts
   |
   v
load Network Session continuation / lifecycle intent
   |
   v
load exact Privacy Envelope authority
   |
   v
observe + verify/reconcile the exact envelope
   |
   v
recover obsolete/incomplete Data Plane Generation(s)
   |
   v
resume/connect a new Data Plane Generation
   |
   v
verify data plane with envelope active
   |
   v
CONNECTED
```

Generic data-plane recovery does not have authority to decide that the Privacy Envelope should be removed. Envelope removal belongs to Network Session teardown after explicit disconnect or a persisted terminal transition.

Continuation intent and cleanup authority remain distinct. Removing reconnect intent must never erase the durable authority required to recover or remove a surviving exact Privacy Envelope.

## Explicit disconnect and terminal failure

Both explicit disconnect and terminal failure use the same safe teardown ordering; only the reason for the transition differs.

Required ordering:

```text
persist / declare terminal session intent
        |
        v
stop reconnect and recovery attempts
        |
        v
KEEP Privacy Envelope ARMED
        |
        v
cleanup all exact Podlaz Data Plane Generations
        |
        v
verify Podlaz data plane absent
        |
        v
remove exact Privacy Envelope
        |
        v
verify Privacy Envelope absent
        |
        v
verify remaining host network is usable
        |
        v
clear Network Session authority / continuation state
        |
        v
publish DISCONNECTED
```

For a user-requested disconnect, the first step is explicit disconnect intent rather than a failure decision; the cleanup safety contract is otherwise the same.

The post-Podlaz network is not required to be direct ISP networking. Another VPN, custom routing policy, or any other unrelated host configuration may remain underneath Podlaz. The contract is:

> Return control to the remaining host network and verify that the post-Podlaz network is usable.

Podlaz must not stop, restore, rewrite, or otherwise manage unrelated VPN software or foreign network state while reaching this result.

## Cleanup failure semantics

A failed teardown must never publish a false clean `DISCONNECTED`.

If data-plane cleanup is incomplete:

- the Privacy Envelope stays armed;
- Network Session cleanup authority stays durable;
- recovery remains required;
- clean `DISCONNECTED` is not published.

If the data plane is absent but the Privacy Envelope cannot be removed:

- the envelope remains exact-owned and recoverable;
- Network Session authority remains;
- clean `DISCONNECTED` is not published.

If the envelope has already been deliberately removed but the remaining host network has not yet been proven usable:

- Podlaz does not automatically start the failed VPN again;
- it does not publish a false clean state;
- terminal convergence / diagnostic authority remains until the post-Podlaz network outcome is known.

Removing the firewall resource therefore must not automatically delete all session metadata.

## State model

Do not introduce two independent top-level state machines such as `VPN transaction lifecycle` plus `Privacy transaction lifecycle`.

The preferred model remains:

```text
Network Session
   +-- continuation / lifecycle intent
   +-- protection state
   +-- exact Privacy Envelope identity / authority
   +-- current Data Plane Generation
```

Data Plane Generation continues to use the existing transaction architecture. Privacy Envelope authority is durable but session-scoped.

Internal protection states such as `unarmed`, `arming`, `armed`, and `removing` are acceptable where needed for deterministic crash recovery; they do not need to become a new public API state machine.

Do not create an independent `PrivacyManager`, generic `BarrierTransactionEngine`, or similar framework unless a concrete implementation constraint demonstrates that it is necessary.

## Recovery authority

All recovery operations keep the existing exact-ownership rule:

```text
recognition != ownership
```

Neither table name, chain name, comment, historical value, nor a continuation request by itself proves authority over a live nftables resource.

If exact ownership cannot be proven, Podlaz fails closed, leaves ambiguous state untouched, keeps the lifecycle non-clean, and does not falsely publish `DISCONNECTED`.

## Deterministic regression invariant

The primary regression guarantee is packet-path behavior, not a status string:

> From publication of protected `CONNECTED` until intentional explicit/terminal Privacy Envelope removal, no ordinary non-exempt uplink egress becomes admissible between lifecycle steps, between data-plane generations, or after daemon crash/restart.

Deterministic coverage must include at least:

- `CONNECTED -> core loss -> recovery -> CONNECTED`;
- `CONNECTED -> daemon SIGKILL -> restart -> CONNECTED`;
- `CONNECTED -> service/package restart -> CONNECTED`;
- old generation rollback before replacement generation exists;
- DNS reconstruction;
- temporary uplink loss;
- terminal failure returning to a clean remaining network;
- explicit disconnect;
- failure during data-plane cleanup;
- failure during Privacy Envelope removal;
- foreign nftables state alongside the envelope;
- collision with a candidate envelope identity;
- idempotent startup reconciliation.

Tests should assert real admissible/blocked packet paths where practical and deterministic exact nftables composition where rootless unit tests are more appropriate. `status == reconnecting` alone is not privacy evidence.

## Fault-injection boundaries

Crash/restart tests should cover these significant persistence/mutation boundaries:

- after envelope authority is persisted, before nftables apply;
- after nftables apply, before exact verification;
- after envelope verification, before `CONNECTED` publication;
- after old data-plane cleanup starts;
- after old generation is removed, before the new generation exists;
- after terminal decision is persisted;
- after data plane is removed, before envelope removal;
- after envelope removal, before remaining-network verification;
- after remaining-network verification, before session metadata clear.

Every simulated restart must converge deterministically without broadening ownership or leaking direct traffic.

## Installed-package acceptance

A controlled Ubuntu 24.04 installed-package regression must exercise the real service lifecycle.

Protected continuation scenario:

```text
install candidate package
connect TUN
prove protected data plane
induce daemon/service restart or supervised crash
prove ordinary direct egress remains blocked
prove automatic VPN continuation
```

Terminal scenario:

```text
connect
induce deterministic unrecoverable failure
prove Privacy Envelope remains during recovery
prove exact Podlaz data-plane cleanup
prove deliberate envelope removal
prove remaining host network usable
prove final DISCONNECTED
prove no automatic reconnect afterward
```

Successful acceptance must not require manual `ip rule`, `nft`, `resolvectl`, or `recover --execute` repair.

All test fixtures, logs, PR text, issues, and artifacts must use synthetic/documentation-safe endpoints and identifiers. Private profile values, user IPs, domains, and other sensitive host data must not be committed or published.

## Non-goals

Issue #261 does not:

- define the soft-vs-hard revalidation evidence model planned for Issue #262;
- implement boot autostart/autoconnect planned for Issue #263;
- identify or manage foreign VPN products;
- use infinite reconnect after a terminal failure;
- broaden direct networking during recovery;
- normalize the whole host firewall or routing state;
- build the Privacy Envelope primarily from policy routing;
- weaken exact ownership so cleanup can succeed cosmetically.

## Acceptance summary

Issue #261 is complete when the implementation provides a separate collision-safe exact-owned nftables Privacy Envelope whose durable authority belongs to the Podlaz Network Session, whose protection survives replacement of data-plane transactions and daemon/service/package recovery, and whose removal occurs only through an intentional session teardown after exact Podlaz data-plane cleanup. `DISCONNECTED` is published only after the envelope is absent and the remaining host network is verified usable, with no automatic reconnect after a terminal failure.
