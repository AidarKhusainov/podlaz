# Resume Terminal Convergence Design

## Problem

A protected current-boot TUN Network Session can survive package replacement with `intent=resume` while the candidate replay fails after the old generation has already converged away. The daemon then correctly remains fail-closed behind the Privacy Envelope, but today it has no exact protocol for deciding that the replay failure is terminal for the current recovery attempt, that the evidence is still fresh, and that terminal cleanup is safe. Repeated recovery may advance `RecoveryEpoch` and replace the original diagnostic, leaving the host indefinitely blocked even after the candidate data plane has been rolled back.

The fix must provide deterministic same-boot convergence for the exact `v0.2.40 active -> candidate` package boundary without weakening exact ownership or creating a second cleanup subsystem. It reuses the Network Session state/store, exact transaction recovery, lifecycle operation lock, Privacy Envelope lifecycle, and retained/final persisted terminal teardown paths added by the existing terminal recovery implementation.

## Constraints

- Replay disposition, attempt freshness, and cleanup safety are separate predicates.
- Evidence never becomes cleanup authority.
- Keep `podlaz.network-session-resume-diagnostic.v1`; do not introduce a v2 schema.
- Preserve released top-level v1 diagnostic semantics for same-boot downgrade compatibility.
- Do not expose `SessionID`, `RecoveryEpoch`, transaction identity, raw command stderr, profile identity, endpoint data, or generated config contents publicly.
- Do not add a package, parallel recovery state machine, cleanup-proof store, second teardown coordinator, or dependency.
- Do not retry destructive lifecycle operations merely to refresh diagnostics.
- Unknown or ambiguous evidence fails closed.

## Backward-readable additive v1 diagnostic

Keep the existing private mode-`0600`, bounded diagnostic file and schema identifier exactly:

`podlaz.network-session-resume-diagnostic.v1`.

Released `v0.2.41` accepts that schema and ignores unknown JSON fields, so the fixed candidate extends v1 only with optional fields.

### Top-level v1 latest-blocker projection

The existing top-level fields keep their released meaning: they describe the **latest resume failure or blocker from any resume stage**, not necessarily the latest connect replay:

- `recovery_epoch`;
- `resume_stage`;
- `last_resume_outcome`;
- optional `tun_failure_phase`;
- optional `rollback_status`;
- `transaction_present`;
- `legacy_migration`.

A later `privacy-reconcile`, `exact-recovery`, `generic-recovery`, or other pre-replay blocker may update those top-level fields exactly as `v0.2.41` does today so old readers and current status/recovery inspection continue to show the latest blocker.

The top-level projection is diagnostic only and is never replay terminalization authority. It is therefore **not required to match structured `current` replay evidence**.

Optional new top-level `replay_disposition` and `network_apply_subphase` describe the same latest top-level failure only when that failure has those semantics. When a newer pre-replay blocker updates the top-level projection, these optional fields are cleared unless they truthfully describe that same newer blocker. Older replay disposition/subphase remain only inside structured `originating/current`; they must never leak into a public projection for a different latest blocker.

### Structured replay evidence

Add optional `originating` and `current` structured replay-attempt records inside the same v1 file. They are updated only by an actually admitted connect replay, never by a pre-replay blocker.

Each structured attempt contains the minimum private fields required for fencing and crash reconstruction:

- `session_id`;
- `recovery_epoch`;
- `replay_disposition`: `terminal`, `retryable`, `interrupted`, or `incomplete`;
- resume stage and TUN failure phase;
- optional privacy-safe `network_apply_subphase`;
- rollback status;
- transaction-present diagnostic evidence;
- legacy-migration flag;
- typed candidate mutation outcome sufficient to distinguish `not-opened`, `rolled-back`, and `unresolved` without inferring it from error text or phase names.

`originating` is the first actionable replay failure for the unresolved Network Session. `current` is the latest actually admitted replay attempt that durably published structured outcome evidence. For the first structured failure, `originating == current`. A later replay after a retryable or eligible interrupted attempt updates only `current`; `originating` remains immutable diagnostic evidence until resume succeeds, terminal convergence completes, or an explicit user lifecycle epoch supersedes the session.

Only structured `current`, fenced to the exact current `SessionID + RecoveryEpoch`, may participate in replay terminalization eligibility. Top-level v1 fields and `originating` are never mutation authority.

Existing v1 diagnostics, or a record rewritten by downgraded `v0.2.41` that has lost the added structured fields, remain readable as latest-blocker diagnostics but are never eligible to authorize `resume -> terminal`.

`transaction_present` remains historical evidence only. Current transaction cleanup authority still comes only from exact current recovery candidates/durable transaction state.

## Replay disposition

Disposition is a total, conservative typed classification.

- `terminal`: allowed only by a positive typed non-retryable classification for the admitted replay.
- `retryable`: allowed only by a positive typed transient classification that may legitimately succeed in a newer recovery epoch without a new user lifecycle epoch.
- `interrupted`: parent cancellation, daemon shutdown, package replacement interruption, explicit lifecycle supersession, or a durably admitted replay that was abandoned by process loss before publishing an outcome. It never terminalizes by itself.
- `incomplete`: every unknown, unsupported, untyped, contradictory, or otherwise ambiguous failure.

Do not infer disposition from `err.Error()`, phase name alone, retry count, timeout, `tun_health`, one failed OS command, or rollback status alone. Reuse existing typed errors/classifications where they already provide the required semantics; add only the smallest private wrapper/classifier for missing cases.

An interrupted attempt is not a permanent retry blocker. No replacement replay is admitted while the interrupting lifecycle/context remains active. On a later fresh startup or serialized recovery entry, if the same Network Session still exists with `intent=resume`, no supersession is active, and pre-replay prerequisites converge, exactly one new recovery epoch and replay may be admitted. The old interrupted attempt remains diagnostic evidence and can never terminalize the newer attempt.

An incomplete attempt remains fail-closed until missing evidence becomes conclusive. Repeated calls or elapsed time alone do not turn it into retryable or terminal.

## RecoveryEpoch admission semantics

`RecoveryEpoch` identifies an **admitted connect replay attempt**, not an arbitrary startup/recovery invocation.

Move `BeginRecoveryAttempt()` out of the beginning of `resumeNetworkSession()`. It is called exactly once immediately before a new connect replay, only after:

1. current Network Session state/intent is loaded;
2. Privacy Envelope authority is reconciled;
3. exact old/candidate recovery prerequisites have converged sufficiently for replay;
4. generic recovery, where it owns distinct candidates, has converged sufficiently for replay;
5. existing structured replay evidence and any abandoned admission have been evaluated;
6. the operation has decided that a new replay is permitted because there is no prior admitted replay, the current structured attempt is retryable, or a prior interrupted/abandoned attempt is being superseded on a later fresh lifecycle entry.

Terminal re-evaluation of epoch `E`, terminal convergence, pre-replay blockers, and merely entering recovery never increment the epoch. The `networkSessionState` returned by `BeginRecoveryAttempt()` is the exact `SessionID + RecoveryEpoch` bound to that admitted replay.

### Crash after replay admission but before structured outcome

The `RecoveryEpoch` increment itself is the durable replay-admission marker. There is an intentional crash window after `BeginRecoveryAttempt()` has durably saved epoch `E` and before structured replay outcome evidence for `E` has been persisted.

If startup/recovery observes:

```text
current Network Session RecoveryEpoch = E
structured current is absent or fenced to an older epoch
```

then epoch `E` is an **admitted replay without a durable outcome**.

That state has these exact semantics:

- it is never terminalization authority;
- missing structured evidence must never be interpreted as candidate mutation `not-opened`;
- an older structured `current` cannot authorize mutation for epoch `E`;
- no new replay epoch is admitted until exact transaction/runtime recovery for the abandoned attempt has run first;
- any exact transaction/runtime residue from the admitted attempt is converged only through existing durable ownership authority;
- if exact recovery or ownership remains incomplete/ambiguous, remain fail-closed and do not increment again;
- after exact recovery prerequisites conclusively converge, if the same `SessionID` and `intent=resume` are still current and no supersession is active, the abandoned admission is deterministically treated as `interrupted` for retry-admission purposes and one later fresh lifecycle entry may admit exactly one newer epoch.

This rule applies whether the crash occurred before `Connect` executed any child code or after `Connect` had already opened transaction/runtime mutation. The product never distinguishes those cases from missing diagnostic evidence alone; exact recovery determines whether residue exists and what can safely be converged.

No synthetic `not-opened` evidence is created for an abandoned admission. The epoch mismatch/absence is sufficient to fence it from terminalization and to require recovery-before-retry.

## Durable cleanup-safety reconstruction

Do not persist a new cleanup authority, cleanup-proof record, or standalone `cleanup_safe` boolean.

During the same process execution, cleanup safety may be represented by a private in-memory witness derived from exact recovery and read-only observation. After a crash, that witness must be deterministically reconstructible from existing durable evidence.

### Candidate replay evidence

For an admitted replay that did publish structured outcome evidence:

- typed mutation outcome `rolled-back` together with `rollback_status=completed` means the replay's exact candidate rollback protocol completed successfully. This is durable **evidence**, not cleanup authority;
- typed mutation outcome `not-opened` means the candidate replay never opened data-plane mutation. This must come from a typed control-flow boundary, not from `transaction_present=false`, missing structured evidence, or failure phase inference;
- `unresolved`, `rollback_status=failed`, `rollback_status=unknown`, missing structured evidence for the current admitted epoch, or contradictory evidence can never prove cleanup safety.

The implementation must only persist `rolled-back/completed` after the existing rollback path has completed all resources it owns, including the tracked child and generated runtime config where applicable. The current transaction implementation removes durable transaction state only after its exact cleanup sequence has converged; absence of that file later is therefore supporting observation, not the sole proof.

### Reconstructing the witness after restart

For crash boundary `terminal replay evidence persisted -> terminal intent not yet committed`, the new process reconstructs cleanup safety from all of:

1. structured `current` evidence for the exact current `SessionID + RecoveryEpoch` proving either candidate mutation `not-opened` or exact candidate rollback `rolled-back/completed`;
2. a fresh exact recovery scan showing no unresolved current/old transaction recovery candidates or ownership warnings;
3. fresh bounded read-only observation proving the applicable terminal data-plane postconditions from exact known identities/authority: TUN address/routes/policy rules/DNS/nftables absent or converged, tracked candidate/old child absent from valid process/config identity, terminal native `podlaz0` lifecycle converged, and generated runtime config absent/cleaned;
4. the same current Network Session still carrying exact Privacy Envelope authority required for terminal teardown.

An abandoned admitted epoch with no structured outcome can never satisfy item 1 and therefore can never be reconstructed into terminal cleanup eligibility. It must follow the recovery-before-retry rule above instead.

A missing transaction file, missing `podlaz0`, inactive status, process-name match, timeout, or `systemd-resolved=unknown` is never sufficient by itself. If any required reconstruction input is unavailable or ambiguous, the witness is unavailable and terminalization is forbidden.

The witness is bound to the expected `SessionID + RecoveryEpoch` and is consumed only while the lifecycle operation token that produced/reconstructed it remains owned.

## Fenced `resume -> terminal` transition

Add one conditional method on `networkSessionStateStore` under the existing per-state mutation lock. It receives expected attempt identity, structured terminal replay evidence, and the cleanup-safety witness. Inside one load-transition-validate-save boundary it verifies:

- current `SessionID` equals expected `SessionID`;
- current `RecoveryEpoch` equals expected `RecoveryEpoch`;
- current intent is `resume`;
- structured `current` evidence is `terminal` for exactly that session/epoch;
- the cleanup-safety witness is positive for exactly that session/epoch and was derived while the current serialized lifecycle operation is owned.

Only then is intent changed to `terminal` and durably saved. Any mismatch is a typed no-transition result with no cleanup or protection mutation. There is no unlocked `Load()` followed by `SetIntent(terminal)`.

Terminal intent is durable before Privacy Envelope removal or Network Session clearing.

## One operation token for the complete resume flow

The cleanup-safety witness and fenced state transition must not be separated by another lifecycle mutation.

`/recover` already satisfies this with `runRecoveryWithFollowUp()`, which keeps generic recovery plus resume follow-up under one mutation registration and one operation token.

Startup continuation must gain the same property. The startup caller acquires one existing `lifecycleOperationLock` mutation registration/token **before** entering privacy reconciliation/exact recovery/generic recovery/evidence evaluation and retains it through replay, witness construction, fenced transition, and the appropriate convergence result.

While that token is held, startup passes the **unwrapped `runtime.sessionLifecycle`** into `resumeNetworkSession`; it must not call `runtime.lockedLifecycle`, because that wrapper would try to acquire the same non-reentrant operation token around `Connect` and deadlock.

Use the existing operation-lock primitives or one small private helper on that existing type. Do not introduce a second startup-specific lock/authority model.

Tests must prove a competing connect/disconnect/recover cannot interleave between witness construction and `resume -> terminal`, and must prove startup does not nested-lock when replay calls `Connect`.

## Typed convergence result

Replace the ambiguous `(bool, error)` contract of `resumeNetworkSession` with one small private typed result representing its semantic outcome. The minimum states are equivalent to:

- `resumed` — protected replay succeeded; the Network Session remains `intent=resume` and active;
- `terminal-converged` — terminal intent is durable and terminal data-plane/protection convergence succeeded while retained Network Session authority still exists for caller finalization;
- `no-session` — there is no current Network Session continuation to finalize.

Errors remain separate and represent incomplete/blocked convergence.

This result is required because callers have different durable finalization responsibilities and because `/recover` must not convert a terminal convergence into the old `intent=resume / succeeded` projection.

### Retained terminal authority

When `resumeNetworkSession` terminalizes a replay, it reuses the existing **retained** terminal convergence path (`convergePersistedNetworkSessionTeardown` semantics): exact terminal data-plane convergence, Privacy Envelope removal, and remaining-host verification complete, but the terminal Network Session record is retained until its caller commits any higher-level durable outcome.

Callers then finalize as follows:

- ordinary startup continuation with no in-progress boot attempt: clear the retained converged Network Session, then publish terminal/no-session convergence;
- `/recover`: clear the retained converged Network Session inside the same operation token and return a truthful terminal/disconnected recovery result, never an old cloned `resume/succeeded` plan;
- boot autostart with `attempt=in_progress`: preserve the established ordering exactly: `terminal Network Session -> retained terminal convergence -> attemptStore.MarkTerminal(...) -> Network Session finalize`. If `MarkTerminal` fails, retained terminal Network Session authority remains for restart continuation.

A crash after durable terminal intent but before higher-level finalization therefore cannot return to replay/resume semantics.

The private typed result is one existing domain boundary consumed by startup/boot-autostart and `/recover`; it does not justify a new package or public API type.

## Recovery orchestration

For `intent=resume`, under one lifecycle operation token:

1. Load current Network Session without incrementing `RecoveryEpoch`.
2. Reconcile Privacy Envelope authority.
3. Converge exact current/old transaction recovery prerequisites.
4. Run generic recovery only for distinct candidates.
5. If steps 2-4 fail, update the top-level v1 latest-blocker projection without altering structured `originating/current`; clear top-level `replay_disposition` and `network_apply_subphase` unless they describe that same blocker.
6. Compare the durable Network Session epoch to structured `current`. If the current epoch has no matching structured outcome, treat it as an abandoned admitted replay: never terminalize it, complete exact recovery first, and only a later fresh eligible entry may admit the next epoch.
7. Otherwise evaluate matching structured `current` replay evidence.
8. If `current=terminal`, do not increment or replay. Reconstruct/derive cleanup safety; if positive, perform the fenced `resume -> terminal`, run retained terminal convergence, and return `terminal-converged`. Otherwise remain fail-closed with the current blocker.
9. If `current=incomplete`, do not increment/replay merely because recovery was called again.
10. If `current=interrupted`, admit nothing while interruption is active; on a later eligible lifecycle entry it may be superseded by one new epoch.
11. If `current=retryable`, or there has never been an admitted replay, call `BeginRecoveryAttempt()` exactly once immediately before `Connect` and bind the replay to the returned session/epoch.
12. On replay success, keep `intent=resume`, preserve the same logical session/protection, clear resolved replay evidence as appropriate, and return `resumed`.
13. On replay failure, classify it conservatively and durably write the latest top-level blocker plus structured evidence for that admitted session/epoch.
14. If the newly persisted disposition is `terminal`, derive cleanup safety immediately. When positive, perform fenced `resume -> terminal` plus retained terminal convergence **before returning from the same serialized startup/recover operation**. A second `recover` call is not required just to consume an already-proven terminal outcome.
15. `retryable`, `interrupted`, and `incomplete` failures return their truthful blocked semantics without terminalization.

## Public recovery semantics

Extend the existing bounded public `network_session` projection with only:

- optional `replay_disposition`;
- optional `network_apply_subphase`.

Do not expose `SessionID` or `RecoveryEpoch`.

`docs/cli.md` must be updated because it already defines the stable `network_session` recovery fields.

`status`, `doctor`, and `recover` must remain mutually consistent:

- latest top-level blocker remains visible even when older structured replay evidence is preserved;
- top-level disposition/subphase never describe an older replay when the latest blocker is a different stage;
- abandoned admitted replay -> recovery-before-retry, never terminalization and never inferred `not-opened`;
- retryable -> retry-resume;
- interrupted while interruption is active -> no competing replay; a later eligible lifecycle entry may retry;
- incomplete/unsafe ownership -> fail-closed manual diagnosis/blocker;
- terminal + fresh + cleanup-safe -> terminal convergence in the same operation;
- terminal-converged -> conclusively disconnected/no Network Session after caller finalization, never `resume/succeeded`;
- successful replay -> active `resume`, open gate, no terminal transition.

## Network-apply attribution

Add one small typed apply-subphase marker at existing executor/application boundaries, with bounded semantics equivalent to:

- TUN address;
- routes;
- policy rules;
- systemd-resolved DNS;
- nftables firewall.

It is diagnostic only and never determines disposition or cleanup authority. Do not parse raw error text or persist raw stderr/private network data.

## Crash/restart safety

Cover these durable boundaries:

0. replay epoch admitted by `BeginRecoveryAttempt()`, but no structured outcome for that epoch has been persisted yet;
1. terminal structured replay evidence persisted, terminal intent not committed;
2. terminal intent committed, exact data-plane convergence incomplete;
3. exact data-plane convergence complete, Privacy Envelope still armed;
4. Privacy Envelope removed, retained Network Session authority still present;
5. Network Session cleared, startup publication/gate finalization pending.

At boundary 0, absence of structured outcome means abandoned admitted replay, never `not-opened` and never terminalization authority. Restart must perform exact transaction/runtime recovery first. If the same session/resume intent remains current and exact prerequisites converge, a later fresh lifecycle entry may treat the abandoned admission as interrupted and admit exactly one newer epoch.

At boundary 1, the witness must be reconstructed as specified above. Re-evaluation does not increment `RecoveryEpoch`. Any newer session/epoch/intent makes old evidence stale and mutation-free.

Once terminal intent is committed, restart must never return to `resume`.

For an interrupted attempt, restart may admit a newer replay only through a fresh lifecycle entry after revalidating the same current session and replay prerequisites; interruption is never reclassified as terminal.

For boot autostart, a crash/failure between retained terminal convergence and `attempt=terminal` persistence must preserve terminal Network Session authority so the next daemon continues terminal finalization rather than replaying the boot attempt.

## Tests

Use TDD from the reproduced failure shape. Hosted tests must cover:

- extended-v1 diagnostic remains readable by a `v0.2.41`-compatible decoder;
- top-level v1 fields retain latest-blocker semantics independently of structured `current`;
- pre-replay blockers update top-level fields without overwriting `originating/current`, and clear stale top-level replay disposition/subphase;
- stripped/legacy v1 records are diagnostic-only and never terminalization authority;
- all four replay dispositions, including an unknown/untyped wrapper -> `incomplete`;
- cancellation/shutdown/supersession -> `interrupted`, never terminal;
- interrupted attempt does not compete with the active interrupt and may admit exactly one fresh `E+1` replay on a later eligible entry;
- `BeginRecoveryAttempt()` is not called for terminal re-evaluation, terminal convergence, incomplete/pre-replay blockers, or merely entering recovery;
- `BeginRecoveryAttempt()` is called exactly once immediately before every actually admitted replay;
- crash after `BeginRecoveryAttempt()` but before `Connect`/structured outcome: restart never terminalizes or infers `not-opened`, converges prerequisites, and only then permits one fresh newer epoch;
- crash after `BeginRecoveryAttempt()` with `Connect`/transaction possibly started but before structured failure evidence: restart runs exact transaction/runtime recovery first, never infers mutation absence from missing diagnostic, and only after convergence permits a fresh replay;
- exact `SessionID + RecoveryEpoch + resume` fenced transition accepts current evidence and rejects stale session, stale epoch, superseded intent, legacy evidence, abandoned admission, and stale current evidence without mutation;
- cleanup-safety negatives for incomplete exact recovery, ownership ambiguity, candidate mutation unresolved, rollback failed/unknown, current recovery candidates, and unproven read-only absence;
- crash-boundary-1 witness reconstruction from `not-opened` or `rolled-back/completed` structured evidence + clean exact recovery scan + fresh exact observation;
- no witness is reconstructed from transaction-file absence, `transaction_present=false`, phase name, timeout, missing structured outcome, or missing `podlaz0` alone;
- terminal replay returned by the current call persists evidence and, when cleanup safety is positive, reaches fenced terminalization and retained terminal convergence in the same operation;
- no second recovery request is needed solely to consume that terminal evidence;
- the existing terminal ordering keeps Privacy Envelope until exact data-plane convergence is proven;
- private convergence result distinguishes `resumed`, `terminal-converged`, and `no-session` and `/recover` never projects terminal convergence as `resume/succeeded`;
- boot-autostart terminalization preserves `terminal session -> retained convergence -> attempt=terminal -> session clear`, including failure/restart between each durable step;
- startup owns one operation token for the entire resume flow, uses unwrapped `sessionLifecycle`, does not nested-lock, and rejects/interlocks competing mutations through the existing operation lock;
- successful replay remains transparent, including stale older terminal evidence, with no `resume -> terminal` transition;
- originating/current evidence survives retryable/interrupted supersession correctly;
- all six crash/restart boundaries are idempotent;
- public recovery projection and CLI rendering include the two new optional bounded fields;
- network-apply subphase is privacy-safe and structured.

## Target-host acceptance

The focused physical scenario must use the exact public `v0.2.40` package and exact candidate through the normal package lifecycle with exact package/runtime provenance.

This is specifically a qualification of the historical package-restart failure boundary, not merely a healthy lower-release upgrade. The focused #314 scenario **must not PASS unless the exact historical `v0.2.40` restart-teardown defect is actually exercised while reconnect intent is preserved**.

Required flow:

1. prove clean baseline and ordinary system DNS + IPv4 TCP/TLS/HTTPS;
2. install exact `v0.2.40`, establish product-verified active TUN, then independently prove bounded **system DNS + IPv4 HTTPS/TLS through the VPN** immediately before replacement;
3. capture immutable pre-replacement package/runtime/process and reconnect-intent evidence needed to identify the source generation;
4. install the candidate once through normal package lifecycle; no hidden second connect, package retry, service restart, manual state deletion, broad cleanup, or reboot may repair the scenario;
5. require bounded immutable acceptance evidence that the exact public `v0.2.40` daemon entered the known package-restart teardown failure class while reconnect intent was preserved, at minimum equivalent to: old daemon entered package-restart shutdown, the old shutdown was unsuccessful, the historical `missing nftables chains` teardown class was observed, the candidate replacement daemon started, and current-boot Network Session authority still represented `resume` continuation;
6. only after step 5 is proven may this run count as exercising #314. If the old release stops cleanly, record the run as useful compatibility evidence but not as PASS evidence for closure of #314;
7. if candidate replay succeeds, require verified active continuity and independently prove bounded **system DNS + IPv4 HTTPS/TLS through the candidate VPN**;
8. if the current replay is typed terminal and cleanup safety is proven, require same-operation/same-boot terminal convergence and independently prove bounded **system DNS + IPv4 HTTPS/TLS through ordinary host networking**;
9. after terminal convergence, run a second `recover --execute --yes` and require it to be clean, idempotent, and mutation-free;
10. if evidence is stale or ownership/cleanup remains ambiguous, require truthful fail-closed/manual-diagnosis semantics and forbid unsafe terminalization;
11. preserve unrelated NetworkManager/Docker/libvirt/nftables state semantically unchanged.

Because the immutable source is an old public release, bounded journal/process evidence may identify this known historical failure class **for acceptance only**. Such journal text/process observations are never product cleanup authority and must not be reused by production recovery logic.

The scenario must also prove Privacy Envelope ordering, absence of current transaction/Network Session cleanup authority after terminal completion, and no unnecessary terminalization after successful resume.

## Scope and YAGNI

In scope:

- `internal/daemon/**` Network Session state/recovery/diagnostics, existing operation-lock wiring, and focused typed classification;
- `internal/api/**` only for the two bounded public recovery fields;
- `internal/app/cli/**` as required to render the public fields consistently;
- `docs/cli.md` mandatory for the changed stable recovery projection;
- `internal/recovery/**` only if an existing exact recovery result lacks a small predicate needed to reconstruct the witness;
- focused acceptance/E2E coverage and existing shared helpers where needed.

Out of scope:

- #315 reporting fixes;
- dependency upgrades;
- broad lifecycle refactoring;
- new recovery packages/subsystems;
- another durable cleanup/evidence store;
- broad network cleanup or ownership expansion;
- unrelated connect/disconnect behavior.

Prefer fields and private helpers in existing files. Create a production source file only when an existing file would otherwise mix a clearly separate responsibility or become materially harder to review. The private convergence result is justified by the existing startup/boot-autostart and `/recover` consumers; do not generalize it beyond that boundary. Add no other interface unless at least two production consumers need it or it represents an existing domain boundary. Temporary spec/plan files are removed before final repository verification as required by `AGENTS.md`.