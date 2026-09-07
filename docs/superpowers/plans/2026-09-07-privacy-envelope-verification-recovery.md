# Privacy Envelope Verification Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Privacy Envelope verification semantic and machine-readable so valid nftables canonicalization cannot strand a fail-closed egress barrier, while preserving exact ownership and drift rejection.

**Architecture:** Keep durable Network Session authority and fail-closed cleanup ordering unchanged. Replace presentation-oriented `nft list` comparison on owned firewall compositions with structured `nft -j` observation normalized into a small internal semantic model shared by the Privacy Envelope and ordinary Podlaz nftables verifier. Keep unknown/extra composition fail-closed. Remove the redundant explicit IPv6 family predicate from the ICMPv6 link-control rule because nftables already establishes that dependency implicitly.

**Tech Stack:** Go 1.26, `encoding/json`, nftables JSON schema v1, existing executor/daemon transaction abstractions.

**Spec:** `ARCHITECTURE.md`

## Global Constraints

- No new external dependency.
- No user-specific network/profile data in code, tests, docs, PRs, or logs.
- Preserve exact Network Session authority and cleanup ordering.
- Extra/unknown rules remain ambiguous and must fail closed.
- Successful data-plane cleanup followed by an exactly verified Privacy Envelope must restore ordinary networking without reboot.

### Task 1: Reproduce production canonicalization and stranded cleanup

- [ ] Add a production-shaped seven-rule Privacy Envelope fixture including DHCPv4, DHCPv6, ICMPv6 link control, and final reject.
- [ ] Feed canonical nftables output where the ICMPv6 rule omits the redundant explicit IPv6 family predicate and prove current verification fails at that rule.
- [ ] Add lifecycle coverage proving the same false mismatch prevents `RemoveAfterDataPlaneCleanup` and leaves protection authority present.
- [ ] Run focused tests and record RED evidence.

### Task 2: Replace presentation parsing with structured exact verification

- [ ] Observe owned tables through `nft -j -y list table`.
- [ ] Parse schema-v1 table/chain/rule objects with strict validation.
- [ ] Normalize only the nft statements Podlaz actually emits.
- [ ] Compare exact chain metadata, rule cardinality/order, ownership comments, and semantic statements.
- [ ] Reject unknown objects/statements, extra rules/chains, changed predicates, changed verdicts, changed comments, and malformed JSON.
- [ ] Keep missing-table handling and mutation authority unchanged.

### Task 3: Canonicalize the production IPv6 link-control rule

- [ ] Replace `meta nfproto ipv6 icmpv6 type {...}` with the semantically sufficient `icmpv6 type {...}`.
- [ ] Preserve the exact allowed ND message set and owner comment.

### Task 4: Audit sibling cleanup/recovery paths

- [ ] Confirm every Privacy Envelope reconciliation/removal path uses the same structured executor verifier.
- [ ] Add regression coverage for restart/recover/disconnect after exact data-plane cleanup.
- [ ] Prove genuine composition drift still refuses mutation and retains authority.

### Task 5: Final verification and PR hygiene

- [ ] Run repository Go/race/vet/vulnerability checks.
- [ ] Run repository shell/workflow/package checks via CI.
- [ ] Review diff for scope, privacy, rollback/recovery, and compatibility.
- [ ] Remove this temporary plan before Ready for review.
- [ ] Squash to a coherent final commit and rerun CI on exact final HEAD.
