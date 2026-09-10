# Resume Terminal Convergence Design

## Problem

A current-boot protected TUN Network Session may survive package replacement with `intent=resume` while the candidate replay fails after the old generation has already converged away. Today the daemon preserves Privacy Envelope protection and blocks ordinary lifecycle mutation, but it has no typed authority to decide that the failed replay is terminal for the exact current recovery attempt and that exact terminal convergence is now safe. Re-running recovery advances `RecoveryEpoch` and may overwrite the first actionable failure diagnostic, leaving the host indefinitely fail-closed even after the data plane is gone.

The fix must preserve fail-closed ownership semantics while adding a deterministic same-boot convergence path for the exact `v0.2.40 active -> candidate` package boundary. It must reuse, not duplicate, the terminal recovery ordering already implemented for exact TUN teardown.

## Constraints

- Reuse the existing Network Session state, serialized mutation boundary, exact transaction recovery, Privacy Envelope lifecycle, and persisted terminal teardown path.
- Keep replay disposition, attempt freshness, and terminal cleanup safety as separate predicates.
- Historical diagnostics remain evidence, never cleanup authority.
- Preserve the released `podlaz.network-session-resume-diagnostic.v1` schema identifier and old top-level fields so same-boot package rollback to `v0.2.41` remains readable.
- Do not expose raw `SessionID`, `RecoveryEpoch`, transaction identity, command stderr, profile identity, or endpoint data publicly.
- Do not retry destructive lifecycle operations merely to obtain a different diagnostic.
- Do not add a package, recovery subsystem, second teardown coordinator, or dependency.

## Backward-readable additive v1 replay evidence

Keep one private current-boot resume diagnostic file and keep its schema identifier exactly:

`podlaz.network-session-resume-diagnostic.v1`.

Do not introduce a v2 schema for this issue. Released `v0.2.41` accepts only the v1 schema identifier, while ordinary Go JSON decoding ignores unknown object fields. The fixed candidate therefore extends v1 additively so a same-boot downgrade to `v0.2.41` can still decode the file.

The existing top-level v1 fields remain present with their existing names and meanings:

- `recovery_epoch`;
- `resume_stage`;
- `last_resume_outcome`;
- optional `tun_failure_phase`;
- optional `rollback_status`;
- `transaction_present`;
- `legacy_migration`.

When the extended record contains a structured `current` replay attempt, these top-level fields are its backward-compatible projection. A new reader validates that the top-level projection and structured `current` attempt agree before any attempt evidence can be mutation-eligible. A mismatch is diagnostic-only/incomplete and never authorizes terminalization.

Add only optional unknown-to-v0.2.41 fields:

- private top-level `session_id` for the structured current-attempt binding;
- optional `replay_disposition` and `network_apply_subphase` compatibility projections for new readers;
- optional `originating` replay-attempt record;
- optional `current` replay-attempt record.

Each structured replay-attempt record contains:

- private `session_id`;
- `recovery_epoch`;
- `replay_disposition`: `terminal`, `retryable`, `interrupted`, or `incomplete`;
- resume stage;
- TUN failure phase;
- rollback status;
- transaction-present diagnostic evidence;
- legacy-migration flag;
- optional privacy-safe `network_apply_subphase`.

The file remains mode `0600` and bounded. Existing released v1 records without structured attempt fields remain readable by the new candidate as legacy diagnostic evidence, but because they lack the exact `SessionID` plus typed replay disposition they are never mutation authority for `resume -> terminal`.

If a downgraded `v0.2.41` process rewrites the file and therefore drops fields it does not know, a later fixed candidate must treat the remaining old v1 projection as legacy diagnostic-only evidence. Loss of the extension must never be reconstructed into terminalization authority.

Before any structured replay attempt has been admitted, existing pre-replay startup diagnostics may continue to use the old top-level v1 projection. Once a structured replay attempt exists, later privacy/exact/generic recovery blockers must not overwrite `originating` or `current`; they are surfaced as current recovery blockers while the preserved replay evidence remains intact.

`transaction_present` remains historical evidence only. Current transaction cleanup authority continues to come from exact recovery candidates/durable transaction state.

The originating attempt is retained until resume succeeds, terminal convergence completes, or an explicit user lifecycle epoch supersedes the session. A newer replay attempt may update only `current`; it never erases `originating`.

## Replay disposition

Disposition comes from typed internal error semantics, never `err.Error()`, phase name alone, retry count, timeout, or `tun_health` alone.

- `terminal`: replay completed and is non-retryable for this attempt.
- `retryable`: a bounded transient may legitimately succeed in a newer recovery epoch without a new user lifecycle epoch.
- `interrupted`: cancellation, shutdown, package-replacement interruption, or lifecycle supersession ended the attempt. It never terminalizes by itself.
- `incomplete`: recovery/ownership/observation is insufficient to decide.

Reuse existing typed classifications where they already express these semantics. Add only one small private classifier/wrapper where needed. Cancellation and supersession must never become terminal merely because `Connect` returned an error.

An interrupted attempt is not a permanent retry blocker. While the interrupting lifecycle/context is active, no replacement replay is admitted. On a later fresh startup or explicit serialized recovery entry, if the same `SessionID` still exists with `intent=resume`, no lifecycle supersession is active, and pre-replay recovery prerequisites converge, the interrupted attempt may be superseded by exactly one new recovery epoch and one fresh replay. The interrupted attempt remains diagnostic evidence and can never authorize terminalization of that newer epoch.

`incomplete` remains fail-closed until the missing ownership/recovery/observation evidence becomes conclusive; it does not gain retry or terminal authority merely from elapsed time or repeated recovery calls.

## RecoveryEpoch admission semantics

`RecoveryEpoch` identifies an admitted replay attempt, not an arbitrary invocation of startup/recovery orchestration.

Move `BeginRecoveryAttempt()` out of the beginning of `resumeNetworkSession()`. It is called exactly once, immediately before admitting a new connect replay, after all of the following have already happened for the still-current Network Session:

1. current Network Session state and intent are loaded;
2. Privacy Envelope authority is reconciled;
3. exact old/candidate transaction recovery prerequisites have converged sufficiently to permit replay;
4. the existing generic recovery stage, where it owns distinct candidates, has converged sufficiently to permit replay;
5. already-persisted current-attempt evidence has been evaluated;
6. the operation has decided that a new replay is actually permitted (`no current attempt`, `retryable`, or a previously `interrupted` attempt entering a later fresh lifecycle/recovery entry).

Re-evaluating or terminalizing existing evidence for epoch `E` never increments the epoch. Running terminal convergence after a fenced `resume -> terminal` transition never increments the epoch. A pre-replay privacy/exact/generic blocker never increments the epoch merely because recovery was invoked.

The state returned by `BeginRecoveryAttempt()` is the exact `SessionID + RecoveryEpoch` identity bound to the new replay and its persisted evidence. No second increment is permitted for that replay.

## Cleanup-safety witness

Do not persist a new cleanup authority or a standalone `cleanup_safe` boolean.

After the existing exact old/candidate recovery stages run, derive one private in-memory terminalization witness from their typed results plus current exact ownership inspection. The witness is bound to the expected `SessionID` and `RecoveryEpoch` and is valid only while the already-existing serialized recovery/lifecycle operation remains owned.

The witness is positive only when all applicable exact authority has converged:

- no unresolved transaction recovery candidate remains;
- a candidate transaction either never mutated or its exact rollback converged;
- exact TUN address, routes, policy rules, DNS, and nftables state are absent/converged from durable authority;
- tracked old/candidate Xray child absence is proven from valid process/config ownership, never stale PID alone;
- terminal native `podlaz0` lifecycle is absent/converged;
- generated runtime config is absent/cleaned from exact authority;
- the same Network Session still owns the exact Privacy Envelope authority needed for terminal teardown.

A missing transaction file, missing `podlaz0`, inactive status, process-name match, timeout, or `systemd-resolved=unknown` is never sufficient by itself.

No new durable cleanup-proof file or package is introduced.

## Fenced `resume -> terminal` transition

Add one conditional method on `networkSessionStateStore` under the existing per-state mutation lock. The method receives the expected attempt identity, terminal replay evidence, and the in-memory cleanup-safety witness. Inside the same load-transition-validate-save boundary it verifies:

- current `SessionID` equals expected `SessionID`;
- current `RecoveryEpoch` equals expected `RecoveryEpoch`;
- current intent is `resume`;
- the consumed `current` replay evidence is `terminal` and matches the same session/epoch;
- the cleanup-safety witness matches that same session/epoch and was produced by the currently serialized recovery operation.

Only then is intent changed to `terminal` and durably saved.

Any mismatch returns a typed non-transition result with no mutation. There is no unlocked `Load()` followed by `SetIntent(terminal)` sequence. Terminal intent is durable before Privacy Envelope removal or Network Session authority clearing.

## Recovery orchestration

For `intent=resume`, while holding the existing serialized lifecycle/recovery operation ownership:

1. Load current Network Session state without incrementing `RecoveryEpoch`.
2. Reconcile current Privacy Envelope authority.
3. Converge exact old/candidate transaction recovery prerequisites.
4. Run the existing generic recovery stage only for its distinct candidates.
5. Load and evaluate current structured replay evidence before opening another replay attempt.
6. If current evidence is `terminal`, do not increment the epoch and do not replay. Derive the cleanup-safety witness. If positive, perform the fenced `resume -> terminal` transition for the same `SessionID/RecoveryEpoch` and immediately continue the existing terminal teardown in this same serialized operation. If the witness is not positive, remain fail-closed with the specific blocker.
7. If current evidence is `incomplete`, do not increment/replay solely because recovery was called again. Preserve fail-closed semantics until the missing evidence becomes conclusive.
8. If current evidence is `interrupted`, do nothing while the interrupting lifecycle/context is still active. On a later fresh startup/recover entry, if the same session remains current with `intent=resume` and replay prerequisites converge, it may admit one new replay epoch.
9. If current evidence is `retryable`, or there is no structured replay evidence, admit one new replay: call `BeginRecoveryAttempt()` exactly once immediately before `Connect`, then bind the replay to the returned `SessionID/RecoveryEpoch`.
10. If that replay succeeds, keep `intent=resume`, preserve the same logical Network Session and valid Privacy Envelope, clear resolved resume evidence, and release the startup gate.
11. If that replay fails, classify the failure and durably persist its structured evidence for the exact admitted `SessionID/RecoveryEpoch` before taking any disposition-dependent action.
12. If the just-persisted disposition is `terminal`, derive cleanup safety immediately. When positive, perform the fenced `resume -> terminal` transition and existing terminal teardown before returning from the same serialized startup/recover operation. A second `recover` call is not required merely to consume an already-proven terminal replay outcome.
13. If the just-persisted disposition is `retryable`, return the truthful retryable failure; a later recovery entry may admit a newer epoch. If it is `interrupted` or `incomplete`, follow the semantics above and never reinterpret it as terminal.

After terminal intent is durable, reuse the existing terminal path:

`terminal intent -> exact data-plane recovery/absence proof -> Privacy Envelope removal -> remaining host-network verification -> Network Session authority clear -> startup publication/gate release`.

No parallel cleanup path is added.

## Originating/current evidence rules

The first actionable structured replay failure for an unresolved Network Session becomes `originating` and is retained until resume succeeds, terminal convergence completes, or an explicit user lifecycle epoch legitimately supersedes the session.

For the first structured replay failure, `originating == current`.

A later admitted replay after a `retryable` or eligible prior `interrupted` attempt updates only `current`. `originating` remains immutable diagnostic evidence. Only `current`, when fenced to the exact current `SessionID/RecoveryEpoch`, may participate in terminalization eligibility.

Pre-replay privacy/exact/generic blockers never replace an existing `originating/current` replay record merely to report a newer observation.

## Network-apply attribution

Add one small typed apply-subphase marker at existing executor/application boundaries. Required values cover:

- TUN address;
- routes;
- policy rules;
- systemd-resolved DNS;
- nftables firewall.

The subphase is diagnostic only; it never decides disposition or cleanup authority. Do not persist raw command stderr or private profile/network data.

## Public recovery semantics

Keep the public surface minimal. Extend the bounded `network_session` recovery projection only with fields needed for truthful operator semantics:

- optional `replay_disposition`;
- optional `network_apply_subphase`.

Do not expose `SessionID` or `RecoveryEpoch` publicly.

`recover`, `status`, and `doctor` remain mutually consistent:

- retryable -> retry-resume;
- interrupted during active interruption -> no competing replay; later fresh eligible recovery may retry;
- incomplete/unsafe ownership -> fail-closed blocker/manual diagnosis as appropriate;
- terminal + fresh + cleanup-safe -> transition to terminal convergence without requiring another recovery request;
- success -> active `resume`, open gate, no terminal transition.

## Crash/restart safety

Cover these durable boundaries:

1. terminal replay evidence persisted, terminal intent not yet committed;
2. terminal intent committed, exact data-plane convergence incomplete;
3. exact data-plane convergence complete, Privacy Envelope still armed;
4. Privacy Envelope removed, Network Session authority still present;
5. Network Session authority cleared, publication/gate refresh pending.

At boundary 1, terminalization may continue only if the same `SessionID`, `RecoveryEpoch`, and `resume` intent still match. Re-evaluating this boundary does not increment `RecoveryEpoch`. Any newer epoch/session/intent makes the evidence stale and mutation-free.

Once terminal intent is committed, restart must never return the session to `resume`.

An interrupted replay persisted before daemon/package replacement is retryable only through a later fresh lifecycle/recovery entry that explicitly admits a new epoch after revalidating the same current session and replay prerequisites. Restart does not reinterpret the old interruption as terminal.

## Tests

Use TDD from the reproduced failure shape. Hosted tests cover:

- additive extended-v1 diagnostic round-trip while preserving all released top-level v1 fields and schema identifier;
- a `v0.2.41`-compatible decoder can read an extended-v1 fixture and ignore the added fields;
- the new reader treats a legacy/stripped v1 record without structured attempt identity as diagnostic-only, never terminalization authority;
- top-level compatibility projection mismatch with structured `current` fails closed;
- all four dispositions;
- cancellation/shutdown/supersession never terminal;
- an interrupted attempt cannot terminalize, does not start a competing replay while interruption is active, and may admit exactly one fresh `E+1` replay on a later eligible startup/recover entry;
- `BeginRecoveryAttempt()` is not called for terminal re-evaluation, terminal convergence, incomplete pre-replay blockers, or merely entering recovery;
- `BeginRecoveryAttempt()` is called exactly once immediately before each actually admitted replay;
- exact current `SessionID + RecoveryEpoch + resume` fencing;
- stale session, stale epoch, superseded intent, and legacy/stale diagnostic are mutation-free;
- cleanup-safety negatives: incomplete exact recovery, ownership ambiguity, rollback failed/unknown, unresolved transaction authority, unproven data-plane absence;
- a terminal failure returned by the replay admitted in the current call is persisted, fenced, terminalized, and sent through existing terminal teardown in that same serialized operation when cleanup safety is positive;
- no second `recover` is required solely to consume that just-produced terminal evidence;
- terminal positive path reuses existing teardown ordering and never removes Privacy Envelope early;
- originating/current evidence preservation across retryable/interrupted replay supersession;
- successful resume positive control, including stale older terminal evidence, with no `resume -> terminal` transition;
- all five crash/restart boundaries and repeated idempotent recovery;
- production-shaped startup and `/recover` paths;
- privacy-safe apply-subphase diagnostics.

The target-host acceptance scenario covers exact public `v0.2.40` active TUN -> candidate package replacement through the normal package lifecycle. It independently proves real DNS and IPv4 HTTPS/TLS before replacement, then requires either verified active continuity or, for a current terminal replay with proven cleanup safety, same-boot terminal convergence to ordinary networking. A terminal replay produced during candidate startup must converge in that same startup operation when safety is already proven; the harness must not need a second `recover` merely to trigger terminalization. No hidden second connect, package retry, manual state deletion, broad cleanup, or reboot may make the scenario pass.

## Scope

In scope:

- `internal/daemon/**` Network Session state/recovery/diagnostics and focused lifecycle classification;
- `internal/api/**` only for bounded public recovery fields required for truthful semantics;
- `internal/app/cli/**` and `docs/cli.md` only if rendering changes are required;
- `internal/recovery/**` only if an existing exact recovery result lacks a small reusable predicate needed for the witness;
- focused acceptance/E2E coverage and existing shared helpers where needed.

Out of scope:

- #315 reporting fixes;
- dependency upgrades;
- broad lifecycle refactoring;
- new recovery packages/subsystems;
- broad network cleanup or ownership expansion;
- unrelated connect/disconnect behavior.

## YAGNI guardrail

Prefer fields and private helpers in existing files. Create a new production source file only if adding the code to an existing file would mix a clearly separate responsibility or materially harm reviewability. Add no interface unless at least two production consumers need it or it represents an existing domain boundary. Temporary spec/plan files are removed before final repository verification, as required by `AGENTS.md`.
