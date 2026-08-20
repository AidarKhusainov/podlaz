# Session-Scoped Privacy Envelope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** preserve a fail-closed privacy boundary for an already-protected Podlaz Network Session across data-plane replacement and daemon/service/package recovery, then deliberately remove that boundary only after exact Podlaz teardown and verification of the remaining host network.

**Architecture:** evolve volatile Network Session state so reconnect intent and exact cleanup authority are distinct fields of one session lifecycle; add a collision-safe exact-owned nftables Privacy Envelope whose composition can be atomically replaced; arm and re-verify it before publishing `CONNECTED`; preserve it while data-plane transactions are recovered or replaced; and use one ordered explicit/terminal teardown path that cleans data plane first, removes the envelope second, verifies the post-Podlaz network, then clears session authority and permits clean `DISCONNECTED` publication.

**Tech Stack:** Go 1.26.6, Linux nftables, existing Podlaz transaction/recovery/snapshot/planner/executor packages, systemd service lifecycle, shell/Go installed-package acceptance.

**Spec:** `docs/superpowers/specs/2026-08-20-session-scoped-privacy-envelope-design.md`

## Global Constraints

- Use only documentation/example addresses, domains, identifiers, SSIDs, and profile data in code, tests, docs, PRs, comments, and public artifacts.
- The Privacy Envelope is a separate physical nftables resource but not a separate independent generic transaction state machine.
- Persist exact session/envelope cleanup authority before the first envelope nftables mutation.
- `recognition != ownership`: names, comments, conventional values, and continuation intent alone never grant deletion authority.
- Never flush or restore the whole nftables ruleset, never delete an ambiguous table, and never normalize unrelated firewall/routing/VPN state.
- From protected `CONNECTED` until intentional envelope removal, ordinary non-exempt direct uplink egress must remain blocked across lifecycle/data-plane boundaries.
- Do not use a blanket external DNS exception or a generic `ct state established,related accept` rule.
- Reuse the existing pre-resolved concrete IPv4 server bootstrap path; Xray TUN runtime already consumes `OutboundAddressOverride`, so daemon recovery must not require broad direct DNS.
- Endpoint allowance changes must be atomic from nftables packet-path semantics. A single `nft -f` batch may replace the exact envelope table because nftables commits a successful batch atomically; a failed batch must leave the old generation in place.
- `DISCONNECTED` is not clean while exact Podlaz data-plane residue, a live/ambiguous envelope, or unverified post-Podlaz network state remains.
- Do not implement Issue #262 soft/hard evidence policy or Issue #263 boot autostart.

---

### Task 1: Persist one Network Session identity and separate reconnect intent from cleanup authority

**Files:**
- Create: `internal/daemon/network_session_state.go`
- Create: `internal/daemon/network_session_state_test.go`
- Modify: `internal/daemon/network_session_continuation.go`
- Modify: `internal/daemon/network_session_continuation_test.go`

**Interfaces:**
- Produces:
  ```go
  const networkSessionStateSchemaVersion = "podlaz.network-session-state.v1"

  type networkSessionIntent string
  const (
      networkSessionIntentResume     networkSessionIntent = "resume"
      networkSessionIntentDisconnect networkSessionIntent = "disconnect"
      networkSessionIntentTerminal   networkSessionIntent = "terminal"
  )

  type networkSessionProtectionState string
  const (
      networkSessionProtectionUnarmed  networkSessionProtectionState = "unarmed"
      networkSessionProtectionArming   networkSessionProtectionState = "arming"
      networkSessionProtectionArmed    networkSessionProtectionState = "armed"
      networkSessionProtectionRemoving networkSessionProtectionState = "removing"
  )

  type networkSessionProtection struct {
      State              networkSessionProtectionState `json:"state"`
      CompositionVersion int                           `json:"composition_version"`
      Family             string                        `json:"family"`
      Table              string                        `json:"table"`
      TunInterface       string                        `json:"tun_interface"`
      BootstrapIPv4      []string                      `json:"bootstrap_ipv4"`
  }

  type networkSessionState struct {
      SchemaVersion string                    `json:"schema_version"`
      Owner         string                    `json:"owner"`
      BootID        string                    `json:"boot_id"`
      SessionID     string                    `json:"session_id"`
      Intent        networkSessionIntent      `json:"intent"`
      Request       api.ConnectRequest        `json:"request"`
      Protection    *networkSessionProtection `json:"protection,omitempty"`
  }
  ```
- `networkSessionStateStore` persists one mode-0600 atomic record under the daemon runtime directory and supports `BeginOrResume`, `Load`, `SetIntent`, `SetProtection`, and `Remove`.
- Existing `networkSessionContinuationStore` becomes a compatibility adapter over the session-state store so Issue #259 call sites retain `Save`, `LoadCurrent`, and `Remove` semantics while explicit stop/disconnect can disarm resume intent without deleting exact protection authority.
- Existing `podlaz.network-session-continuation.v1` files are migrated only when valid/current-boot; migration generates a new session identity before any new #261 mutation and preserves the request.

- [ ] **Step 1: Write failing state tests** proving a new connect gets a non-empty stable session ID, repeated same-boot resume retains that ID, `SetIntent(disconnect|terminal)` makes `LoadCurrent` return no reconnect request while the protection authority remains loadable, permissions remain 0600, malformed/foreign/previous-boot records fail closed without granting cleanup authority, and v1 continuation migration preserves the request without inventing authority for any live nftables object.
- [ ] **Step 2: Verify RED** with `go test ./internal/daemon -run 'NetworkSession(State|Continuation)'` and capture the expected failures before production code exists.
- [ ] **Step 3: Implement the session-state store** using the existing atomic private-file/sync helpers, `crypto/rand` session IDs, strict enum/table/IP validation, defensive copies of endpoint slices, and bounded JSON size.
- [ ] **Step 4: Adapt continuation lifecycle methods** so explicit service stop/disconnect changes intent first and does not delete a protection record; final record removal is deferred to successful session teardown.
- [ ] **Step 5: Verify GREEN** for state/continuation/shutdown tests and add a regression that a failed disconnect cannot erase envelope cleanup authority.
- [ ] **Step 6: Commit** `feat: persist network session privacy authority`.

### Task 2: Generalize exact nftables execution for a collision-safe envelope table

**Files:**
- Modify: `internal/network/executor/nftables.go`
- Modify: `internal/network/executor/nftables_test.go`
- Create: `internal/network/executor/privacy_envelope.go`
- Create: `internal/network/executor/privacy_envelope_test.go`

**Interfaces:**
- Produces:
  ```go
  type PrivacyEnvelopePlan struct {
      Family string
      Table  string
      Chains []planner.TunFirewallChainPlan
      Rules  []planner.TunFirewallRulePlan
      Reason string
  }

  type PrivacyEnvelopeExecutor struct {
      Runner    CommandRunner
      ScriptDir string
  }

  func (e PrivacyEnvelopeExecutor) Exists(context.Context, PrivacyEnvelopePlan) (bool, error)
  func (e PrivacyEnvelopeExecutor) Apply(context.Context, PrivacyEnvelopePlan) error
  func (e PrivacyEnvelopeExecutor) Replace(context.Context, PrivacyEnvelopePlan, PrivacyEnvelopePlan) error
  func (e PrivacyEnvelopeExecutor) Verify(context.Context, PrivacyEnvelopePlan) error
  func (e PrivacyEnvelopeExecutor) Remove(context.Context, PrivacyEnvelopePlan) error
  ```
- `Replace` emits one nftables batch containing exact old-table deletion plus complete new-table recreation, so the kernel changes generations atomically rather than creating a permissive gap.
- Existing `NftablesExecutor` retains strict whole-table ownership of `inet podlaz`; the new executor does not broaden normal TUN rollback authority.

- [ ] **Step 1: Write failing executor tests** for exact apply/verify/remove of a synthetic dynamic envelope table, collision detection without pre-delete, exact-composition rejection on extra/missing/reordered rules, idempotent missing-table remove, and refusal to mutate malformed/non-envelope targets.
- [ ] **Step 2: Write a failing atomic replacement test** requiring one `nft -f` invocation whose script deletes the exact old table and recreates the exact new composition in the same batch; assert there is no standalone delete command.
- [ ] **Step 3: Verify RED** with `go test ./internal/network/executor -run 'PrivacyEnvelope|Nftables'`.
- [ ] **Step 4: Implement the narrow executor** by reusing/refactoring exact nft table parsing/rendering helpers without weakening the normal `inet podlaz` verifier. Restrict envelope table identifiers to a generated Podlaz envelope namespace syntactically, but treat that syntax only as input validation, never as ownership evidence.
- [ ] **Step 5: Verify GREEN** and regression-test that existing `NftablesExecutor.Rollback` still deletes only the normal transaction-owned `inet podlaz` table.
- [ ] **Step 6: Commit** `feat: execute exact privacy envelope tables`.

### Task 3: Allocate and reconstruct the exact Privacy Envelope composition

**Files:**
- Create: `internal/daemon/privacy_envelope_plan.go`
- Create: `internal/daemon/privacy_envelope_plan_test.go`
- Modify: `internal/daemon/tun_core_runtime_plan_test.go` only if needed to pin bootstrap-address reuse.

**Interfaces:**
- Produces:
  ```go
  const privacyEnvelopeCompositionVersion = 1

  func allocatePrivacyEnvelope(
      context.Context,
      string, // session ID
      string, // TUN interface
      []string, // exact bootstrap IPv4 endpoints
      privacyEnvelopeObserver,
  ) (networkSessionProtection, PrivacyEnvelopePlan, error)

  func privacyEnvelopePlanFromAuthority(networkSessionProtection) (PrivacyEnvelopePlan, error)
  ```
- Candidate tables are deterministic from the cryptographically random session ID plus a bounded numeric suffix, e.g. `podlaz_pe_<session-tag>` and `podlaz_pe_<session-tag>_1`; each candidate is observed read-only before authority is persisted. A raced `add table` collision fails closed and is never deleted/adopted.
- Composition is one `inet` output base chain with an explicit default accept policy plus ordered terminal reject/drop rule; accepted exceptions are narrowly enumerated before the terminal rule.
- Required allowed packet classes for the first composition version:
  - loopback output;
  - output through the exact active Podlaz TUN interface;
  - exact persisted IPv4 VPN bootstrap endpoint(s);
  - DHCPv4 client control traffic (`udp sport 68 udp dport 67`);
  - DHCPv6 client control traffic (`udp sport 546 udp dport 547`);
  - essential outbound IPv6 neighbor/router discovery control messages required for link maintenance, expressed as exact ICMPv6 types rather than arbitrary IPv6 traffic.
- No generic conntrack-established allowance, external DNS blanket, whole-uplink allowance, or arbitrary LAN allowance.

- [ ] **Step 1: Write failing planner tests** for deterministic candidate selection, occupied first candidate -> next candidate, bounded exhaustion, malformed session ID/interface/bootstrap endpoint, exact rule order/composition, IPv4+IPv6 ordinary egress reaching the terminal block rule, loopback/TUN/bootstrap/control exceptions, and absence of broad DNS/conntrack/uplink expressions.
- [ ] **Step 2: Verify RED** with `go test ./internal/daemon -run 'PrivacyEnvelopePlan'`.
- [ ] **Step 3: Implement allocation/composition** with shell-safe nft identifiers, normalized/deduplicated/sorted concrete IPv4 bootstrap addresses, exact ownership comments containing no profile/domain/private values, and no mutable foreign-state inference.
- [ ] **Step 4: Verify GREEN** and add a reconstruction test proving persisted authority alone regenerates byte-for-byte-equivalent logical composition after daemon restart.
- [ ] **Step 5: Commit** `feat: plan session privacy envelope`.

### Task 4: Arm and verify protection before `CONNECTED` publication

**Files:**
- Create: `internal/daemon/privacy_envelope_lifecycle.go`
- Create: `internal/daemon/privacy_envelope_lifecycle_test.go`
- Modify: `internal/daemon/tun_full_tunnel_runner.go`
- Modify: `internal/daemon/tun_full_tunnel_runner_test.go`
- Modify: `internal/daemon/tun_connect_lifecycle.go`
- Modify: `internal/daemon/tun_connect_lifecycle_test.go`

**Interfaces:**
- Produces:
  ```go
  type privacyEnvelopeLifecycle struct {
      store    networkSessionStateStore
      executor netexecutor.PrivacyEnvelopeExecutor
  }

  func (p privacyEnvelopeLifecycle) Arm(context.Context, planner.TunPlan) error
  func (p privacyEnvelopeLifecycle) Verify(context.Context) error
  func (p privacyEnvelopeLifecycle) RemoveAfterDataPlaneCleanup(context.Context) error
  ```
- `fullTunnelTransactionRunner` gets explicit hooks `armPrivacyEnvelope` and `verifyPrivacyEnvelope` and performs:
  `initial connectivity verify -> persist authority/arm -> exact envelope verify -> connectivity verify again with envelope active -> commit transaction/active state`.
- The second connectivity verification must occur before `commitActiveState`, which is the current boundary that updates `m.state` and permits `CONNECTED` publication.
- If arming/post-arm verification fails, cleanup keeps the envelope in place while the data-plane transaction is rolled back, then deliberately removes/verifies the envelope and returns without publishing active state.

- [ ] **Step 1: Write failing runner ordering tests** recording hooks and requiring exactly `network-verify -> connectivity -> envelope-authority -> envelope-apply -> envelope-verify -> connectivity-under-envelope -> commit`.
- [ ] **Step 2: Add failing crash-boundary tests** for authority persisted before apply, apply before verify, and envelope verified before commit; each persisted state must be restart-reconstructable and never grant authority to a foreign table.
- [ ] **Step 3: Verify RED** for focused daemon runner/connect tests.
- [ ] **Step 4: Implement arming lifecycle** so state is persisted as `arming` before `nft -f`, transitions to `armed` only after exact verification, and stores only concrete bootstrap IPs already present in the TUN plan.
- [ ] **Step 5: Wire the runner ordering** and post-envelope connectivity verification before active publication. Keep the existing normal transaction firewall during initial arming so protection overlaps rather than swaps through a gap.
- [ ] **Step 6: Verify GREEN** including failure cleanup and existing connect/rollback regressions.
- [ ] **Step 7: Commit** `feat: arm privacy envelope before connected state`.

### Task 5: Preserve/reconcile the envelope before startup data-plane recovery

**Files:**
- Create: `internal/daemon/network_session_privacy_recovery.go`
- Create: `internal/daemon/network_session_privacy_recovery_test.go`
- Modify: `internal/daemon/network_session_continuation.go`
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/network_session_shutdown_test.go`
- Modify: `internal/daemon/recovery_active_committed_test.go`

**Interfaces:**
- Produces:
  ```go
  func reconcileNetworkSessionProtection(context.Context, networkSessionStateStore, netexecutor.PrivacyEnvelopeExecutor) (networkSessionState, bool, error)
  ```
- Startup ordering with resume intent is:
  `load exact session state -> reconcile/verify arming|armed envelope -> recover exact old data-plane transactions -> reconnect using the persisted bootstrap endpoint -> verify under envelope -> CONNECTED`.
- If the persisted envelope endpoint exists, TUN snapshot/server-bypass planning during restart uses that exact concrete IP and does not require broad direct DNS before the new TUN is up.
- Generic transaction recovery never sees/removes the envelope because the envelope is not represented as a TUN transaction.

- [ ] **Step 1: Write failing startup tests** where an armed envelope exists alongside a committed old TUN transaction and prove the envelope verifier/reconciler runs before any transaction rollback callback.
- [ ] **Step 2: Write failing restart tests** for `arming + table missing` (apply then verify), `arming + exact table present` (verify then mark armed), `armed + exact table present` (idempotent verify), `armed + table missing` (recreate from exact authority), and `authority points to unexpected composition` (fail closed; do not overwrite ambiguous object).
- [ ] **Step 3: Add a failing bootstrap regression** where the original profile server is a documentation domain but the protected restart path uses the persisted concrete documentation IPv4 endpoint while external resolver access is unavailable.
- [ ] **Step 4: Verify RED** with focused network-session/recovery tests.
- [ ] **Step 5: Implement protection reconciliation before `recoverExactNetworkSessionTransactions`** and thread the exact bootstrap override into TUN snapshot planning only for the matching current Network Session.
- [ ] **Step 6: Verify GREEN** for #259 restart/recover serialization regressions and new privacy ordering tests.
- [ ] **Step 7: Commit** `fix: preserve privacy before startup recovery`.

### Task 6: Make restart teardown keep the envelope while ordinary/terminal teardown removes it last

**Files:**
- Modify: `internal/daemon/network_session_continuation.go`
- Create: `internal/daemon/network_session_teardown.go`
- Create: `internal/daemon/network_session_teardown_test.go`
- Modify: `internal/daemon/tun_cleanup_lifecycle.go`
- Modify: `internal/daemon/tun_revalidation_terminal.go`
- Modify: `internal/daemon/tun_revalidation_terminal_test.go`
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/network_session_shutdown_fence_test.go`

**Interfaces:**
- Produces one session-level ordered teardown function with reason `explicit`, `terminal`, or `service-stop` and a separate `restart` path.
- Restart path: disarm nothing, keep exact envelope armed, remove/finish exact old Data Plane Generation, leave session state/authority, then startup resume reconstructs the data plane under the same envelope.
- Explicit/terminal/service-stop path:
  1. persist non-resume intent;
  2. stop further reconnect/revalidation scheduling;
  3. keep envelope armed;
  4. clean all exact Podlaz data-plane transactions/state;
  5. prove relevant Podlaz data-plane state absent;
  6. persist protection `removing`;
  7. remove exact envelope;
  8. verify exact envelope absent;
  9. verify the remaining host network is usable;
  10. remove session-state authority;
  11. permit final `DISCONNECTED` publication.

- [ ] **Step 1: Write failing deterministic ordering tests** using event hooks for explicit disconnect, terminal failure, explicit service stop, and daemon restart. Require envelope removal only in the first three and strictly after data-plane cleanup proof.
- [ ] **Step 2: Write failing cleanup-failure tests** proving data-plane cleanup failure leaves the envelope armed and authority persisted; envelope-removal failure leaves authority persisted and prevents clean disconnected publication; post-envelope network-verification failure keeps terminal intent/diagnostic authority and never restarts the VPN automatically.
- [ ] **Step 3: Write failing crash-boundary tests** after terminal decision, after data-plane removal, after envelope removal, and after host verification but before metadata clear; each restart must continue the terminal convergence rather than resume the VPN.
- [ ] **Step 4: Verify RED** for teardown/terminal/shutdown suites.
- [ ] **Step 5: Implement one teardown coordinator** rather than duplicating ordering in terminal handler and normal disconnect. Keep underlying data-plane cleanup exact and idempotent; envelope authority is cleared only after successful absence+network verification.
- [ ] **Step 6: Update terminal handler** to persist terminal intent before cleanup and cancel further automatic revalidation/reconnect scheduling before calling teardown.
- [ ] **Step 7: Verify GREEN** including #259 shutdown fencing and concurrent lifecycle tests.
- [ ] **Step 8: Commit** `fix: teardown network session behind privacy envelope`.

### Task 7: Verify the post-Podlaz host network without assuming direct ISP ownership

**Files:**
- Create: `internal/daemon/post_podlaz_network.go`
- Create: `internal/daemon/post_podlaz_network_test.go`
- Modify: `internal/daemon/network_session_teardown.go`

**Interfaces:**
- Produces:
  ```go
  type postPodlazNetworkVerifier interface {
      Verify(context.Context) error
  }
  ```
- Production verification recollects authoritative host state after Podlaz data-plane/envelope removal, requires a usable non-Podlaz default/server path according to existing snapshot safety semantics, and performs a bounded functional connectivity proof without taking ownership of the route/DNS/VPN providing that path.
- The verifier does not require the physical ISP interface specifically and does not identify a foreign VPN product.

- [ ] **Step 1: Write failing verifier tests** for an ordinary physical default route, a remaining foreign/custom routed baseline, missing default path, incomplete snapshot evidence, and a functional probe failure. Use documentation-only addresses and synthetic interface names.
- [ ] **Step 2: Verify RED**.
- [ ] **Step 3: Implement bounded observation + functional verification** by reusing existing snapshot/connectivity primitives where possible; avoid introducing a new external diagnostic dependency and keep errors redacted/classified.
- [ ] **Step 4: Verify GREEN** and prove the verifier never calls foreign disconnect, route deletion, DNS reset, or nftables mutation.
- [ ] **Step 5: Commit** `feat: verify remaining host network after teardown`.

### Task 8: Add direct-leak invariants and installed-package acceptance

**Files:**
- Create: `internal/daemon/issue261_privacy_envelope_regression_test.go`
- Create: `scripts/e2e/issue261-package-acceptance.sh`
- Create: `scripts/e2e/issue261_acceptance_contract_test.go`
- Modify: `.github/workflows/e2e-tun-package-convergence.yml`
- Modify: `internal/daemon/e2e_tun_hooks.go` only for narrowly scoped deterministic #261 fault points that cannot be expressed through existing hooks.
- Modify: `internal/daemon/e2e_tun_hooks_test.go` if hooks change.

**Acceptance:**
- deterministic rootless regressions model the exact envelope composition and lifecycle order rather than only status strings;
- installed-package protected-continuation scenario proves a real packaged TUN session is protected, restarts/kills the daemon under systemd, tests an ordinary non-exempt direct path while recovery is in progress, and proves automatic continuation without manual repair;
- terminal scenario induces a deterministic unrecoverable condition, proves envelope presence while data-plane cleanup runs, proves deliberate envelope removal afterward, proves remaining network usability and final disconnected state, and observes no automatic reconnect after the terminal decision;
- a foreign synthetic nftables table/routing baseline survives byte-for-byte/structurally equivalent where the kernel output is order/counter normalized;
- candidate-envelope collision fixture is created by the E2E harness and is removed only by the harness, never by Podlaz.

- [ ] **Step 1: Add failing Go regression/acceptance contract tests** that parse the shell script/workflow and require no manual `ip rule del`, `nft delete`, `resolvectl`, or `recover --execute` repair in the success path.
- [ ] **Step 2: Verify RED** in hosted tests.
- [ ] **Step 3: Implement the package script** using only synthetic public test identifiers and private environment variables without echoing them; keep raw host evidence in the private E2E temp directory and publish only sanitized assertions/artifacts.
- [ ] **Step 4: Wire the script into the existing Ubuntu 24.04 self-hosted package-convergence workflow with a bounded timeout.
- [ ] **Step 5: Verify GREEN** for shell syntax/contract tests and, when the self-hosted runner is available, the real package scenario.
- [ ] **Step 6: Commit** `test: add privacy envelope package acceptance`.

### Task 9: Update canonical behavior/security documentation

**Files:**
- Modify: `docs/state-and-security.md`
- Modify: `docs/packaged-tun-runtime.md`
- Modify: `docs/e2e.md`
- Modify: `docs/man/podlazd.8` only if its current lifecycle description would otherwise be materially false.
- Modify: `docs/debian-package.md` only for packaged service/acceptance behavior owned by that document.

- [ ] **Step 1: Update security/state ownership docs** with the Network Session identity, reconnect-intent vs cleanup-authority distinction, exact envelope authority, collision behavior, atomic replacement, recovery ordering, and cleanup-failure semantics.
- [ ] **Step 2: Update packaged runtime/E2E docs** with crash/restart privacy continuity and terminal teardown acceptance; do not document private host/profile values.
- [ ] **Step 3: Review all docs against Issue #261 and the approved spec** so no text promises direct ISP restoration when the correct contract is remaining-host-network usability.
- [ ] **Step 4: Commit** `docs: document network session privacy envelope`.

### Task 10: Full verification, security review, and PR

**Files:** repository-wide validation only.

- [ ] **Step 1: Require hosted validation** for the exact head:
  ```bash
  test -z "$(gofmt -l .)"
  go test ./...
  go vet ./...
  govulncheck ./...
  go run ./cmd/podlaz version
  go run ./cmd/podlaz completion bash >/dev/null
  go run ./cmd/podlaz completion zsh >/dev/null
  go run ./cmd/podlaz completion fish >/dev/null
  ```
- [ ] **Step 2: Require package/static validation** through the repository CI helpers and ensure the PR workflow is green on the exact commit SHA.
- [ ] **Step 3: Review `master...HEAD`** for unrelated refactors, broad firewall allowances, accidental direct DNS, conntrack-established bypass, global nft flush/delete, foreign-state mutation, name-based ownership inference, lost authority on failure, false disconnected publication, infinite reconnect, and any private values.
- [ ] **Step 4: Review every persistence/mutation pair** and prove authority is durable before mutation and survives every failure path until exact cleanup is verified.
- [ ] **Step 5: Review nftables scripts against current nftables transactional semantics**: replacement must be one batch and no correctness argument may depend on a userspace gap between commands.
- [ ] **Step 6: Run/require the self-hosted Issue #261 installed-package acceptance** because this PR touches TUN, nftables, service lifecycle, crash/recovery, and package behavior. If unavailable, keep the PR draft and state the exact missing host evidence rather than claiming success.
- [ ] **Step 7: Open/update one draft PR linked with `Closes #261`**, documenting system state changed, exact ownership authority, verification, rollback/recovery, terminal ordering, hosted CI results, and self-hosted acceptance status.
