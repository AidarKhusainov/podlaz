# Kernel-native TUN Allocation Evidence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove `iproute2` presentation parsing from collision-sensitive TUN allocation authority and make release publication depend on a real packaged TUN smoke.

**Architecture:** Keep the existing diagnostic `snapshot.Snapshot` and command-based observation paths intact. Add a small typed `TunAllocationEvidence` model in `internal/network/snapshot`, collect only IPv4 addresses/routes/rules from Linux rtnetlink through `github.com/vishvananda/netlink`, and inject that evidence into the existing TUN planner. Incomplete/interrupted dumps are discarded and retried within a bounded policy; persistent failure stays fail-closed. Add a read-only Linux CI contract and a short exact-release `.deb` TUN smoke before publication.

**Tech Stack:** Go 1.26, `net/netip`, `github.com/vishvananda/netlink`, Linux rtnetlink/NETLINK_ROUTE, GitHub Actions, Bash E2E helpers.

**Spec:** GitHub issue #293 and the approved in-chat design.

## Global Constraints

- No real user IPs, domains, profile IDs, credentials, subscription values, or endpoint data in repository artifacts.
- Preserve CLI/API/state-schema/package/service/recovery/Privacy Envelope semantics.
- Allocation authority never falls back to `ip` text/JSON parsing.
- Partial or interrupted netlink dumps are never accepted as complete evidence.
- Keep general diagnostics on the existing snapshot path; this is not a networking rewrite.
- Remove this temporary plan before final repository-structure verification.

---

### Task 1: Establish the TDD regression boundary

**Files:**
- Modify: `internal/network/snapshot/tun_allocation_evidence_test.go`

**Interfaces:**
- Produces the desired typed evidence contract used by Task 2.

- [ ] **Step 1:** Add a test that requires a `TunAllocationEvidence` model containing typed IPv4 prefixes and numeric route/rule identities, including local/broadcast-style routes and Docker/libvirt-like prefixes.
- [ ] **Step 2:** Add a test that requires interrupted authoritative collection to discard partial results and fail after the bounded retry limit.
- [ ] **Step 3:** Push the test-only commit and verify CI fails for the expected missing typed/netlink implementation, not for unrelated reasons.

### Task 2: Implement the minimal kernel-native evidence adapter

**Files:**
- Create: `internal/network/snapshot/tun_allocation_netlink_linux.go`
- Create: `internal/network/snapshot/tun_allocation_netlink_stub.go`
- Modify: `internal/network/snapshot/model.go`
- Modify: `internal/network/snapshot/tun_allocation_evidence.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `TunAllocationEvidence`, `CollectTunAllocationEvidence(context.Context) (TunAllocationEvidence, error)`.
- Linux implementation uses a small private interface around `AddrList`, `RouteListFiltered`, and `RuleList` so dump retry behavior is deterministic in unit tests.

- [ ] **Step 1:** Define the smallest typed evidence model using `netip.Prefix` and numeric table/priority values only where the allocator needs them.
- [ ] **Step 2:** Implement Linux collection with a dedicated netlink handle and IPv4-only dumps.
- [ ] **Step 3:** Convert netlink address/route/rule structures to typed evidence without textual round-trips.
- [ ] **Step 4:** Treat `netlink.ErrDumpInterrupted` as unusable partial evidence, retry the complete collection a bounded number of times, and return an error after exhaustion; any other collection/conversion error fails immediately.
- [ ] **Step 5:** Provide a non-Linux stub returning unsupported/unavailable evidence without adding Linux-only build failures.
- [ ] **Step 6:** Run focused snapshot tests, then `go test ./...`.

### Task 3: Make the allocator depend on typed authority

**Files:**
- Modify: `internal/network/planner/tun_resources.go`
- Modify: `internal/network/planner/tun_resources_test.go`
- Modify: `internal/daemon/tun_resource_snapshot.go`
- Modify: directly affected characterization tests only.

**Interfaces:**
- Consumes: `snapshot.TunAllocationEvidence` from Task 2.
- Preserve `PlanTunForSession` unless a smaller private composition change can keep callers stable; do not create parallel planner APIs without need.

- [ ] **Step 1:** Add failing planner tests proving occupied typed address/table/priorities are avoided and unavailable evidence blocks allocation.
- [ ] **Step 2:** Change only the allocation-critical planner code to use typed evidence, preserving diagnostic snapshot use for server route/DNS/firewall planning.
- [ ] **Step 3:** Wire daemon TUN resource collection to obtain authoritative evidence before planning; no fallback to parsed route/rule stdout.
- [ ] **Step 4:** Keep legacy/in-memory test collectors explicit and fail-closed rather than silently manufacturing production authority.
- [ ] **Step 5:** Run planner/daemon focused tests and daemon race tests.

### Task 4: Add a real read-only Linux compatibility gate

**Files:**
- Create: `internal/network/snapshot/tun_allocation_linux_integration_test.go`
- Modify: `.github/workflows/ci.yml` only if a dedicated invocation is required.

**Interfaces:**
- Uses the same production `CollectTunAllocationEvidence` path as the daemon.

- [ ] **Step 1:** Add a Linux-only integration test that performs real read-only rtnetlink dumps and validates non-empty/valid IPv4 evidence without mutating networking.
- [ ] **Step 2:** Ensure the test runs in ordinary PR CI on GitHub-hosted Linux; do not require secrets, root mutation, or the self-hosted VPN runner.

### Task 5: Gate release publication on exact-package TUN smoke

**Files:**
- Create or minimally extend an E2E script under `scripts/e2e/**` using existing `scripts/e2e/lib/**` helpers.
- Modify: `.github/workflows/release.yml`
- Add/update E2E contract test beside the script.

**Interfaces:**
- Input: exact release `.deb` artifact built by the release workflow plus existing `vpn-e2e` environment profile secret/vars.
- Output: pass only after install, daemon readiness, TUN connect, semantic protected status/connectivity, disconnect, and clean convergence.

- [ ] **Step 1:** Add a contract test for the short release smoke script and exact package provenance.
- [ ] **Step 2:** Implement the smallest smoke by reusing existing profile/readiness/installed-client/cleanup helpers; do not duplicate the long convergence suite.
- [ ] **Step 3:** Insert a self-hosted `vpn-e2e` job after release artifacts are built and before `attest-and-publish`; download the exact artifact, run smoke, always perform exact cleanup, and upload only sanitized evidence.
- [ ] **Step 4:** Make publication depend on the smoke job.

### Task 6: Final verification and cleanup

**Files:**
- Delete: `docs/superpowers/plans/2026-09-04-tun-allocation-netlink-evidence.md`

- [ ] **Step 1:** Run/confirm `test -z "$(gofmt -l .)"`, `go test ./...`, `go test ./internal/daemon -race -count=1`, `go vet ./...`, `govulncheck ./...`, shell/workflow/package checks, and relevant E2E contract tests.
- [ ] **Step 2:** Run `bash scripts/ci/repository-structure.sh --final` after deleting the temporary plan.
- [ ] **Step 3:** Review the final diff for scope, privacy, ownership, recovery ordering, dependency size, and dead text-parser authority paths.
- [ ] **Step 4:** Update the PR with exact validation evidence and close #293 through the PR.