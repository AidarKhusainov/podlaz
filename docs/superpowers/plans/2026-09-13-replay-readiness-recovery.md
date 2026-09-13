# Replay Readiness Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the released package-restart replay race that can fail immediately after Xray creates `podlaz0`, preserve precise typed apply attribution, and prove existing recovery/terminal convergence remains safe and sufficient.

**Architecture:** Keep Xray ownership of `podlaz0` unchanged. The address binder remains the exact creation/identity fence for an Xray-created TUN; the full device readiness check must not run before the stage that itself brings the link up. Final TUN verification remains strict. Add precise typed attribution for any remaining device-level apply boundary, then validate daemon replay/recovery behavior without broadening cleanup authority or terminalizing incomplete evidence.

**Tech Stack:** Go, existing daemon/network executor tests, current Network Session/recovery model, existing package-restart acceptance contracts.

**Spec:** Reopened GitHub issue #314 plus the sanitized v0.2.41 -> v0.2.42 real-host evidence comment.

## Global Constraints

- Xray remains the sole owner of native `podlaz0` creation/lifetime.
- Do not weaken exact link identity, address, route, rule, DNS, nftables, process, or transaction ownership checks.
- `retryable`, `interrupted`, and `incomplete` replay dispositions must not become terminal solely because replay failed or rollback completed.
- Do not add retry-until-success or correctness-by-sleep.
- Preserve strict final device/TUN verification after apply.
- Keep current public API minimal; add a bounded public subphase only if required for truthful recovery/status semantics.
- Physical target-host qualification remains required before closing #314.
- Remove this temporary plan before the final repository-structure gate.

---

### Task 1: Reproduce the early Xray-TUN readiness boundary

**Files:**
- Modify: `internal/network/executor/tun_apply_subphase_test.go`
- Modify if a production-shaped composition test is needed: `internal/daemon/tun_full_tunnel_runner_test.go`

**Interfaces:**
- Consumes: `TunExecutor.ApplyWithStepSink`, bound `planner.TunAddressPlan`, existing `TunAddressExecutor` identity fencing.
- Produces: a deterministic RED test proving an Xray-created exact TUN may be bound while not yet fully `UP`, and that address apply—not the pre-address full-device verifier—owns bringing it to apply-ready state.

- [ ] **Step 1: Add an executor regression for a bound but not-yet-UP Xray TUN**

Use a fake `TunDeviceExecutor` whose `Verify` returns a deterministic "link not up yet" error and a fake `TunAddressExecutor` whose `Apply` succeeds. Use a plan with a bound TUN address (`LinkIndex`, `LinkKind="tun"`, `AppearedAfterCore=true`).

Desired behavior:

```text
ApplyWithStepSink(address-present plan)
-> does not require full TunDevice.Verify before address apply
-> invokes TunAddress.Apply
-> returns success/address step
```

The final transaction verification path is tested separately and must still call strict `TunDevice.Verify`.

- [ ] **Step 2: Run the focused executor test and verify RED**

Expected: FAIL because current `TunExecutor.ApplyWithStepSink` calls `TunDevice.Verify` before `TunAddress.Apply`.

- [ ] **Step 3: Add a negative control for missing/wrong link identity**

Prove the address executor/bound-plan validation still rejects missing or mismatched exact link identity; the fix must not permit mutation from name-only resemblance.

---

### Task 2: Fix apply ordering without weakening final verification

**Files:**
- Modify: `internal/network/executor/tun.go`
- Test: `internal/network/executor/tun_apply_subphase_test.go`

**Interfaces:**
- Produces: apply ordering in which an address-present plan relies on prior bind/identity proof, applies the exact address and link-up mutation, then later transaction verification retains the full device readiness check.

- [ ] **Step 1: Implement the smallest ordering change**

For plans with `shouldApplyTunAddress(plan.TunAddress)==true`, do not execute the full `TunDevice.Verify` before `TunAddress.Apply`; the address plan has already been bound to the exact Xray-created link identity and `IPTunAddressExecutor.Apply` revalidates that identity before every mutation and brings the link up itself.

For plans without an address mutation, retain an appropriate device verification before route/rule mutation so no route/rule can be applied against an unverified device.

- [ ] **Step 2: Run the focused executor tests and verify GREEN**

- [ ] **Step 3: Run existing TUN address identity/ownership tests**

Confirm binding, revalidation, wrong-ifindex, wrong-kind, pre-existing link, and rollback ownership tests remain green.

---

### Task 3: Close the typed attribution hole for device-level apply failures

**Files:**
- Modify: `internal/network/executor/apply_failure_subphase.go`
- Modify: `internal/network/executor/tun.go`
- Modify: `internal/network/executor/tun_apply_subphase_test.go`
- Modify: `internal/api/network_session_recovery.go`
- Modify: `internal/api/network_session_recovery_test.go`
- Modify: `docs/cli.md` only if the bounded subphase is publicly projected.

**Interfaces:**
- Produces: bounded `tun-device` apply attribution for a remaining device-level pre-mutation verification failure; existing `tun-address`, `routes`, `policy-rules`, `dns`, `nftables` values stay unchanged.

- [ ] **Step 1: Add a failing typed-subphase test for the no-address device verification path**

Construct a plan without address mutation and a `TunDevice.Verify` error. Assert:

```go
ApplyFailureSubphase(err) == "tun-device"
```

Expected: RED because the current early device error is returned unwrapped.

- [ ] **Step 2: Add the bounded subphase constant/wrapper use**

Introduce `tun-device` in the executor subphase set and wrap only the remaining device-level apply verification boundary. Do not infer the value from error text.

- [ ] **Step 3: If public recovery projection can surface this boundary, extend API validation/docs**

Add `NetworkSessionApplySubphaseTUNDevice = "tun-device"` and update the bounded validation/docs enum. Keep raw device output/private identity out of public APIs.

- [ ] **Step 4: Run executor/API tests and verify GREEN**

---

### Task 4: Prove replay evidence preserves the concrete production attribution

**Files:**
- Modify: `internal/daemon/network_session_replay_evidence_test.go`
- Modify if needed: `internal/daemon/network_session_resume_diagnostics_test.go`

**Interfaces:**
- Consumes: real `persistNetworkSessionReplayFailure`, `ApplyFailureSubphase`, `ApplyFailureCause`.
- Produces: persisted current-attempt evidence that does not collapse an executor typed boundary back to generic `network-apply`.

- [ ] **Step 1: Add a production-shaped replay persistence regression**

Feed a `network-apply` error chain carrying a typed executor subphase through the real replay persistence function. Assert the saved `network-session-resume` diagnostic has the expected `current.network_apply_subphase` and top-level projection.

- [ ] **Step 2: Add a bounded command-cause control**

For an executor command error, assert `command-exit`, `command-timeout`, or `command-unavailable` survives through replay persistence. For a semantic readiness error with no command failure, assert cause remains absent/unknown rather than fabricated.

- [ ] **Step 3: Run focused daemon tests and verify GREEN**

---

### Task 5: Verify recovery behavior rather than pre-emptively changing it

**Files:**
- Test only initially: existing `internal/daemon/network_session_*recovery*`, terminal replay, crash-order, startup continuation tests.
- Production files: none unless a new RED test proves a separate recovery defect.

**Interfaces:**
- Consumes: current #316 fenced replay/terminal convergence model.
- Produces: evidence whether replay-order fix alone restores transparent continuation and whether terminal recovery remains correct.

- [ ] **Step 1: Run the focused Network Session/replay/recovery suite after Tasks 1-4**

Required controls include terminal/current attempt, stale epoch/session, incomplete/retryable/interrupted, rollback completed/failed, successful resume, no-session retry, and crash-order finalization.

- [ ] **Step 2: If all recovery tests remain green, make no recovery production change**

Record that the recovery model is intentionally preserved; the remaining proof moves to physical qualification.

- [ ] **Step 3: If a recovery regression fails, stop and create one deterministic RED test for that exact semantic defect before editing production recovery code**

Any subsequent implementation must reuse the existing serialized Network Session transition and exact cleanup witness; no parallel/broad cleanup path is permitted.

---

### Task 6: Strengthen exact package-restart acceptance only where the new regression requires it

**Files:**
- Modify if needed: `scripts/e2e/tun-package-restart-recovery.sh`
- Modify corresponding E2E contract tests under `scripts/e2e/tests/**`

**Interfaces:**
- Produces: target-host evidence for exact candidate replay readiness and recovery without adding test-side repair.

- [ ] **Step 1: Require candidate replay to publish either verified active continuity or truthful typed non-success**

Do not add hidden second connect, service restart, manual state deletion, or broad network cleanup.

- [ ] **Step 2: Preserve independent real DNS/HTTPS/TLS verification on the active success path and ordinary-network verification on terminal recovery**

- [ ] **Step 3: Ensure foreign NetworkManager/routes/rules/nftables state remains semantically unchanged**

- [ ] **Step 4: Run hosted E2E contract tests (non-destructive)**

Physical host mutation is deferred to the dedicated target-host qualification step.

---

### Task 7: Final verification and temporary-plan removal

**Files:**
- Delete: `docs/superpowers/plans/2026-09-13-replay-readiness-recovery.md`

**Interfaces:**
- Produces: one coherent #314 product PR with no temporary prose.

- [ ] **Step 1: Run focused Go tests**

At minimum:

```bash
go test ./internal/network/executor -count=1
go test ./internal/daemon -count=1
go test ./internal/daemon -race -count=1
go test ./internal/api -count=1
```

- [ ] **Step 2: Run repository verification**

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
bash scripts/ci/repository-structure.sh --final
```

Also run relevant shell/E2E contract checks.

- [ ] **Step 3: Remove this temporary plan and rerun the final repository-structure gate**

- [ ] **Step 4: Open one PR targeting `master` and keep #314 open pending physical qualification**

The PR must explicitly state hosted validation and that destructive target-host qualification is still required.

- [ ] **Step 5: After merge/build, run exact real-host qualification**

Required boundaries:

```text
v0.2.41 active -> candidate package replacement -> verified active continuity
v0.2.40 active -> candidate package replacement -> exact recovery/replay outcome
controlled terminal-safe failure -> recover --execute -> ordinary network without reboot
```

Only close #314 after this physical evidence passes.
