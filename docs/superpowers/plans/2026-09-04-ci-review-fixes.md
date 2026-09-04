# CI Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the four validated PR #292 review gaps without restoring retired catch-all/self-hosted orchestration.

**Architecture:** Keep each lost contract focused and independently testable. Public real-provider evidence is reduced to one exact file; the v0.2.29 baseline is pinned to the official historical package artifact; child-Xray crash and ordinary-user packaged authorization become small installed-package acceptances that reuse existing helpers and run only where their environment is safe.

**Tech Stack:** Go tests, Bash acceptance harnesses, Debian packages, systemd, polkit, GitHub Actions.

**Spec:** PR #292 validated review findings plus issue #160, #184, and #273 contracts.

## Global Constraints

- Do not restore `e2e.yml`, `e2e-tun-package-convergence.yml`, or `e2e-tun-resource-soak.yml`.
- Do not publish real IPs, endpoints, profile material, generated configs, or other private host data.
- Do not add destructive TUN work to general hosted runners.
- Keep final permanent prose limited by `AGENTS.md`; delete this temporary plan before final verification.
- Use current public CLI semantics rather than removed presentation strings.

---

### Task 1: Make public real-provider publication unbypassable

**Files:**
- Modify: `.github/workflows/integration.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `scripts/e2e/real_provider_artifact_contract_test.go`

- [ ] Add a failing test proving a nested staging file is rejected and both workflows upload the exact `real-provider-result.txt`, not a directory.
- [ ] Verify RED in CI.
- [ ] Change both upload steps to the exact result path with `if-no-files-found: error`; keep the content gate.
- [ ] Verify GREEN.

### Task 2: Pin the historical v0.2.29 package artifact

**Files:**
- Modify: `scripts/e2e/network-recovery-package-acceptance.sh`
- Modify: `scripts/e2e/network_recovery_acceptance_contract_test.go`

- [ ] Resolve the official v0.2.29 release asset identity and SHA-256 from GitHub release metadata/asset bytes.
- [ ] Add a failing contract test requiring an immutable per-architecture digest check in the standalone acceptance.
- [ ] Verify RED.
- [ ] Add the smallest fail-closed digest map/check before baseline installation; retain the Debian Version check.
- [ ] Verify GREEN.

### Task 3: Preserve intentional supervised Xray crash coverage

**Files:**
- Create: `scripts/e2e/core-crash-package-acceptance.sh`
- Create: `scripts/e2e/core_crash_acceptance_contract_test.go`
- Modify: `scripts/e2e/installed-package-integration.sh`

- [ ] Add a failing contract test requiring a focused installed-package child-Xray SIGKILL scenario and hosted integration wiring.
- [ ] Verify RED.
- [ ] Implement proxy-only connect with a deterministic fictional profile, locate only the supervised packaged Xray child, SIGKILL it, assert diagnostic/status visibility, disconnect/recovery convergence, and absence of stale owned state.
- [ ] Run it from hosted installed-package integration; do not add TUN or real-provider dependencies.
- [ ] Verify GREEN in hosted Integration and CI.

### Task 4: Restore packaged ordinary-user lifecycle authorization coverage

**Files:**
- Create: `scripts/e2e/package-authorization-acceptance.sh`
- Create: `scripts/e2e/package_authorization_acceptance_test.go`
- Modify: `scripts/e2e/installed-package-integration.sh`

- [ ] Add a failing contract test requiring ordinary-user no-group identity, packaged filesystem socket ownership/mode inspection, unavailable/allow polkit outcomes, and ordinary-user `connect -> status -> disconnect`.
- [ ] Verify RED.
- [ ] Implement a narrow systemd drop-in that replaces only `pkcheck` behavior while retaining packaged Xray and normal user-owned profile state; assert no group mutation and clean up the exact drop-in.
- [ ] Run it in hosted installed-package integration after read-only checks.
- [ ] Verify GREEN in hosted Integration and CI.

### Task 5: Final verification

- [ ] Remove this temporary plan.
- [ ] Run final non-draft CI with `repository-structure.sh --final`.
- [ ] Confirm hosted installed-package Integration passes with both focused acceptances.
- [ ] Audit PR diff for retired workflow resurrection, private-data publication paths, and accidental mode-only changes.
- [ ] Update PR body with exact final HEAD and verification evidence.
