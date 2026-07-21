# TUN Lifecycle Convergence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make post-apply TUN failures diagnosable before cleanup, make systemd-resolved cleanup idempotent for a validated missing podlaz link, and ensure all published lifecycle views converge after rollback or recovery.

**Architecture:** Keep ownership validation in the existing planner/executor/recovery boundaries. Split post-mutation failure preparation from rollback so the full-tunnel runner can persist a bounded redacted report before cleanup, then complete rollback with an uncancelled bounded context and update the historical report with the final rollback state. Preserve the existing `ResolvedDNSExecutor` configuration contract and add production-composition/package regression coverage rather than adding a second verifier.

**Tech Stack:** Go 1.26.5, Linux `systemd-resolved`/`resolvectl`, existing transaction store, existing `tundiag` schema/store, GitHub Actions package and disposable-host validation.

## Global Constraints

- Never use real endpoints, domains, local addresses, SSIDs, profile identifiers, or secrets in tests, logs, docs, issues, or PR text.
- Do not add adaptive DNS or MTU behavior.
- Do not write `/etc/resolv.conf`, restart `systemd-resolved`, broaden privileges, or weaken ownership validation.
- Diagnostics must be bounded, centrally redacted, and best-effort; cleanup remains mandatory.
- Rollback must use `context.WithoutCancel` plus the existing 20-second cleanup timeout.
- A successful rollback or recovery must remove the completed transaction candidate and permit an immediate retry.

---

### Task 1: Type the post-mutation transaction failure boundary

**Files:**
- Modify: `internal/daemon/tun_transaction.go`
- Modify: `internal/daemon/tun_transaction_rollback.go`
- Modify: `internal/daemon/tun_full_tunnel_runner.go`
- Test: `internal/daemon/tun_network_failure_diagnostics_test.go`

**Interfaces:**
- Produces: `*tunNetworkMutationError` with `Phase() string`, `RollbackPlan() planner.TunPlan`, `Unwrap() error`, and `Rollback(context.Context, tunPlanExecutor) error`.
- Consumes: existing transaction store, applied executor steps, and rollback metadata.

- [ ] **Step 1: Write failing ordering tests**

Add table-driven tests for `network-apply` and `network-verify` that record:

```go
want := []string{"diagnostics", "rollback", "stop-core"}
```

The tests must also assert `errors.Is(err, originalCause)`, stable phase metadata, rollback status `completed`, and no remaining cleanup-required transaction.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```bash
go test ./internal/daemon -run 'TestFullTunnelTransactionRunner(NetworkApply|NetworkVerify)Failure' -count=1
```

Expected: FAIL because `applyVerifyTunTransaction` currently performs rollback before returning to the runner.

- [ ] **Step 3: Implement deferred rollback**

Introduce a typed error that owns the in-memory transaction, exact applied-step rollback plan, and original cause. Refactor the production runner default to use the deferred variant; retain the immediate-rollback wrapper for existing direct transaction callers.

- [ ] **Step 4: Run focused tests and verify GREEN**

```bash
go test ./internal/daemon -run 'TestFullTunnelTransactionRunner(NetworkApply|NetworkVerify)Failure' -count=1
```

Expected: PASS.

### Task 2: Persist lifecycle failure metadata and rollback outcome

**Files:**
- Modify: `internal/tundiag/model.go`
- Modify: `internal/tundiag/sanitize.go`
- Modify: `internal/tundiag/render.go`
- Modify: `internal/daemon/tun_diagnostics.go`
- Modify: `internal/daemon/tun_diagnostic_error.go`
- Modify: `internal/daemon/tun_connect_lifecycle.go`
- Test: `internal/tundiag/lifecycle_failure_test.go`
- Test: `internal/daemon/tun_network_failure_report_test.go`

**Interfaces:**
- Produces: top-level report fields `failure_phase` and `rollback_status`.
- Produces: best-effort `finalizeTunFailureDiagnosticRollback(status string)` that never changes rollback success/failure.

- [ ] **Step 1: Write failing report tests**

Assert that a pre-rollback report contains:

```go
report.FailurePhase == "network-verify"
report.RollbackStatus == "pending"
```

After successful rollback and reload, assert `Historical == true`, `RollbackStatus == "completed"`, restrictive mode `0600`, bounded size, and unchanged redaction.

- [ ] **Step 2: Verify RED**

```bash
go test ./internal/tundiag ./internal/daemon -run 'LifecycleFailure|NetworkFailureReport' -count=1
```

Expected: FAIL because lifecycle fields and finalization do not exist.

- [ ] **Step 3: Implement additive schema fields and finalization**

Use the existing atomic `tundiag.Store`. Save the report before rollback with `pending`, then load/replace it after rollback with `completed` or `failed`. Treat finalization errors as diagnostic warnings only; never join them into the cleanup result.

- [ ] **Step 4: Verify GREEN**

```bash
go test ./internal/tundiag ./internal/daemon -run 'LifecycleFailure|NetworkFailureReport' -count=1
```

Expected: PASS.

### Task 3: Converge resolved-link recovery

**Files:**
- Modify: `internal/recovery/resolved.go`
- Modify: `internal/recovery/resolved_missing_device_test.go`
- Test: `internal/recovery/resolved_subprocess_test.go`
- Test: `internal/recovery/resolved_recovery_convergence_test.go`

**Interfaces:**
- Consumes: exact validated candidate `{Kind: "dns-link", Target: "podlaz0"}`.
- Produces: `recovered` when `resolvectl revert podlaz0` exits 1 with `No such device` after ownership/absence validation.

- [ ] **Step 1: Change the existing expectation to RED**

The direct cleanup test must expect `recovered`, not a persistent-record `skipped` result, and a second plan must contain no repeated DNS candidate.

- [ ] **Step 2: Verify RED**

```bash
go test ./internal/recovery -run 'Resolved.*NoSuchDevice|Resolved.*Convergence' -count=1
```

Expected: FAIL because cleanup currently re-queries status and republishes the stale record.

- [ ] **Step 3: Implement exact missing-link convergence**

After candidate target validation and proof that `ip link show dev podlaz0` is missing, accept the supported missing-link revert result as the desired final state without requiring a contradictory persisted resolved record to disappear immediately. Preserve failures for permission denial, timeout, cancellation, and unrelated exit 1.

- [ ] **Step 4: Verify GREEN and runner boundary**

Use a real helper subprocess so the runner receives an actual `*exec.ExitError` for exit 1.

```bash
go test ./internal/recovery -run 'Resolved.*(NoSuchDevice|Subprocess|Convergence)' -count=1
```

Expected: PASS.

### Task 4: Lock the production DNS composition and immediate retry contract

**Files:**
- Test: `internal/daemon/tun_dns_production_composition_test.go`
- Test: `internal/daemon/tun_retry_after_rollback_test.go`
- Modify as required: `internal/daemon/startup_scan_lifecycle.go`
- Modify as required: `internal/daemon/status_scan_publication.go`

**Interfaces:**
- Exercises: `XrayManager.tunPlanExecutor -> NewOSDNSExecutor -> applyVerifyTunTransaction -> executor.Verify`.
- Produces: fresh recovery/status publication after every lifecycle mutation.

- [ ] **Step 1: Add production-composition and retry tests**

Use a configured `podlaz0` fixture with `Current Scopes: none`, all planned servers, `~.`, and `+DefaultRoute`. Inject `network-verify` failure after all owned categories are recorded, then assert no stale transaction/startup-scan blocker and successful immediate second preflight.

- [ ] **Step 2: Verify RED or confirm existing behavior**

```bash
go test ./internal/daemon -run 'ProductionDNSComposition|RetryAfterRollback' -count=1
```

If a test is already GREEN, retain it only when it crosses the exact production boundary and would fail if another wrapper reintroduced a `Current Scopes` criterion.

- [ ] **Step 3: Implement only missing lifecycle refresh behavior**

Reuse the existing serialized startup-scan refresh and coherent publication paths. Do not add a second cache.

- [ ] **Step 4: Verify GREEN**

```bash
go test ./internal/daemon -run 'ProductionDNSComposition|RetryAfterRollback' -count=1
```

Expected: PASS.

### Task 5: Documentation, package regression, and full verification

**Files:**
- Modify: `docs/cli.md`
- Modify: `docs/state-and-security.md`
- Modify: `docs/packaged-tun-runtime.md`
- Modify: `docs/e2e.md`
- Modify: `docs/man/podlaz.1`
- Modify: `docs/man/podlazd.8`
- Modify: existing package/disposable-host TUN validation scripts where required.

- [ ] **Step 1: Update canonical behavior contracts**

Document why `Current Scopes` is diagnostic only, exact missing-link idempotency, pre-rollback `network-apply`/`network-verify` reports, historical rollback status, immediate retry semantics, and truthful permission-limited inspection.

- [ ] **Step 2: Run repository verification**

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
```

Expected: all commands pass.

- [ ] **Step 3: Run package and disposable-host validation**

Run the release-equivalent package workflow and the existing bounded self-hosted TUN E2E path. Confirm package provenance, valid `Current Scopes: none`, diagnostics-before-rollback, missing-link recovery, clean status/doctor/recover state, and immediate retry.

- [ ] **Step 4: Publish review evidence**

Record exact commands and CI jobs in the PR, without any real user endpoint, domain, address, SSID, or profile identifier.
