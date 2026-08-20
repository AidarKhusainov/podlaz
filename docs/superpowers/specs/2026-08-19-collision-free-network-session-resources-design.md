# Collision-Free Network Session Resources Design

Status: implementation design for #260.

## Goal

Allow a new Podlaz TUN Network Session to coexist with unrelated or imperfect host networking state by selecting collision-free Podlaz-owned resources, persisting their exact identities before mutation, and using those identities throughout apply, verification, disconnect, restart recovery, and explicit recovery.

## Core model

The host snapshot and the session allocation are different concepts:

```text
read-only host snapshot
        |
        v
prove a usable server bootstrap path
        |
        v
allocate verified-free Podlaz resources
        |
        v
immutable ResourceAllocation
        |
        v
persist transaction before mutation
        |
        v
plan -> apply -> verify -> recover/disconnect
```

The snapshot is evidence about the surrounding host. `ResourceAllocation` is the exact set of identities Podlaz decided to occupy for one Network Session. The allocation is immutable for that session and must be persisted before the first privileged network mutation.

## Allocation scope

`ResourceAllocation` contains only identities that Podlaz chooses and later needs for exact lifecycle ownership. For #260 this includes:

- the Podlaz TUN IPv4 point address/CIDR;
- the numeric full-tunnel routing table ID;
- the numeric VPN-server bypass rule priority;
- the numeric full-tunnel rule priority;
- any other dynamically selected identity introduced by this change and required for exact cleanup/recovery.

It does not contain the host snapshot, diagnostic observations, or unrelated baseline state.

Historical values such as `198.18.0.1/32`, table `51820`, and priorities `9999`/`10000` may remain first-choice candidates for compatibility and predictable diagnostics. They are never ownership proof. If occupied by unrelated state, allocation must select different verified-free values.

## Allocation algorithm

Allocation is deterministic over one authoritative snapshot.

### TUN address

Select the first candidate from a bounded documentation-only address pool whose point address does not overlap any observed IPv4 address or route. The current historical address remains the first candidate. If address/route inventories required to prove safety are incomplete or malformed, allocation fails closed rather than assuming absence.

### Routing table

Select the first candidate numeric table ID from a bounded Podlaz allocation range that is absent from the authoritative route inventory and policy-routing evidence. Table `51820` remains the first candidate. New sessions persist and execute the numeric table ID directly; symbolic `podlaz`/historical forms remain accepted only where legacy migration or recovery requires them.

### Policy-rule priorities

Select one ordered pair where the server-bypass rule has stronger precedence than the full-tunnel rule and both precede the kernel `main` rule. `9999`/`10000` is the first candidate pair. Both priorities must be absent from authoritative policy-rule evidence. Exhaustion fails before mutation.

A race after allocation is handled safely by atomic kernel add semantics: if a selected identity is claimed before apply, connect fails and rolls back only steps that were actually recorded as Podlaz-owned. Podlaz never deletes the competing object to make room.

## Bootstrap path

The connect precondition is not a pristine direct network. Podlaz needs a concrete usable route to the selected VPN server endpoint from the actual current host network.

A server route through an unrelated TUN or custom routing layer is acceptable when it is concrete and usable. Podlaz does not identify which product owns that path and does not stop or reconfigure it. Connect fails before destructive mutation only when no safe concrete bootstrap path can be established.

## Coexistence semantics

After #260, the question is not "does foreign networking state exist?". The question is "does the observed state prevent this exact Podlaz plan from being installed safely without mutating unowned state?"

Therefore these are baseline observations, not automatic blockers:

- unrelated TUN/TAP/WireGuard-like interfaces;
- unrelated policy rules or routing tables;
- unrelated custom routes;
- unrelated NetworkManager VPN connections;
- non-Podlaz resolved links or route-only DNS state;
- unrelated nftables tables/chains;
- partially degraded soft diagnostics that do not prevent bootstrap or the required Podlaz mutation/verification evidence.

Podlaz must not scan product names, stop foreign daemons, deactivate foreign VPN profiles, delete unknown TUN devices, or normalize the host before connect.

## Transaction and ownership

The exact allocation is persisted in the daemon transaction before any privileged network mutation. Desired state, applied steps, rollback metadata, restart continuation, disconnect, and recovery derive resource identities from that persisted allocation.

Cleanup authority remains exact and session-backed:

- a numeric match with historical Podlaz values is never enough to delete a route or rule;
- a foreign object that occupies a historical value is preserved;
- malformed or incomplete transaction ownership evidence remains fail-closed;
- legacy fixed layouts may be recognized for migration/diagnostics only when existing exact transaction evidence authorizes them.

The transaction schema may gain optional allocation fields without invalidating older transaction files. Legacy recovery must continue to understand historical transaction records.

## Planner and executor boundaries

The planner owns read-only allocation and emits an inspectable `TunPlan` containing the exact selected resources. New route and rule plans use exact numeric identities.

Executors remain narrow: they apply and verify the exact plan. They do not choose alternative identities during apply and do not resolve collisions by deleting pre-existing state.

Recovery consumes persisted exact identities. It must not reconstruct a new allocation from the current host.

## Firewall and DNS

#260 does not require a generalized dynamic firewall namespace or a new DNS backend. Existing Podlaz-owned DNS/firewall resources may stay fixed where their ownership contract is already exact and collision-safe.

However, pre-existing unrelated DNS or nftables state must not be rejected merely because it exists. If an exact Podlaz-owned firewall object would collide with an unowned object and cannot be installed safely without replacing it, that is a genuine plan conflict and connect fails before foreign mutation.

## Failure semantics

Allocation or connect fails before destructive foreign mutation when:

- required authoritative evidence for a resource cannot prove a free candidate;
- the bounded candidate pool is exhausted;
- there is no concrete safe server bootstrap path;
- an exact Podlaz-owned resource cannot be installed without replacing unowned state;
- persisted session ownership evidence required for cleanup/recovery is malformed or incomplete.

Unrelated diagnostics that are imperfect but do not invalidate the concrete plan remain warnings/soft evidence rather than blockers.

## Verification

Successful connect must verify the exact allocated table, rule priorities, TUN address, DNS/firewall state, core/TUN relationship, and protected data plane required by the existing full-tunnel contract. Verification must use the session allocation rather than historical constants.

Disconnect/recovery tests must prove that unrelated baseline state is structurally unchanged after Podlaz-owned resources are removed.

## Deterministic tests

Required regressions include sanitized fixtures where unrelated baseline state occupies historical Podlaz values and also contains unrelated TUN, routes/rules, DNS links, and nftables state. Tests prove:

1. a different deterministic allocation is selected;
2. a safe server path through the actual baseline is accepted;
3. partially degraded soft diagnostics do not independently block connect;
4. allocation is persisted before mutation;
5. apply/verify use the allocation;
6. disconnect/recovery remove only exact allocated resources;
7. the foreign baseline remains unchanged;
8. exhaustion and missing bootstrap path fail before mutation;
9. numeric resemblance never grants delete authority;
10. malformed ownership evidence stays fail-closed.

## Installed-package acceptance

Add a sanitized packaged acceptance scenario that establishes unrelated pre-existing TUN/routing/DNS state, including occupation of historical routing identifiers, then connects Podlaz, verifies the protected data plane, disconnects, and proves the unrelated baseline remains present and structurally unchanged. The fixture must not identify or control a real foreign VPN product.

## Non-goals

- general event-driven reconciliation or self-healing (#262);
- privacy-first transient recovery policy (#261);
- user-facing CLI/status redesign;
- product-specific foreign VPN adapters;
- repairing arbitrary foreign broken networking;
- guaranteeing arbitrary nested VPN combinations when no usable server path exists.
