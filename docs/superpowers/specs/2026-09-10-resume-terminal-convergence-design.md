# Resume Terminal Convergence Design

## Problem

A current-boot protected TUN Network Session may survive package replacement with `intent=resume` while the candidate replay fails after the old generation has already converged away. Today the daemon preserves Privacy Envelope protection and blocks ordinary lifecycle mutation, but it has no typed authority to decide that the failed replay is terminal for the exact current recovery attempt and that exact terminal convergence is now safe. Re-running recovery advances `RecoveryEpoch` and may overwrite the first actionable failure diagnostic, leaving the host indefinitely fail-closed even after the data plane is gone.

The fix must preserve fail-closed ownership semantics while adding a deterministic same-boot convergence path for the exact `v0.2.40 active -> candidate` package boundary. It must reuse, not duplicate, the terminal recovery ordering already implemented for exact TUN teardown.

## Constraints

- Reuse the existing Network Session state, serialized mutation boundary, exact transaction recovery, Privacy Envelope lifecycle, and persisted terminal teardown path.
- Keep replay disposition, attempt freshness, and terminal cleanup safety as separate predicates.
- Historical diagnostics remain evidence, never cleanup authority.
- Do not expose raw `SessionID`, `RecoveryEpoch`, transaction identity, command stderr, profile identity, or endpoint data publicly.
- Do not retry destructive lifecycle operations merely to obtain a different diagnostic.
- Do not add a package, recovery subsystem, second teardown coordinator, or dependency.

## Attempt-scoped replay evidence

Keep one private current-boot resume diagnostic file. Evolve it to a new schema that contains two bounded attempt records rather than introducing a second store:

- `originating` — the first actionable replay failure for the unresolved Network Session;
- `current` — the latest attempt evidence, equal to `originating` until an explicitly retryable attempt advances to a newer recovery epoch.

Each attempt record contains:

- private `session_id`;
- `recovery_epoch`;
- `replay_disposition`: `terminal`, `retryable`, `interrupted`, or `incomplete`;
- resume stage;
- TUN failure phase;
- rollback status;
- transaction-present diagnostic evidence;
- legacy-migration flag;
- optional privacy-safe `network_apply_subphase`.

The file remains mode `0600` and bounded. Existing v1 diagnostic data may be read as legacy diagnostic evidence, but because it lacks `SessionID` and disposition it is never mutation authority for `resume -> terminal`.

`transaction_present` remains historical evidence only. Current transaction cleanup authority continues to come from exact recovery candidates/durable transaction state.

The originating attempt is retained until resume succeeds, terminal convergence completes, or an explicit user lifecycle epoch supersedes the session. A newer retryable attempt may update only `current`; it never erases `originating`.

## Replay disposition

Disposition comes from typed internal error semantics, never `err.Error()`, phase name alone, retry count, timeout, or `tun_health` alone.

- `terminal`: replay completed and is non-retryable for this attempt.
- `retryable`: a bounded transient may legitimately succeed in a newer recovery epoch without a new user lifecycle epoch.
- `interrupted`: cancellation, shutdown, package-replacement interruption, or lifecycle supersession ended the attempt.
- `incomplete`: recovery/ownership/observation is insufficient to decide.

Reuse existing typed classifications where they already express these semantics. Add only one small private classifier/wrapper where needed. Cancellation and supersession must never become terminal merely because `Connect` returned an error.

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

For `intent=resume`:

1. Reconcile current Privacy Envelope authority.
2. Converge exact old-generation transaction recovery.
3. Run the existing generic recovery stage only for its distinct candidates.
4. Load the current attempt evidence before opening another replay attempt.
5. If current evidence is terminal, derive the cleanup-safety witness. If positive, perform the fenced `resume -> terminal` transition without another replay. If not positive, remain fail-closed.
6. If current evidence is retryable, begin one newer `RecoveryEpoch` and run one replay attempt.
7. If current evidence is interrupted or incomplete, keep fail-closed semantics and expose the blocker; do not terminalize.
8. If there is no authoritative current attempt evidence, begin one recovery epoch and run one replay attempt.
9. On replay success, keep `intent=resume`, preserve the same logical Network Session and valid Privacy Envelope, clear resolved resume evidence, and release the startup gate.

After terminal intent is durable, immediately reuse the existing terminal path:

`terminal intent -> exact data-plane recovery/absence proof -> Privacy Envelope removal -> remaining host-network verification -> Network Session authority clear -> startup publication/gate release`.

No parallel cleanup path is added.

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
- interrupted/incomplete/unsafe ownership -> fail-closed blocker/manual diagnosis as appropriate;
- terminal + fresh + cleanup-safe -> transition to terminal convergence;
- success -> active `resume`, open gate, no terminal transition.

## Crash/restart safety

Cover these durable boundaries:

1. terminal replay evidence persisted, terminal intent not yet committed;
2. terminal intent committed, exact data-plane convergence incomplete;
3. exact data-plane convergence complete, Privacy Envelope still armed;
4. Privacy Envelope removed, Network Session authority still present;
5. Network Session authority cleared, publication/gate refresh pending.

At boundary 1, terminalization may continue only if the same `SessionID`, `RecoveryEpoch`, and `resume` intent still match. Any newer epoch/session/intent makes the evidence stale and mutation-free.

Once terminal intent is committed, restart must never return the session to `resume`.

## Tests

Use TDD from the reproduced failure shape. Hosted tests cover:

- all four dispositions;
- cancellation/shutdown/supersession never terminal;
- exact current `SessionID + RecoveryEpoch + resume` fencing;
- stale session, stale epoch, superseded intent, and legacy/stale diagnostic are mutation-free;
- cleanup-safety negatives: incomplete exact recovery, ownership ambiguity, rollback failed/unknown, unresolved transaction authority, unproven data-plane absence;
- terminal positive path reuses existing teardown ordering and never removes Privacy Envelope early;
- originating/current evidence preservation across retryable attempts;
- successful resume positive control, including stale older terminal evidence, with no `resume -> terminal` transition;
- all five crash/restart boundaries and repeated idempotent recovery;
- production-shaped startup and `/recover` paths;
- privacy-safe apply-subphase diagnostics.

The target-host acceptance scenario covers exact public `v0.2.40` active TUN -> candidate package replacement through the normal package lifecycle. It independently proves real DNS and IPv4 HTTPS/TLS before replacement, then requires either verified active continuity or, for a current terminal replay with proven cleanup safety, same-boot terminal convergence to ordinary networking. No hidden second connect, package retry, manual state deletion, broad cleanup, or reboot may make the scenario pass.

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
