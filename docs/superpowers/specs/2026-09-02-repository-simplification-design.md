# Repository Simplification and Context Reduction Design

Status: approved direction; implementation requires explicit review of this written spec before coding.

## Goal

Reduce repository context cost, duplication, stale knowledge, and maintenance burden without intentionally changing Podlaz product behavior or weakening safety guarantees.

The final repository should optimize for a small task-specific working set: an engineer or AI agent should be able to locate the authoritative contract and relevant code for one bounded context without reading unrelated historical designs, issue narratives, package scenarios, or large cross-cutting documents.

This is one coordinated repository-simplification pull request rather than a sequence of transitional cleanup PRs. The PR may be large, but its semantic scope is narrow: representation, organization, naming, deduplication, dead-code removal, and behavior-preserving decomposition only.

## Non-goals

The PR must not intentionally:

- change public CLI commands, flags, exit codes, JSON semantics, or authorization behavior;
- change TUN/network ownership, transaction, rollback, recovery, privacy, lifecycle, revalidation, or boot-autostart semantics;
- broaden daemon privileges or mutation authority;
- weaken fail-closed behavior, redaction, diagnostics, or deterministic regression coverage;
- add new product features;
- perform package-boundary refactors merely to reduce file count;
- remove tests solely because they are issue-specific or verbose.

If cleanup reveals a product bug or ambiguous contract, preserve current behavior in this PR and record the product change separately rather than silently fixing it here.

## Final documentation model

Permanent product documentation is reduced to four knowledge surfaces:

1. `README.md` — product purpose, current capabilities, high-level architecture, installation/quick-start guidance, and links to the other permanent contracts.
2. `docs/cli.md` — complete public CLI contract: commands, flags, modes, output, JSON schema expectations, exit codes, safety/confirmation semantics, aliases and completion behavior where applicable.
3. `ARCHITECTURE.md` — concise engineering invariants that cannot be recovered cheaply or safely from one implementation file. This includes privilege boundaries, ownership/authority rules, transaction lifecycle invariants, state-category distinctions, fail-closed principles, redaction requirements, and a compact source-tree map.
4. `AGENTS.md` — agent/developer workflow only: source-of-truth hierarchy, context routing, review discipline, validation commands, repository safety rules, and instructions not to treat historical Git material as active product contracts.

No other Markdown/man/design document is retained merely because it existed historically. Existing documentation is first distilled: every still-valid long-lived invariant must be represented by code/tests or one of the four permanent surfaces before the source document is removed.

`AGENTS.md` must not duplicate product contracts. It should route work to the relevant permanent contract and source area. Default reading should be small: `AGENTS.md`, `README.md`, and only the task-relevant contract. An agent must not be instructed to preload packaging, release, E2E, state, security, man pages, and unrelated networking documents for every task.

## Historical design and implementation material

`docs/superpowers/specs/*` and `docs/superpowers/plans/*` are implementation-process artifacts, not permanent product documentation.

Completed implementation plans must be removed from the final tree. Implemented design specs must also be removed after their durable invariants have been distilled into code/tests or the permanent documentation set. Git history, issues, and pull requests remain the historical record.

The repository must not expose completed specs/plans as an alternative active source of truth because their intermediate architecture or terminology may be superseded by later implementation.

This repository-simplification design and its implementation plan are temporary workflow artifacts: they may exist on the feature branch while work is in progress, but they must be removed before the final PR is considered complete, after the PR description captures the migration evidence needed for review.

## Documentation distillation rules

For each deleted document, classify each substantive statement as one of:

- **public contract** → retain in `README.md` or `docs/cli.md`;
- **engineering invariant** → retain concisely in `ARCHITECTURE.md`;
- **agent workflow** → retain in `AGENTS.md`;
- **implementation detail already enforced by code/tests** → delete from prose;
- **temporary acceptance evidence, roadmap, issue narrative, implementation steps, or obsolete design** → delete from the active tree.

Do not copy entire old documents into `ARCHITECTURE.md`. Distillation is intentionally lossy for implementation narration and intentionally lossless for current invariants.

## Source-tree and naming cleanup

Permanent production/test artifacts should be named by behavior or domain invariant, not by the issue number that introduced them.

Issue-oriented E2E scripts, Go regression tests, helper files, CI entrypoints, artifact names, and workflow labels should be renamed when a stable behavior-oriented name is clear. Historical issue references may remain in commit/PR history and in a short source comment only when the link materially helps diagnosis.

Examples of target naming concepts include:

- active-session package upgrade continuity;
- foreign-resource preservation;
- privacy-envelope recovery;
- uplink reconciliation;
- boot-autostart continuity;
- daemon restart/session continuation.

Renames must preserve test coverage and workflow behavior. They are not justification to combine semantically different regression scenarios.

## E2E consolidation

The current E2E suite contains repeated shell primitives with small semantic differences, especially daemon/socket readiness, service checks, package provenance, command execution, diagnostics, and network observations.

Consolidate proven duplicate primitives into focused libraries rather than one generic utility file. The preferred shape is approximately:

```text
scripts/e2e/lib/
  common.sh
  daemon.sh
  package.sh
  network.sh
  tun.sh
  assertions.sh
```

The exact final file map may differ if the repository demonstrates a better cohesive boundary.

Shared helpers must have explicit semantics. For example, `wait_for_daemon_socket`, `wait_for_daemon_ready`, and `wait_for_service_active` are distinct concepts and must not be collapsed into one ambiguous helper merely to reduce line count.

A helper extraction is acceptable only when callers genuinely share the same contract. Scenario-specific mutation and failure evidence remains local to the scenario.

## Go code simplification

The PR may simplify production Go code where the change is demonstrably behavior-preserving and reduces the working set or duplication.

Primary focus:

- reduce oversized orchestration methods, especially daemon composition/wiring;
- extract cohesive private constructors/components;
- eliminate exact duplicate state/error/polling helpers where ownership is clear;
- remove code proven dead by repository-wide search and tests;
- split large files by responsibility when that improves locality.

Do not introduce a large new package graph as a cosmetic refactor. Prefer stabilizing boundaries inside the existing package first. `internal/daemon/server.go` should become a clearer composition root, but TUN, boot, HTTP, recovery, and lifecycle code should move to new packages only if dependency direction and ownership become materially clearer and tests justify the move.

File count is not a metric. Task-specific cognitive/context load is.

## Safety and preservation requirements

The cleanup must preserve these repository-wide invariants:

- privileged network mutation remains daemon-owned;
- cleanup/mutation authority derives only from exact durable ownership, never resemblance to live state;
- transactional apply/verify/commit and exact rollback/recovery semantics remain fail-closed;
- Network Session, transaction state, boot-autostart policy/attempt, product terminal state, and current TUN health remain distinct concepts;
- ambiguous ownership/evidence never becomes cleanup permission;
- secrets, private endpoints, addresses, SSIDs, profile identifiers, credentials, and subscription material do not enter public logs, tests, examples, PR evidence, or shareable artifacts;
- tests protecting race, crash, rollback, privacy, recovery, coexistence, and lifecycle edge cases remain represented after renaming/consolidation.

No real personal IP addresses, domains, profile identifiers, or other private environment values may be added during the cleanup. Use documentation-safe synthetic values.

## One-PR execution strategy

The implementation is one PR from `master`, but changes should be committed in reviewable phases so the final diff is auditable:

1. establish baseline inventory and behavior-preservation mapping;
2. distill permanent contracts and shrink `AGENTS.md`;
3. remove obsolete/historical documentation and process artifacts;
4. rename issue-oriented tests/scripts/workflow references by behavior;
5. consolidate duplicated E2E helpers with focused regression checks;
6. perform targeted Go orchestration/deduplication cleanup;
7. remove temporary simplification spec/plan artifacts;
8. run the full validation matrix and produce before/after repository/context metrics.

Intermediate commits may temporarily contain old and new representations. The final tree must satisfy the simplified knowledge model.

## Verification strategy

Before semantic cleanup, capture a baseline inventory sufficient to map old artifacts to final artifacts. The PR description must include a concise migration table for deleted documentation and renamed/merged tests/scripts.

At minimum run, when supported by the repository/tooling:

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
```

Run all repository shell lint/contract checks and the deterministic portions of E2E/acceptance coverage affected by script or workflow renames. Validate workflow references after renaming.

For behavior-preserving source moves/refactors, focused tests must pass before and after the move. Existing safety regressions must not be deleted without a clear successor test covering the same invariant.

Real-host destructive E2E need not be executed merely because files are renamed, but any refactor of their shared mutation logic must be covered by deterministic contract checks and must be called out explicitly if real-host execution is unavailable.

## Context-reduction acceptance criteria

The final PR is successful only if all of the following hold:

- permanent prose documentation is limited to `README.md`, `docs/cli.md`, `ARCHITECTURE.md`, and `AGENTS.md`;
- completed `docs/superpowers/specs` and `docs/superpowers/plans` are absent from the final tree;
- `AGENTS.md` no longer instructs agents to preload a large fixed documentation set;
- permanent source/test names are behavior/domain oriented rather than issue-number oriented wherever safe to rename;
- duplicated E2E infrastructure is materially reduced without obscuring semantic differences;
- targeted large Go orchestration areas are easier to navigate without introducing needless package fragmentation;
- public product behavior and safety semantics remain unchanged;
- no regression coverage is silently lost;
- all feasible validation gates pass;
- the PR reports before/after documentation bytes/file counts and a representative task-context estimate.

## Review standard

Because this is intentionally a large PR, reviewability comes from explicit preservation evidence rather than from minimizing changed-file count.

The final PR description must state:

- what was removed, renamed, merged, or split;
- where each deleted long-lived contract now lives;
- which test/script names replaced issue-oriented artifacts;
- which duplicated helpers were consolidated and what their exact semantics are;
- which Go refactors were purely structural;
- validation commands and results;
- any checks that could not be executed;
- before/after documentation/context metrics.

Any discovered requirement that cannot be safely preserved within this behavior-neutral cleanup is a blocker for that deletion/refactor, not permission to guess.