# TUN Terminal Recovery P0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every exact-owned terminal TUN teardown either converge to a clean disconnected state or stop before an unsafe dependent mutation while preserving exact durable authority for retry/recovery without reboot.

**Architecture:** Keep the existing transaction and Network Session ownership model. Reuse one exact nftables reconstruction contract for active disconnect and recovery, make rollback phase-gated, route terminal intent through one exact terminal convergence owner, and keep observation failures bounded/typed without broadening cleanup authority. Preserve public CLI/API/state schemas unless an existing compatibility-safe field is sufficient.

**Tech Stack:** Go 1.26.6, Linux nftables/netlink, systemd-resolved, Xray lifecycle, GitHub Actions Ubuntu 24.04, existing Podlaz recovery/session packages.

**Spec:** https://github.com/AidarKhusainov/podlaz/issues/313

## Global Constraints

- Privileged network mutation remains fail-closed and exact-ownership-driven.
- Observation, names, handles, comments, historical resemblance, and timeouts never grant cleanup authority.
- Preserve CLI/API/state-schema/package/service compatibility unless strictly required by #313.
- Do not mix the separate healthy-active `doctor --tun` false-unhealthy question into this P0.
- Use only synthetic/RFC/example values in repository artifacts.
- Prefer small private/shared helpers; no new public layer without a real domain boundary.
- No arbitrary sleeps/retries or timeout inflation.

---

### Task 1: Exact transaction firewall reconstruction

**Files:**
- Modify: `internal/daemon/tun_status_helpers.go`
- Modify: `internal/recovery/nftables_exact_rollback.go` / small internal export surface if required
- Test: `internal/daemon/tun_disconnect_recovery_regression_test.go`
- Test: existing recovery nftables negative matrix

**Interfaces:**
- Consumes: persisted `DesiredPlan.NFT` plus one exact `Rollback.NFTables` tuple.
- Produces: one complete `planner.TunFirewallPlan` or a fail-closed reconstruction error.

- [ ] Add/retain RED coverage proving active disconnect loses chain/rule semantics on v0.2.40.
- [ ] Add negative cases: desired-only, rollback-only, identity mismatch, duplicate/ambiguous authority, incomplete composition.
- [ ] Reuse exactly one reconstruction helper in active disconnect and transaction recovery.
- [ ] Verify GREEN in daemon/recovery tests.

### Task 2: Phase-gated rollback

**Files:**
- Modify: `internal/network/executor/dns_resolved.go`
- Modify: `internal/network/executor/tun.go` only if lower-level dependent phases also need gating
- Test: focused executor rollback tests
- Test: production-shaped daemon disconnect regression

**Interfaces:**
- Consumes: exact verified rollback plan.
- Produces: first blocking phase error without starting later dependent destructive phases.

- [ ] Write RED test where firewall rollback fails and assert DNS/routes/rules/address are untouched.
- [ ] Write RED test where DNS rollback fails and assert routes/rules/address are untouched.
- [ ] Preserve exact resource idempotence for already-absent state from previous partial attempts.
- [ ] Implement minimal fail-fast phase gating.
- [ ] Verify successful rollback order remains firewall -> DNS -> policy rules/routes -> address.

### Task 3: One terminal Network Session convergence owner

**Files:**
- Modify: `internal/daemon/network_session_lifecycle.go`
- Modify: `internal/daemon/http_server.go`
- Modify: `internal/daemon/network_session_recovery_plan.go`
- Modify: `internal/daemon/recovery.go` only for healthy-active vs cleanup-required-active routing
- Test: terminal startup/recovery/order tests

**Interfaces:**
- Consumes: current-boot terminal intent, exact transaction recovery, exact Privacy Envelope authority.
- Produces: exact transaction convergence -> tracked child/config convergence -> Privacy Envelope teardown -> remaining-host verification -> authority clear.

- [ ] RED: terminal intent remains recovery-visible with open and blocked startup gate.
- [ ] RED: healthy active TUN remains mutation-free.
- [ ] RED: cleanup-required active + terminal intent proceeds to exact terminal convergence.
- [ ] RED: unrelated generic recovery warning cannot veto an otherwise exact terminal teardown.
- [ ] Remove overlapping terminal cleanup stage if it has no unique ownership responsibility; do not special-case warning strings.
- [ ] Ensure same-daemon lifecycle state converges to inactive after tracked child cleanup.

### Task 4: Captured stranded-state recovery

**Files:**
- Test: production-shaped daemon HTTP/recovery integration test(s)
- Modify only the smallest lifecycle/recovery code required by the failing tests.

**Interfaces:**
- Seed: active/cleanup-required publication + terminal Network Session + failed exact transaction + exact remaining nftables + tracked child/link + already-missing address/routes/rules.
- Expected: clean recovery without reboot; second recovery clean and mutation-free.

- [ ] Write RED production-shaped `/recover` test for the sanitized v0.2.40 stranded semantic state.
- [ ] Prove exact `inet podlaz` cleanup, idempotent missing-resource rollback, exact tracked child stop, link/process absence, config removal, Privacy Envelope removal, remaining-network verification, transaction/session authority clear.
- [ ] Prove a second `recover --execute --yes` performs no network mutation and succeeds.

### Task 5: Bounded resolver and daemon-backed diagnostic behavior in terminal cleanup

**Files:**
- Inspect/modify: recovery resolved observation path
- Inspect/modify: daemon diagnostic HTTP/client fallback boundary only where the RED test demonstrates a defect
- Test: existing resolved tri-state tests plus new terminal/daemon-backed regression

**Interfaces:**
- `resolvectl` timeout/signal/unavailable -> typed incomplete observation, never absence.
- Reachable daemon + one subordinate timeout -> bounded daemon response, never daemon-unavailable fallback.

- [ ] RED: resolver inspection timeout during terminal cleanup does not become absence.
- [ ] RED: if exact transaction DNS cleanup is already proven, unrelated later resolver observation does not permanently veto final terminal postcondition verification.
- [ ] RED: reachable daemon returns bounded unknown/incomplete evidence when one subordinate inspection times out.
- [ ] Do not increase timeouts or add sleeps/retries.

### Task 6: Truthful terminal status and root-cause diagnostics

**Files:**
- Modify: existing status/network-session guard helpers only if necessary
- Test: status/doctor/recover lifecycle regressions

**Interfaces:**
- terminal intent + cleanup-required authority -> non-zero incomplete-cleanup/unknown terminal state.
- successful convergence -> conclusively disconnected.

- [ ] RED: failed explicit disconnect cannot publish normal reconnect intent.
- [ ] RED: originating rollback blocker remains visible despite downstream missing route/rule/address observations.
- [ ] RED: successful recovery clears stale terminal failure publication.
- [ ] Preserve existing compatible fields where possible.

### Task 7: Crash/restart matrix

**Files:**
- Extend: `internal/daemon/network_session_terminal_crash_boundaries_test.go`
- Extend neighboring transaction/recovery tests only as required.

- [ ] Cover terminal intent persisted before firewall removal.
- [ ] Cover firewall removed before later network rollback.
- [ ] Cover partial routes/rules/address cleanup.
- [ ] Cover Xray absent while config remains.
- [ ] Cover data plane cleaned while Privacy Envelope remains.
- [ ] Cover Privacy Envelope absent while Network Session authority remains.
- [ ] At every boundary require exact convergence or a precise fail-closed blocker; never reboot semantics.

### Task 8: Real-host/repository acceptance contract

**Files:**
- Prefer existing `scripts/acceptance/release-laptop.sh` / `scripts/e2e/**` surfaces.
- Modify only if current executable contracts cannot express #313 postconditions.

- [ ] Ensure exact-candidate acceptance checks normal connect -> verified -> real traffic -> disconnect -> ordinary networking -> zero Podlaz-owned residue.
- [ ] Ensure controlled teardown failure -> recover -> clean convergence -> repeat connect/disconnect.
- [ ] Ensure sanitized v0.2.40-equivalent upgrade/recovery scenario.
- [ ] Preserve unrelated host state.

### Task 9: Final verification and PR hygiene

- [ ] Remove this temporary implementation plan before final structure verification.
- [ ] `test -z "$(gofmt -l .)"`.
- [ ] `go test ./...`.
- [ ] `go test ./internal/daemon -race -count=1` (or the repository canonical daemon race wrapper).
- [ ] `go vet ./...`.
- [ ] `govulncheck ./...`.
- [ ] `bash scripts/ci/repository-structure.sh --final`.
- [ ] Relevant workflow/shell/nftables/package/E2E checks.
- [ ] Review final diff for ownership, rollback ordering, state compatibility, diagnostics, privacy, and dead/stale code.
- [ ] Update PR #312 to close #313 only with fresh exact-head evidence; keep draft if the mandatory real-host gate cannot be executed.
