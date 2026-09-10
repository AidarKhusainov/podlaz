# Resume Terminal Convergence Design

## Problem

A current-boot protected TUN Network Session may survive package replacement with `intent=resume` while the candidate replay fails after the old generation has already converged away. Today the daemon preserves Privacy Envelope protection and blocks ordinary lifecycle mutation, but it has no typed authority to decide that the failed replay is terminal for the exact current recovery attempt and that exact terminal convergence is now safe. Re-running recovery advances `RecoveryEpoch` and may overwrite the first actionable failure diagnostic, leaving the host indefinitely fail-closed even after the data plane is gone.

The fix must preserve fail-closed ownership semantics while adding a deterministic same-boot convergence path for the exact `v0.2.40 active -> candidate` package boundary. It must not create another cleanup subsystem or weaken the terminal recovery ordering added for exact TUN teardown.

## Design principles

- Reuse the existing Network Session state, serialized mutation boundary, exact transaction recovery, Privacy Envelope lifecycle, and persisted terminal teardown path.
- Keep replay disposition, attempt freshness, and terminal cleanup safety as separate predicates.
- Historical diagnostics remain evidence, never cleanup authority.
- Add the smallest typed state needed; do not expose raw `SessionID`, `RecoveryEpoch`, transaction identity, command stderr, profile identity, or endpoint data publicly.
- Do not retry destructive lifecycle operations merely to obtain a different diagnostic.
- No new package or parallel recovery state machine is introduced.

## Attempt-scoped replay evidence

Extend the private current-boot resume diagnostic into bounded attempt-scoped evidence. The persisted record remains mode `0600` and gains:

- `session_id` — private binding to the exact current Network Session;
- `recovery_epoch` — existing attempt sequence;
- `replay_disposition` — one of `terminal`, `retryable`, `interrupted`, `incomplete`;
- existing resume stage, TUN failure phase, rollback status, transaction-present evidence, and legacy-migration flag;
- optional privacy-safe `network_apply_subphase` with bounded values equivalent to TUN address, routes, policy rules, DNS, and nftables.

The record is diagnostic/evidentiary state only. `transaction_present` remains historical evidence and never becomes transaction cleanup authority.

The first actionable failure for an unresolved Network Session is preserved as originating evidence. A later retryable attempt may record latest-attempt evidence separately, but it must not erase the originating record or allow the older record to authorize mutation in the newer epoch.

Older diagnostic schema/state without a `SessionID` may still be read for operator diagnostics where safe, but it is never eligible to authorize `resume -> terminal`.

## Replay disposition

Replay disposition is derived from typed internal error semantics, not error strings, phase names, retry count, timeout, or `tun_health` alone.

- `terminal`: the replay completed and the failure is non-retryable for this recovery attempt.
- `retryable`: the failure is a bounded transient that may legitimately succeed in a newer recovery epoch without a new user lifecycle epoch.
- `interrupted`: cancellation, daemon shutdown, package replacement interruption, or explicit lifecycle supersession ended the attempt.
- `incomplete`: ownership, recovery, or observation is insufficient to decide.

Where existing typed error classifications already distinguish these semantics, reuse them. Add only the smallest private wrapper/classifier needed to fill gaps. Cancellation and supersession must never be promoted to terminal because `Connect` returned an error.

## Fenced `resume -> terminal` transition

Add one conditional method on `networkSessionStateStore` that executes under the existing per-state mutation lock. The transition accepts expected attempt identity and terminal-eligibility evidence and, inside the same load-transition-validate-save boundary, verifies:

- current `SessionID` equals the expected session;
- current `RecoveryEpoch` equals the expected epoch;
- current intent is `resume`;
- the consumed replay evidence is `terminal` and belongs to the same session/epoch;
- terminal cleanup safety has been established for that exact state.

Only then is intent changed to `terminal` and durably saved.

Any session, epoch, intent, evidence, or safety mismatch returns a typed non-transition result and performs no mutation. There is no unlocked `Load()` followed by `SetIntent(terminal)` sequence.

Terminal intent is persisted before Privacy Envelope removal or Network Session authority clearing.

## Terminal cleanup safety

Replay terminality does not imply cleanup safety. Eligibility requires current exact evidence that:

- old-generation exact recovery has converged;
- no current transaction recovery candidate remains unresolved;
- a failed candidate transaction either never mutated or its exact rollback converged;
- candidate/old transaction-owned TUN address, routes, policy rules, DNS, and nftables state are absent or exactly converged from durable authority;
- tracked old/candidate Xray child absence is proven from valid process/config ownership, never stale PID alone;
- native `podlaz0` lifecycle is converged/absent when terminal;
- generated runtime config is absent/cleaned according to exact durable authority;
- the current Network Session still owns exact Privacy Envelope authority required for terminal teardown.

Use the existing exact transaction recovery and ownership checks as the source of this proof. Do not infer safety from a missing transaction file, missing `podlaz0`, inactive status, or process-name matching alone.

## Recovery orchestration

For `intent=resume`:

1. Reconcile current Privacy Envelope authority.
2. Converge exact old-generation transaction recovery.
3. Converge the existing independent generic recovery stage only where it owns distinct candidates.
4. Evaluate persisted current-attempt evidence before opening a new replay attempt.
5. If current evidence is terminal and cleanup safety is proven, perform the fenced `resume -> terminal` transition without another replay.
6. If current evidence is retryable, begin a new `RecoveryEpoch` and run one replay attempt.
7. If current evidence is interrupted or incomplete, preserve fail-closed semantics and expose the truthful blocker; do not terminalize.
8. On successful replay, keep `intent=resume`, preserve the same logical Network Session, retain valid Privacy Envelope protection, clear resolved resume evidence, and release the startup gate.

After terminal intent is durable, reuse the existing terminal convergence path:

`terminal intent -> exact data-plane recovery/absence proof -> Privacy Envelope removal -> remaining host-network verification -> Network Session authority clear -> startup publication/gate release`.

Do not add a second teardown coordinator.

## Originating and latest failure evidence

The first actionable replay failure for an unresolved Network Session remains available until one of:

- resume succeeds;
- terminal convergence completes;
- an explicit user lifecycle epoch legitimately supersedes the session.

A terminal first failure is consumed directly for eligibility evaluation and is not replayed again merely to refresh diagnostics.

A retryable first failure may lead to a newer recovery epoch. The originating evidence remains diagnostic only; latest-attempt evidence is separately fenced to the newer session/epoch. Older evidence can never terminalize the newer attempt.

## Network-apply attribution

Add a small typed apply-subphase marker at the existing executor/application boundaries. Required values cover:

- TUN address;
- routes;
- policy rules;
- systemd-resolved DNS;
- nftables firewall.

This marker is diagnostic only. It must not be used as replay-disposition or cleanup-authority semantics. No raw command stderr or private network/profile data is persisted or exposed.

## Public recovery semantics

Keep the public surface minimal. Extend the bounded `network_session` recovery projection only where needed for truthful operator behavior:

- optional `replay_disposition`;
- optional `network_apply_subphase`.

Do not expose `SessionID` or `RecoveryEpoch` publicly.

`recover`, `status`, and `doctor` must remain mutually consistent:

- retryable -> retry-resume semantics;
- interrupted/incomplete/unsafe ownership -> fail-closed blocker/manual diagnosis as appropriate;
- terminal + fresh + cleanup-safe -> terminal convergence/continue-teardown semantics;
- success -> active `resume`, open gate, no terminal transition.

## Crash and restart safety

The protocol must be restart-safe at these durable boundaries:

1. terminal replay evidence saved, terminal intent not yet committed;
2. terminal intent committed, exact data-plane convergence incomplete;
3. exact data-plane convergence complete, Privacy Envelope still armed;
4. Privacy Envelope removed, Network Session authority still present;
5. Network Session authority cleared, final startup publication/gate refresh pending.

At boundary 1, terminalization may continue only if the same `SessionID`, `RecoveryEpoch`, and `resume` intent still match. Any newer epoch/session/intent makes the evidence stale and mutation-free.

Once terminal intent is committed, restart must never return the session to `resume`.

## Tests

Use TDD from the reproduced failure shape.

Hosted tests must cover:

- terminal, retryable, interrupted, and incomplete disposition;
- cancellation/shutdown/supersession never classified terminal;
- fenced transition accepts only exact current `SessionID + RecoveryEpoch + resume`;
- stale session, stale epoch, superseded intent, and stale diagnostic remain mutation-free;
- cleanup-safety negatives: incomplete old recovery, ownership ambiguity, candidate rollback failed/unknown, unresolved transaction authority, unproven data-plane absence;
- terminal positive path uses existing terminal convergence ordering and does not remove Privacy Envelope early;
- originating evidence preservation and latest retryable evidence separation;
- successful resume positive control, including stale older terminal evidence, with no `resume -> terminal` transition;
- all five crash/restart boundaries and repeated idempotent recovery;
- production-shaped `/recover` path and startup-gate behavior;
- privacy-safe apply-subphase diagnostics.

The target-host acceptance scenario must cover exact public `v0.2.40` active TUN -> candidate package replacement through the normal package lifecycle. It must independently prove real DNS and IPv4 HTTPS/TLS before replacement, then require either verified active continuity or, for a current terminal replay with proven cleanup safety, same-boot terminal convergence to ordinary networking. No hidden second connect, package retry, manual state deletion, broad cleanup, or reboot may make the scenario pass.

## Scope

In scope:

- `internal/daemon/**` Network Session recovery/state/diagnostics and focused lifecycle classification;
- `internal/api/**` only for bounded public recovery fields required for truthful semantics;
- `internal/app/cli/**` and `docs/cli.md` only if public recover/status rendering changes;
- directly relevant `internal/recovery/**` only if a reusable exact convergence predicate cannot be expressed from current results without changing ownership semantics;
- focused acceptance/E2E coverage and shared helpers where needed.

Out of scope:

- #315 acceptance-reporting truthfulness fixes;
- dependency upgrades;
- broad lifecycle refactoring;
- new recovery packages/subsystems;
- broad network cleanup or ownership expansion;
- changing unrelated connect/disconnect behavior.

## YAGNI guardrail

Prefer adding fields and private helpers to existing files over creating new abstractions. A new source file is justified only when one existing file would otherwise mix clearly separate responsibilities or become materially harder to review. No new interface is added unless at least two production consumers need the abstraction or it marks an existing domain boundary.
