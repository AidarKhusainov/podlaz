# Nftables Ownership Safety Design

## Goal

Fix #307 without broadening cleanup authority or redesigning the VPN policy: authority-bearing nftables observation must be semantic and machine-readable, fresh table creation must be exclusive, and destructive mutations must fail closed if the ruleset changes after verification.

## Scope

This change covers both Podlaz-owned nftables surfaces that currently share the same bug class:

- the session-scoped Privacy Envelope table;
- the transaction-owned `inet podlaz` table, including lifecycle/doctor exact verification and rollback deletion.

Public CLI/API/state schemas remain unchanged unless implementation proves that an existing contract cannot represent truthful recovery state. Any public compatibility change requires explicit review rather than being folded into this fix implicitly.

## Invariants

- Durable Network Session protection state remains the only long-lived authority for Privacy Envelope replacement/removal.
- Transaction state remains the only long-lived authority for transaction-owned nftables rollback.
- Family/name, generated names, comments, historical resemblance, handles, or read-only observation are never cleanup authority by themselves.
- Ambiguous or unavailable inspection remains fail-closed and retains durable authority.
- Durable protection authority is cleared only after exact absence has been re-observed.
- Human-readable `nft list` output and stderr text are diagnostic only and must not participate in authority-bearing decisions.
- Extra or foreign state inside an otherwise Podlaz-shaped table is ambiguous. It must never be ignored, normalized away, adopted, or deleted merely because the surrounding resource resembles Podlaz state.

## Structured observation

Use the documented structured nftables JSON API (`nft -j`) for authority-bearing reads. Numeric/human presentation flags such as `-y` are implementation details and are not part of the semantic contract.

Decode structured output into one small private internal snapshot model containing only the semantics and live identity evidence Podlaz actually needs:

- table family and name;
- exact table flags;
- ephemeral table handle;
- exact chain cardinality and security-relevant chain metadata;
- ordered rules;
- supported expressions/statements and verdicts;
- ownership comments;
- runtime metadata such as counters and rule handles only where needed for observation, never as persisted composition authority.

### Exact table and chain metadata

Table flags are part of the exact table composition. For the current Podlaz-owned compositions the expected table flag set is empty. Any unexpected flag, including `dormant`, `owner`, or `persist`, is semantic drift and fails closed unless a future explicitly reviewed composition contract changes the expected set. In particular, a `dormant` table must never verify as an active Privacy Envelope merely because its chains and rules otherwise match.

For each expected chain, exact verification includes at least:

- chain name;
- chain type;
- hook;
- numeric priority;
- policy;
- device binding (`dev`) where the chain representation supports it, including exact absence when Podlaz expects no device binding.

Chain cardinality is exact. Chain ordering is compared only where nftables semantics make that order meaningful; independent chain enumeration order must not become presentation-dependent ownership evidence. Rule order within each chain remains exact and security-relevant.

Kernel-assigned chain/rule handles and runtime counter values are not persisted composition semantics and must not create false drift.

### Strict object model

The decoder is strict and narrow. It validates the document envelope, metainfo shape when emitted, supported JSON schema semantics, expected object identities/cardinality, and all security-relevant expressions.

For current Podlaz compositions, any table-scoped object kind that is not explicitly expected must fail closed. This includes unexpected sets, maps, flowtables, named counters, quotas, stateful objects, or any other unhandled table member. The implementation must not silently skip an unknown object because it does not currently affect the planned rule list.

Unknown/unhandled rule statements or expressions, duplicate/ambiguous table or chain identities, malformed structured output, or unsupported representation also fail closed.

Only explicitly understood semantic normalization is allowed. In particular, the redundant explicit IPv6 family predicate adjacent to an ICMPv6 payload match is treated as equivalent to nftables' canonical implicit IPv6 dependency. Ordered rules remain ordered; unordered values inside one semantic set are compared as sets.

One shared semantic verifier is used by Privacy Envelope verification and ordinary transaction-owned nftables verification. No second human-text exact verifier remains in lifecycle/doctor paths.

### Presence, absence, and occupancy

Presence/absence and allocation occupancy are derived from successful structured table enumeration and exact family/name lookup, not from classifying human stderr text from `nft list table`.

The result is three-state:

- exact identity absent from a successfully decoded enumeration -> absent;
- exact identity present -> present, regardless of whether later semantic verification accepts its composition;
- enumeration/decoding unavailable, malformed, unsupported, or ambiguous -> unknown/error.

Unknown is never converted to absence. An occupied generated candidate is skipped but never adopted as ownership.

## Fresh creation

Any path that claims a fresh Podlaz-owned table must use exclusive creation semantics equivalent to `nft create table`, in the same atomic nftables transaction as the initial chains/rules.

A prior absence observation is allocation evidence only. If another process creates the same family/name before the transaction commits, exclusive creation must fail and the transaction must not add chains/rules to that table. Podlaz never adopts the occupied object and must not retry using the same now-occupied identity.

## Coherent observation and generation identity

Authority-bearing verification used for a later mutation must come from one coherent nftables ruleset generation.

The implementation obtains the nftables generation identity around structured observation and accepts the snapshot only when it can prove that the observed semantic state belongs to one unchanged generation. If the generation changes while the snapshot is being established, the partial observation is discarded. A bounded read-only retry is allowed; inability to obtain a coherent snapshot is `unknown`, never absence or ownership.

The table handle captured from the same coherent observation is ephemeral live-object identity evidence. It is validated as part of the mutation precondition but is not persisted as durable ownership state.

## Destructive mutation and concurrency

Destructive Privacy Envelope Remove/Replace and transaction-owned table rollback are authorized in two layers:

1. durable Podlaz state authorizes which resource may be considered for mutation;
2. fresh coherent kernel evidence proves the exact current live object and semantic composition immediately before mutation.

Every destructive mutation is committed as one atomic nftables mutation transaction bound to the generation ID of the coherent snapshot that was verified. If the nftables ruleset changes after verification, the kernel must reject the stale transaction (for example with `ERESTART`) and Podlaz must treat the mutation as not authorized by the stale evidence.

This requirement applies to the complete logical mutation, not only its first command. In particular, Privacy Envelope `Replace` must preserve its current atomic whole-composition property: deleting the verified old composition and creating the complete replacement composition must occur in the same generation-guarded transaction. A guarded delete followed by a separate unguarded `nft -f` apply is invalid because it reintroduces both a stale-evidence race and an unprotected intermediate state.

The implementation may continue using the `nft` CLI where it can satisfy this contract. If the CLI boundary cannot bind a mutation transaction to the observed generation ID, a narrow private netlink mutation transport is allowed. This is an implementation mechanism, not a new public abstraction or a broad nftables backend rewrite.

### `ERESTART` and stale-evidence retry semantics

A stale mutation transaction is never replayed.

After `ERESTART` or equivalent stale-generation rejection, all evidence associated with the failed attempt is discarded. The caller may either report incomplete/retryable recovery or perform a bounded retry of the entire sequence:

1. obtain a new coherent generation-bound observation;
2. reconstruct fresh semantic evidence;
3. verify the exact composition again;
4. construct a new mutation transaction from that fresh evidence;
5. commit it against the newly observed generation.

Retrying only the previous batch, reusing its handle/generation, or skipping semantic re-verification is forbidden. This bounded full-sequence retry does not broaden cleanup authority because every attempt rebuilds its decision from fresh durable authority plus fresh kernel evidence.

Because nftables generation is ruleset-wide, unrelated legitimate firewall changes may invalidate an attempt. Such contention may cause a bounded retry or an incomplete/retryable result, but must never cause Podlaz to bypass the generation guard.

### Post-mutation proof

After successful terminal deletion, Podlaz performs a new structured observation and clears durable authority only after exact family/name absence is proven.

If the post-mutation observation is unavailable or ambiguous, durable authority remains. A successful mutation command alone is not sufficient proof that cleanup authority may be discarded.

## Privacy Envelope composition

Keep the current durable Privacy Envelope composition model for #307. Do not introduce dynamic nftables sets/maps or a new composition version solely for this fix.

Generated ICMPv6 neighbor-discovery rules use the canonical minimal expression without a redundant explicit IPv6 predicate. In an `inet` table, the ICMPv6 expression itself supplies the required IPv6 dependency, so removing the redundant predicate does not broaden the rule to IPv4 traffic.

The verifier remains backward-compatible with the released v0.2.39 spelling only when the old and new forms are semantically identical. Compatibility normalization must remain narrow and explicitly understood; it must not hide additional predicates, missing predicates, extra rules, changed comments, changed verdicts, changed table flags, or changed base-chain metadata.

Protected replacement keeps the existing lifecycle and atomic whole-composition Replace behavior, but the complete Replace transaction receives the same fresh semantic verification and generation guard as Remove. A future migration to static rules plus dynamic sets/maps is out of scope.

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

Status and diagnostics must preserve a known Privacy Envelope verification/cleanup cause using existing typed/public fields where possible; known failure evidence must not be collapsed into a false clean state or an avoidably generic internal diagnostic.

## Compatibility

A fixed daemon must recover same-boot v0.2.39 Network Session protection authority and the corresponding old-form live Privacy Envelope without reboot or manual firewall cleanup. Existing persisted schema/composition version is preserved unless implementation proves that compatibility cannot be maintained; old and canonical ICMPv6 spellings are accepted only when semantically identical.

No real user IP, domain, profile ID, credential, subscription, SSID, or endpoint may appear in tests, docs, PRs, or logs. Use RFC-reserved/example values.

## Implementation shape

Do not introduce a new nftables framework or public abstraction layer. Keep the existing `PrivacyEnvelopeExecutor` and `NftablesExecutor` and share only concrete private nftables mechanics needed by both consumers.

A small internal snapshot type and private helpers for structured observation, semantic comparison, exclusive creation, coherent generation capture, and generation-guarded mutation are sufficient. Do not add `Repository`, `Service`, `Manager`, `Factory`, or strategy interfaces solely for testability or future extensibility.

A narrow private netlink helper/library is justified only if required to implement generation-bound atomic mutation correctly. It must remain below the existing executor boundary and must not duplicate the semantic model already provided by structured nftables observation.

## Testing

Required regression coverage includes:

- production-shaped ICMPv6 canonicalization regression;
- unexpected table flags, including `dormant`, are rejected;
- exact expected table flags are accepted;
- changed chain name/type/hook/priority/policy/device binding is rejected;
- chain enumeration order alone does not create false drift when order has no packet-processing semantics;
- extra/missing/reordered/changed rules, comments, verdicts, predicates, unknown expressions, and duplicate/ambiguous JSON objects are rejected;
- unexpected table-scoped sets/maps/flowtables/named counters/quotas/stateful objects are rejected rather than ignored;
- exact absent/present/unknown observation semantics;
- malformed/unsupported observation is never classified as absence;
- generation change during observation discards the partial snapshot;
- foreign same-name table created between absence observation and apply causes exclusive create failure without foreign mutation;
- semantic mutation after verification before Remove causes generation-guarded failure without mutation;
- same-name delete/recreate after verification before Remove causes generation-guarded failure without deleting the replacement;
- Privacy Envelope Replace remains one atomic generation-guarded transaction and cannot delete/rebuild from stale evidence;
- stale `ERESTART` batches are never replayed; any retry starts again from full observation and semantic verification;
- transaction-owned rollback cannot delete a changed/replaced same-name table;
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
- relying on `owner`/`persist` table flags as a mandatory ownership baseline;
- changing public CLI/API/state schemas without separate evidence and compatibility review.
