# Resume Terminal Convergence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make failed package-restart TUN replay converge safely from `resume` to terminal in the same boot when replay terminality, attempt freshness, and exact cleanup safety are proven, while preserving fail-closed ownership and v0.2.41 diagnostic compatibility.

**Architecture:** Extend the existing v1 resume diagnostic additively with attempt-scoped replay evidence, move recovery-epoch advancement to the exact replay-admission point, and add one fenced state-store transition into the already-existing retained terminal teardown path. Startup and `/recover` share one operation-token-owned resume flow and consume a small private typed convergence result; no new package, cleanup store, or teardown subsystem is added.

**Tech Stack:** Go, Bash acceptance harness, GitHub Actions, existing Podlaz Network Session/recovery/TUN transaction primitives.

**Spec:** `docs/superpowers/specs/2026-09-10-resume-terminal-convergence-design.md`

## Global Constraints

- Preserve `podlaz.network-session-resume-diagnostic.v1` and released top-level v1 field semantics.
- Exact durable ownership remains the only cleanup authority; diagnostic evidence never grants cleanup authority.
- `RecoveryEpoch` advances exactly once per admitted connect replay, immediately before replay.
- Unknown/untyped replay failures classify as `incomplete`; cancellation/supersession classify as `interrupted`.
- Reuse existing exact transaction recovery, operation lock, Privacy Envelope, retained terminal convergence, and final session cleanup.
- Do not add dependencies, packages, a second teardown coordinator, or a durable cleanup-proof store.
- #315 reporting changes are out of scope.

---

### Task 1: Backward-readable attempt-scoped diagnostic evidence

**Files:**
- Modify: `internal/daemon/network_session_resume_diagnostic.go`
- Test: `internal/daemon/network_session_resume_log_test.go`
- Test: `internal/daemon/network_session_resume_diagnostics_test.go`

**Interfaces:**
- Produces private structured replay attempt evidence carrying `SessionID`, `RecoveryEpoch`, replay disposition, mutation outcome, failure phase/subphase, rollback status and existing bounded metadata.
- Keeps released top-level v1 fields as latest-blocker projection.

- [ ] Write RED tests proving extended v1 records remain readable by a v0.2.41-shaped decoder, pre-replay blockers update only top-level latest-blocker fields, structured `originating/current` survive, and stale top-level replay fields are cleared.
- [ ] Run PR CI and confirm the tests fail because structured evidence and clearing semantics do not exist.
- [ ] Implement the minimum additive v1 fields/store update helpers without a second file/store.
- [ ] Run focused/PR CI and make Task 1 green.

### Task 2: Replay disposition, mutation outcome, and apply subphase

**Files:**
- Modify: `internal/daemon/tun_failure_phase.go`
- Modify only where existing typed executor boundaries require it: `internal/daemon/tun_transaction.go`, `internal/daemon/tun_full_tunnel_runner.go`
- Test: existing TUN failure/diagnostic tests under `internal/daemon/**`

**Interfaces:**
- Produces total conservative replay disposition: terminal/retryable/interrupted/incomplete.
- Produces typed candidate mutation outcome: not-opened/rolled-back/unresolved.
- Produces privacy-safe apply subphase: tun-address/routes/policy-rules/dns/nftables.

- [ ] Write RED tests for typed terminal/transient/interrupted classification, unknown wrapped error -> incomplete, mutation outcome correctness, and structured apply subphase.
- [ ] Verify RED in CI.
- [ ] Add only the minimum private typed wrappers/classifier and subphase propagation at existing boundaries.
- [ ] Verify GREEN.

### Task 3: RecoveryEpoch admission and abandoned-admission crash semantics

**Files:**
- Modify: `internal/daemon/network_session_state.go`
- Modify: `internal/daemon/network_session_lifecycle.go`
- Test: `internal/daemon/network_session_resume_diagnostics_test.go` and focused Network Session recovery tests.

**Interfaces:**
- `BeginRecoveryAttempt()` remains the durable epoch increment but is invoked only immediately before actual replay.
- A state epoch newer than structured `current` is treated as an abandoned admitted replay, never as `not-opened` or terminal authority.

- [ ] Write RED tests proving no epoch increment for pre-replay blockers/terminal re-evaluation and exactly one increment immediately before replay.
- [ ] Write RED crash tests for persisted epoch with no structured outcome both before Connect and after possible transaction start.
- [ ] Verify RED.
- [ ] Move admission to the replay boundary and implement conservative abandoned-admission recovery: exact recovery first, then later fresh entry may admit one newer epoch.
- [ ] Verify GREEN.

### Task 4: Fenced terminalization and restart-reconstructible cleanup witness

**Files:**
- Modify: `internal/daemon/network_session_state.go`
- Modify: `internal/daemon/network_session_lifecycle.go`
- Reuse: `internal/daemon/network_session_terminal_recovery.go`
- Modify `internal/recovery/**` only if one small existing-result predicate is required.
- Test: focused Network Session terminal/restart tests.

**Interfaces:**
- One state-store method atomically validates current SessionID/RecoveryEpoch/resume + structured current terminal evidence + in-memory witness and persists terminal intent.
- Witness is reconstructed from structured mutation outcome + clean exact recovery + bounded exact read-only observation + current Privacy Envelope authority.

- [ ] Write RED tests for current/stale session, epoch, intent and evidence fencing; no mutation on mismatch.
- [ ] Write RED crash-boundary witness reconstruction tests for not-opened and rolled-back/completed; reject missing diagnostic, transaction-file absence alone, timeout/unknown and unresolved cleanup.
- [ ] Verify RED.
- [ ] Implement the minimum fenced transition and witness derivation/reconstruction, reusing exact recovery/inspection.
- [ ] Verify GREEN.

### Task 5: One operation token and typed resume convergence result

**Files:**
- Modify: `internal/daemon/lifecycle_operation_lock.go` only if a small existing-lock helper is needed.
- Modify: `internal/daemon/startup_runtime.go`
- Modify: `internal/daemon/network_session_lifecycle.go`
- Modify: `internal/daemon/network_session_recovery_result.go`
- Modify: `internal/daemon/http_server.go`
- Modify: `internal/daemon/boot_autostart_startup.go` only where caller result/finalization integration requires it.
- Test: startup, HTTP recovery, operation-lock, boot-autostart tests.

**Interfaces:**
- Private resume result with semantics equivalent to resumed/terminal-converged/no-session.
- Startup owns one lifecycle operation token across privacy/exact/generic recovery, replay, witness, transition and retained convergence, using unwrapped sessionLifecycle inside.

- [ ] Write RED tests proving startup does not nested-lock and competing lifecycle/recovery cannot interleave witness -> terminal transition.
- [ ] Write RED tests proving `/recover` never returns old `resume/succeeded` after terminal convergence.
- [ ] Write RED boot-autostart tests preserving `terminal session -> retained convergence -> attempt=terminal -> session clear` across failures/restarts.
- [ ] Verify RED.
- [ ] Replace ambiguous `(bool,error)` with the minimum private typed result and wire callers/finalization without changing unrelated lifecycle paths.
- [ ] Verify GREEN, including daemon race coverage if CI exposes it.

### Task 6: Public recovery projection and CLI contract

**Files:**
- Modify: `internal/api/network_session_recovery.go`
- Modify: `internal/daemon/network_session_recovery_plan.go`
- Modify: `internal/app/cli/recover_cli.go`
- Modify: `docs/cli.md`
- Tests: API validation, daemon plan, CLI recovery rendering tests.

**Interfaces:**
- Add only optional public `replay_disposition` and `network_apply_subphase` fields.
- Latest top-level blocker remains truthful; structured attempt identity stays private.

- [ ] Write RED API/CLI tests for the two optional fields and stale-field clearing on a newer pre-replay blocker.
- [ ] Verify RED.
- [ ] Implement minimal projection/validation/rendering and update canonical CLI documentation.
- [ ] Verify GREEN.

### Task 7: Exact v0.2.40 package-restart acceptance

**Files:**
- Modify the smallest existing focused acceptance/E2E scenario under `scripts/e2e/**` and/or `scripts/acceptance/release-laptop.sh` that owns lower-release package replacement.
- Modify shared `scripts/e2e/lib/**` only for mechanics shared by existing scenarios.
- Test: corresponding shell contract tests under `scripts/**/tests/**`.

**Interfaces:**
- Qualification PASS requires the exact public v0.2.40 historical restart-teardown failure boundary to be exercised, not merely a healthy lower-release upgrade.

- [ ] Write RED shell contract tests requiring evidence of old daemon package-restart shutdown + unsuccessful historical `missing nftables chains` teardown + replacement daemon start + preserved resume intent.
- [ ] Add RED assertions for pre-upgrade VPN traffic, post-resume VPN traffic, post-terminal ordinary traffic, and second clean/idempotent recovery.
- [ ] Verify RED via CI.
- [ ] Implement the minimum acceptance predicates and evidence capture without using historical evidence as cleanup authority.
- [ ] Verify GREEN.

### Task 8: Repository verification, scope review, and finalization

**Files:**
- Remove temporary `docs/superpowers/specs/2026-09-10-resume-terminal-convergence-design.md`
- Remove temporary `docs/superpowers/plans/2026-09-10-resume-terminal-convergence.md`
- Update PR body with exact validation performed/not performed.

- [ ] Run/obtain fresh CI evidence for formatting, `go test ./...`, `go vet ./...`, `govulncheck ./...`, repository-structure final, relevant shell/workflow/package tests, daemon race checks and relevant hosted E2E.
- [ ] Review full PR diff for unrelated changes, stale names, compatibility, privacy, authority separation, restart ordering and test quality.
- [ ] Remove temporary spec/plan files and re-run final repository-structure/CI checks.
- [ ] Record that destructive physical-host acceptance remains unexecuted unless an eligible dedicated-runner result exists; do not claim it passed without evidence.
- [ ] Mark PR ready only after hosted validation is green and the PR body accurately states physical validation status.
