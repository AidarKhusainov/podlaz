# Nftables Ownership Safety Design

## Goal

Fix #307 without broadening cleanup authority or redesigning the VPN policy: authority-bearing nftables observation must be semantic and machine-readable, fresh table creation must be exclusive, and destructive mutations must fail closed if the ruleset changes after verification.

## Scope

This change covers both Podlaz-owned nftables surfaces that currently share the same bug class:

- the session-scoped Privacy Envelope table;
- the transaction-owned `inet podlaz` table, including lifecycle/doctor exact verification.

Public CLI/API/state schemas remain unchanged unless implementation proves that an existing contract cannot represent truthful recovery state.

## Invariants

- Durable Network Session protection state remains the only long-lived authority for Privacy Envelope replacement/removal.
- Transaction state remains the only long-lived authority for transaction-owned nftables rollback.
- Family/name, generated names, comments, historical resemblance, handles, or read-only observation are never cleanup authority by themselves.
- Ambiguous or unavailable inspection remains fail-closed and retains durable authority.
- Durable protection authority is cleared only after exact absence has been re-observed.
- Human-readable `nft list` output is diagnostic only and must not participate in authority-bearing decisions.

## Structured observation

Use `nft -j -y` for authority-bearing reads. Decode the document into one small private internal snapshot model containing only semantics Podlaz actually needs: table identity and handle, chain metadata, ordered rules, supported expressions/statements, verdicts, counters as runtime metadata, and ownership comments.

The decoder is strict and narrow. It validates the document envelope, supported JSON schema, expected object identities/cardinality, and all security-relevant expressions. Unknown/unhandled statements or ambiguous duplicate objects fail closed. Runtime-assigned metadata such as counters and rule handles does not create false semantic drift.

Only explicitly understood semantic normalization is allowed. In particular, the redundant explicit IPv6 family predicate adjacent to an ICMPv6 payload match is treated as equivalent to nftables' canonical implicit dependency. Ordered rules remain ordered; unordered values inside one semantic set are compared as sets.

One shared semantic verifier is used by Privacy Envelope verification and ordinary transaction-owned nftables verification. No second human-text exact verifier remains in lifecycle/doctor paths.

## Fresh creation

Any path that claims a fresh Podlaz-owned table must use exclusive creation semantics equivalent to `nft create table`, in the same atomic nftables batch as the initial chains/rules.

A prior absence observation is allocation evidence only. If another process creates the same family/name before the batch commits, the exclusive create must fail and the transaction must not add chains/rules to that table. Podlaz never adopts the occupied object.

## Destructive mutation and concurrency

Destructive Remove/Replace is authorized in two layers:

1. durable Podlaz state authorizes which resource may be considered for mutation;
2. fresh kernel evidence proves the exact live object and composition immediately before mutation.

Observation captures table handle and nftables ruleset generation. The semantic snapshot must be coherent: if generation changes while the snapshot is being established, inspection is retried only as a bounded read; inability to obtain one coherent snapshot is `unknown`, not absence or ownership.

The destructive nftables batch is committed with the observed generation ID. If any process changes the nftables ruleset after verification, the kernel rejects the stale batch (for example with `ERESTART`) and Podlaz fails closed without mutation. This closes same-name delete/recreate and same-object semantic-mutation races between Verify and Remove/Replace.

The table handle is ephemeral identity evidence for the current observation. It is validated as part of the snapshot but is not persisted as durable ownership state.

After successful terminal deletion, Podlaz performs a new structured observation and clears durable protection authority only after exact family/name absence is proven.

## Privacy Envelope composition

Keep the current durable Privacy Envelope composition model for #307. Do not introduce dynamic nftables sets/maps or a new composition version solely for this fix.

Generated ICMPv6 neighbor-discovery rules use the canonical minimal expression without a redundant explicit IPv6 predicate. The verifier remains backward-compatible with the released v0.2.39 spelling when semantics are exactly equivalent.

Protected replacement keeps the existing lifecycle and atomic whole-composition Replace behavior, but the Replace operation receives the same fresh semantic verification and generation guard as Remove. A future migration to static rules plus dynamic sets/maps is out of scope.

## Recovery and crash safety

Existing persistence ordering remains authoritative:

- protection/removal intent is durable before destructive mutation;
- a crash before delete leaves recoverable authority;
- a crash after delete but before authority clear is resolved by structured re-observation;
- present + exact + terminal intent continues exact removal;
- proven absent clears stale authority without mutation;
- present + semantically different/ambiguous retains authority and fails closed;
- unavailable observation retains authority and reports incomplete recovery.

A Network Session with Privacy Envelope authority is recovery-relevant even if transaction recovery has zero candidates. `recover --execute --yes`, startup, terminal teardown, replacement recovery, and reconciliation route through the same Network Session convergence semantics rather than treating transaction candidate count as proof of cleanliness.

## Compatibility

A fixed daemon must recover same-boot v0.2.39 Network Session protection authority and the corresponding old-form live Privacy Envelope without reboot or manual firewall cleanup. Existing persisted schema/composition version is preserved unless implementation proves that compatibility cannot be maintained; old and canonical ICMPv6 spellings are accepted only when semantically identical.

No real user IP, domain, profile ID, credential, subscription, SSID, or endpoint may appear in tests, docs, PRs, or logs. Use RFC-reserved/example values.

## Implementation shape

Do not introduce a new nftables framework or public abstraction layer. Keep the existing `PrivacyEnvelopeExecutor` and `NftablesExecutor` and share only concrete private nftables mechanics needed by both consumers.

A small internal snapshot type and private helpers for structured observation, semantic comparison, exclusive batch creation, and generation-guarded mutation are sufficient. Do not add `Repository`, `Service`, `Manager`, `Factory`, or strategy interfaces solely for testability or future extensibility.

## Testing

Required regression coverage includes:

- production-shaped ICMPv6 canonicalization regression;
- extra/missing/reordered/changed rules, comments, verdicts, base-chain metadata, predicates, unknown expressions, and duplicate/ambiguous JSON objects;
- exact absent/present/unknown observation semantics;
- foreign same-name table created between absence observation and apply;
- semantic mutation after verification before Remove;
- same-name delete/recreate after verification before Remove;
- same races for Replace;
- crash/restart at each durable removal boundary;
- v0.2.39-shaped persisted/live recovery;
- recovery/status cannot report clean while protection authority remains;
- sibling transaction-owned `inet podlaz` exact verification uses the same structured boundary;
- real Ubuntu 24.04 nftables apply/list/verify/remove round-trip in normal PR CI.

Final repository verification follows `AGENTS.md`, including gofmt, `go test ./...`, daemon race tests, `go vet ./...`, `govulncheck ./...`, repository structure, relevant shell/workflow/package checks, and exact-candidate privileged acceptance where required before release publication.

## Non-goals

- redesigning Privacy Envelope policy;
- migrating bootstrap endpoints to sets/maps;
- broad firewall backend rewrite;
- broad-flushing nftables;
- cleanup by generated-name resemblance;
- weakening exact verification to subset matching;
- relying on `owner`/`persist` table flags as a mandatory baseline;
- changing public CLI/API/state schemas without separate evidence and compatibility review.
