# Collision-Free Network Session Resources Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** make new TUN Network Sessions coexist with unrelated host networking by deterministically allocating verified-free Podlaz resources, persisting exact allocation before mutation, and using it throughout the lifecycle.

**Architecture:** extend the authoritative snapshot with strict IPv4 policy-rule inventory, add a read-only planner allocator that produces immutable exact session resources, persist that allocation in the existing transaction before mutation, and make plan/apply/verify/recovery consume those exact identities. Replace broad foreign-state blocking with plan-specific safety checks; keep legacy fixed identifiers only for migration/diagnostics compatibility.

**Tech Stack:** Go, Linux `iproute2`, `systemd-resolved`, nftables, existing transaction/recovery packages, shell-based packaged acceptance.

**Spec:** `docs/superpowers/specs/2026-08-19-collision-free-network-session-resources-design.md`

## Global Constraints

- Never identify, stop, or mutate a foreign VPN product as part of #260.
- Use only documentation/example addresses, domains, identifiers, and fixtures in code, tests, docs, PRs, and comments.
- Persist exact allocation before the first privileged network mutation.
- Numeric resemblance to historical `51820`/`9999`/`10000` values never grants cleanup authority.
- Malformed or incomplete evidence required to prove allocation or cleanup remains fail-closed.
- Do not implement general self-healing/reconciliation from #262.
- Do not redesign public CLI/status behavior.

---

### Task 1: Add authoritative IPv4 policy-rule inventory

**Files:**
- Modify: `internal/network/snapshot/model.go`
- Modify: `internal/network/snapshot/collect.go`
- Modify: `internal/network/snapshot/collect_test.go`

**Interfaces:**
- Produces: `type PolicyRuleInventory struct { Inspection Finding; Rules []PolicyRoutingSignal }`
- Produces: `Snapshot.IPv4PolicyRules PolicyRuleInventory`
- Produces: strict parser `ParseIPv4PolicyRules(string) ([]PolicyRoutingSignal, error)` that retains all non-kernel-default IPv4 rules, including rules using historical Podlaz-looking priorities/table IDs.

- [ ] **Step 1: Write failing tests** proving `ip -4 rule show` entries at priorities `9999` and `10000`, rules looking up table `51820`, and unrelated custom rules are all retained, while malformed lines make inspection `unknown` rather than silently dropping evidence.
- [ ] **Step 2: Verify RED** with focused `go test ./internal/network/snapshot -run 'PolicyRule|Snapshot'` in CI/local environment.
- [ ] **Step 3: Implement strict collection** without product-name classification and without treating historical values specially.
- [ ] **Step 4: Verify GREEN** for snapshot tests.
- [ ] **Step 5: Commit** `feat: collect authoritative policy-rule inventory`.

### Task 2: Implement deterministic session resource allocation

**Files:**
- Create: `internal/network/planner/tun_resources.go`
- Create: `internal/network/planner/tun_resources_test.go`
- Modify: `internal/network/planner/tun.go`
- Update: `internal/network/planner/tun_address_test.go`
- Update: `internal/network/planner/full_tunnel_policy_test.go`

**Interfaces:**
- Produces:
  ```go
  type TunResourceAllocation struct {
      TunIPv4CIDR         string
      RoutingTableID      int
      ServerRulePriority  int
      TunnelRulePriority  int
  }

  func AllocateTunResources(snapshot.Snapshot) (TunResourceAllocation, error)
  ```
- `TunPlan` gains `Resources TunResourceAllocation`.
- New sessions emit exact numeric routing table strings from `Resources.RoutingTableID` and exact priorities from the allocation.

- [ ] **Step 1: Write failing allocator tests** for the clean historical allocation, occupation of `51820`/`9999`/`10000`, unrelated TUN/routes/rules, deterministic next-free selection, TUN-CIDR overlap avoidance, malformed/incomplete required evidence, and bounded exhaustion.
- [ ] **Step 2: Verify RED** for the new planner tests.
- [ ] **Step 3: Implement a bounded deterministic policy** whose first candidates are `198.18.0.1/32`, table `51820`, and rule pair `9999/10000`; subsequent candidates remain in documented safe product ranges and preserve server-rule precedence over the tunnel rule.
- [ ] **Step 4: Make `PlanTunWithOptions` consume one allocation** and build TUN address/routes/rules from it instead of global fixed identifiers.
- [ ] **Step 5: Verify GREEN** for all planner tests and add an explicit regression that a foreign rule numerically resembling historical Podlaz state causes reallocation, not ownership inference.
- [ ] **Step 6: Commit** `feat: allocate collision-free TUN session resources`.

### Task 3: Persist immutable allocation before mutation

**Files:**
- Modify: `internal/state/transaction.go`
- Modify: `internal/state/transaction_test.go`
- Modify: `internal/daemon/tun_transaction_metadata.go`
- Modify: `internal/daemon/tun_transaction_metadata_test.go`
- Modify: `internal/daemon/tun_transaction_test.go`

**Interfaces:**
- Produces:
  ```go
  type ResourceAllocation struct {
      TunIPv4CIDR        string `json:"tun_ipv4_cidr,omitempty"`
      RoutingTableID     int    `json:"routing_table_id,omitempty"`
      ServerRulePriority int    `json:"server_rule_priority,omitempty"`
      TunnelRulePriority int    `json:"tunnel_rule_priority,omitempty"`
  }
  ```
- `DesiredPlan` gains `Resources ResourceAllocation `json:"resources,omitempty"``.
- `desiredPlanFromTunPlan` copies exact planner allocation into persisted state.

- [ ] **Step 1: Write failing persistence tests** proving allocation round-trips, old transaction files without allocation still load, and `beginTunTransaction` saves allocation while state is `planned` before transition to `applying`.
- [ ] **Step 2: Verify RED** for state/daemon transaction tests.
- [ ] **Step 3: Add backward-compatible optional allocation fields** and mapping code; do not change cleanup authority rules merely because the fields exist.
- [ ] **Step 4: Verify GREEN** and assert rollback metadata contains exact allocated routes/rules.
- [ ] **Step 5: Commit** `feat: persist network session resource allocation`.

### Task 4: Make executor and verification use exact allocated identities

**Files:**
- Modify: `internal/network/executor/route.go`
- Modify: `internal/network/executor/route_test.go`
- Modify: `internal/network/executor/policy_rule.go`
- Modify: `internal/network/executor/policy_rule*_test.go`
- Update: `internal/daemon/tun_full_tunnel_runner_test.go`

**Interfaces:**
- New-session plans pass numeric table strings directly to executor commands.
- Legacy symbolic `podlaz` table remains accepted only for old persisted plans/migration compatibility.

- [ ] **Step 1: Write failing executor tests** showing a plan allocated to a non-`51820` table and non-`9999`/`10000` priorities issues/validates/deletes only those exact identities.
- [ ] **Step 2: Verify RED**.
- [ ] **Step 3: Remove new-plan dependence on the `podlaz -> 51820` conversion** while preserving a narrow legacy translation path for historical persisted records.
- [ ] **Step 4: Verify GREEN** and prove a raced kernel collision fails the add without a pre-delete of the conflicting foreign object.
- [ ] **Step 5: Commit** `fix: execute exact allocated routing identities`.

### Task 5: Replace foreign-state blocking with plan-specific coexistence checks

**Files:**
- Modify: `internal/daemon/tun_handoff_preflight.go`
- Modify: `internal/daemon/tun_handoff_preflight_test.go`
- Modify: `internal/daemon/tun_destructive_preflight_lifecycle_test.go`
- Modify: `internal/daemon/tun_connect_lifecycle.go`
- Add: `internal/daemon/issue260_coexistence_test.go`

**Interfaces:**
- `preflightTunOwnership` blocks exact recoverable Podlaz residue or concrete plan conflicts, not the existence of unrelated TUN/DNS/policy-routing/NetworkManager VPN state.
- New connect does not call `nmcli connection down` for foreign state as a prerequisite to coexistence.

- [ ] **Step 1: Write failing coexistence tests** with unrelated TUN, custom policy rules/routes, active NetworkManager VPN metadata, foreign route-only DNS link, and unrelated nftables state while a concrete server route exists.
- [ ] **Step 2: Verify RED**.
- [ ] **Step 3: Remove broad `foreignOwnershipConflicts` blocker semantics and product-name-specific interface heuristics from the connect decision path.** Keep diagnostics observational where useful, but no foreign product detection grants mutation authority.
- [ ] **Step 4: Preserve true blockers** for exact unowned collisions Podlaz cannot route around, incomplete exact Podlaz recovery ownership, missing concrete server bootstrap path, and unsafe DNS/firewall mutation prerequisites.
- [ ] **Step 5: Verify GREEN**, including a regression where the server bootstrap route intentionally uses an unrelated TUN interface and connect preflight accepts it.
- [ ] **Step 6: Commit** `feat: allow safe TUN coexistence with foreign baseline state`.

### Task 6: Remove historical numeric resemblance from stale ownership authority

**Files:**
- Modify: `internal/daemon/tun_handoff_preflight.go`
- Modify: `internal/daemon/issue256_orphan_routing_guidance_test.go`
- Modify: `internal/recovery/daemon_executor.go`
- Modify: `internal/recovery/daemon_policy_rule_recovery_test.go`
- Add: `internal/recovery/issue260_dynamic_routing_recovery_test.go`

**Interfaces:**
- Runtime diagnostics may report historical-layout observations, but `TransactionOwnsObservedRoutingResource` plus exact transaction metadata remains the only deletion authority.
- Dynamic transactions recover table/rule identities from persisted rollback/allocation, never from current historical constants.

- [ ] **Step 1: Write failing safety tests** where foreign table `51820` and priorities `9999`/`10000` exist without matching transaction ownership; recovery must leave them untouched.
- [ ] **Step 2: Write failing dynamic recovery test** where an exact transaction owns different allocated values; recovery must delete only those values.
- [ ] **Step 3: Verify RED**.
- [ ] **Step 4: Refactor runtime stale routing inspection** so historical values can be diagnostics/migration hints but cannot cause a new-session blocker or cleanup without exact transaction match.
- [ ] **Step 5: Verify GREEN** for recovery and issue-256 compatibility tests.
- [ ] **Step 6: Commit** `fix: keep routing cleanup exact under dynamic allocation`.

### Task 7: Propagate allocation through restart/revalidation/status internals

**Files:**
- Modify as required: `internal/daemon/network_session_continuation.go`
- Modify as required: `internal/daemon/tun_revalidation_plan.go`
- Modify as required: `internal/daemon/tun_revalidation_runtime.go`
- Modify as required: `internal/daemon/tun_status_helpers.go`
- Add focused tests next to changed files.

**Interfaces:**
- Active/recovered session logic obtains exact routing identities from persisted transaction/session plan rather than regenerating historical constants.

- [ ] **Step 1: Add focused failing tests** for active transaction/revalidation using a nonhistorical table/rule allocation.
- [ ] **Step 2: Verify RED**.
- [ ] **Step 3: Thread persisted allocation through any code path that currently reconstructs full-tunnel routing from constants.** Do not create a new allocation during recovery of an existing session.
- [ ] **Step 4: Verify GREEN** for daemon restart/revalidation suites, especially #259 regressions.
- [ ] **Step 5: Commit** `fix: preserve allocated resources across session lifecycle`.

### Task 8: Add packaged coexistence acceptance and update canonical docs

**Files:**
- Create: `scripts/e2e/issue260-coexistence-acceptance.sh`
- Modify: `.github/workflows/e2e-tun-package-convergence.yml`
- Modify: `docs/state-and-security.md`
- Modify: `docs/packaged-tun-runtime.md`
- Modify: `docs/e2e.md`
- Modify man pages only if their current implementation contract mentions fixed resource IDs as required behavior.

**Acceptance:**
- create sanitized unrelated baseline objects that occupy historical route/rule identifiers and include unrelated TUN/routing/DNS/nftables state;
- prove the baseline offers a concrete server path;
- connect the packaged candidate without stopping/deleting the baseline;
- assert candidate allocation differs where collisions exist;
- verify protected data plane;
- disconnect/recover Podlaz exact state;
- diff the foreign baseline and prove it remains structurally unchanged.

- [ ] **Step 1: Add acceptance script and static tests/lint hooks first.**
- [ ] **Step 2: Verify the script fails against pre-#260 behavior in the available target-host workflow.**
- [ ] **Step 3: Wire it into the existing packaged TUN convergence workflow without product-specific foreign VPN adapters.**
- [ ] **Step 4: Update canonical security/runtime/e2e docs to describe allocation vs snapshot, exact cleanup authority, bootstrap-through-current-host semantics, and retained fail-closed boundaries.**
- [ ] **Step 5: Commit** `test: add packaged TUN coexistence acceptance`.

### Task 9: Full verification and PR review

**Files:** repository-wide validation only.

- [ ] **Step 1: Run or require**:
  ```bash
  test -z "$(gofmt -l .)"
  go test ./...
  go vet ./...
  govulncheck ./...
  bash scripts/ci/workflow-lint.sh
  bash scripts/ci/validate-packages.sh
  ```
- [ ] **Step 2: Review `master...HEAD`** for unrelated refactors, product-name scanning, unsafe pre-delete behavior, historical-number ownership inference, secret/private endpoint leakage, and documentation drift.
- [ ] **Step 3: Review all new failure paths** to ensure allocation/persistence happens before mutation and cleanup remains exact.
- [ ] **Step 4: Open a draft PR linked to #260** with system-state mutation, ownership, verification, rollback/recovery, CI results, and any host-only acceptance limitation explicitly documented.
