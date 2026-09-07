# Nftables Ownership Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix #307 by replacing presentation-based nftables authority with exact structured semantics, making fresh table creation exclusive, and making every destructive owned-table mutation atomic and stale-generation-safe.

**Architecture:** Keep the existing `PrivacyEnvelopeExecutor`, `NftablesExecutor`, Network Session authority, and recovery ordering. Add one small private nftables snapshot/semantic implementation shared by both executors; use the documented `nft -j` JSON boundary for read/verification, and a narrow private netlink mutation transport only where generation-bound atomic mutation is required. No new public firewall framework, no dynamic sets/maps, and no public state-schema change unless proven necessary.

**Tech Stack:** Go 1.26.6, `encoding/json`, existing command runner abstractions, nftables JSON schema v1, `github.com/google/nftables` pinned to a revision that exposes `GetGen`/`FlushWithGenID`, Ubuntu 24.04 real nftables CI.

**Spec:** `docs/superpowers/specs/2026-09-07-nftables-ownership-safety-design.md`

## Global Constraints

- Durable Network Session protection remains the only Privacy Envelope cleanup authority.
- Transaction state remains the only ordinary `inet podlaz` rollback authority.
- Human-readable nftables output/stderr never grants ownership, absence, or cleanup permission.
- Current Podlaz table flags must be exactly empty; unexpected flags such as `dormant`, `owner`, or `persist` fail closed.
- Unknown/extra table-scoped objects, statements, or expressions fail closed.
- Fresh Podlaz-owned table creation uses exclusive create semantics in the same atomic transaction as initial chains/rules.
- Remove, Replace, and transaction-owned rollback use fresh exact semantic proof plus one generation-bound atomic mutation transaction.
- A stale batch is never replayed. Any bounded retry restarts observation -> semantic verification -> transaction construction from scratch.
- Existing Privacy Envelope composition version remains `1`; v0.2.39 old ICMPv6 spelling remains recoverable only through narrow semantic equivalence.
- No real user IP/domain/profile/credential/subscription/SSID/endpoint data in tests, docs, PRs, logs, or fixtures.
- Temporary spec/plan files are removed before final repository-structure verification and Ready-for-review PR state.

---

## File Structure

- Create `internal/network/executor/nftables_state.go`: strict JSON snapshot model, semantic normalization, exact verification, tri-state table enumeration, coherent generation-bound observation plumbing.
- Create `internal/network/executor/nftables_state_test.go`: JSON schema/object/flag/chain/rule/ICMPv6/presence regressions.
- Create `internal/network/executor/nftables_mutation_linux.go`: narrow Linux netlink mutation transport, generation retrieval/guard, conversion of the limited Podlaz plan vocabulary to nftables netlink objects, guarded remove/replace/rollback.
- Create `internal/network/executor/nftables_mutation_test.go`: transaction construction and stale-generation/race behavior using injected mutation backend/fake connection at the private helper boundary.
- Modify `internal/network/executor/privacy_envelope.go`: structured Exists/Verify, exclusive Apply, guarded Remove/Replace.
- Modify `internal/network/executor/nftables.go`: structured Verify, exclusive Apply, guarded Rollback; retire authority-bearing text parser.
- Modify `internal/network/executor/nftables_verify_output.go`: accept structured JSON and delegate to the shared semantic verifier.
- Modify `internal/daemon/privacy_envelope_plan.go`: emit canonical minimal ICMPv6 expression while preserving composition version 1.
- Modify `internal/daemon/privacy_envelope_lifecycle.go` and existing recovery tests only where needed to preserve crash ordering and consume guarded executor behavior.
- Modify `internal/daemon/network_session_privacy_recovery.go`, `network_session_recovery_plan.go`, `network_session_recovery_status.go`, and focused tests only where the current path can still falsely publish clean state or lose a known Privacy Envelope failure.
- Modify `internal/doctor/stale_resources.go` and tests: structured exact verification for active transaction-owned nftables state.
- Modify `.github/workflows/ci.yml`: install `nftables` and enable bounded real-nft integration coverage in normal PR CI.
- Modify `go.mod` / `go.sum`: pin the narrow netlink dependency required for `GetGen`/`FlushWithGenID`.

---

### Task 1: Establish the production-shaped RED regression and canonical plan

**Files:**
- Modify: `internal/daemon/privacy_envelope_plan.go`
- Modify: `internal/daemon/privacy_envelope_plan_test.go`
- Create/modify: `internal/network/executor/privacy_envelope_canonicalization_test.go`

**Interfaces:**
- Consumes: existing `privacyEnvelopePlanFromAuthority`, `PrivacyEnvelopePlan`.
- Produces: composition-v1 plan using `icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert }` and a production-shaped regression fixture reusable by Task 2.

- [ ] **Step 1: Add a failing production-shaped verifier regression**

Use all seven production rule classes: loopback, TUN egress, bootstrap IPv4, DHCPv4, DHCPv6, ICMPv6 link control, final reject. Feed a canonical live representation whose ICMPv6 rule omits only the redundant explicit `meta nfproto ipv6` predicate and assert the old text verifier rejects it with the same `rule[5] mismatch` class.

```go
func TestPrivacyEnvelopeCanonicalICMPv6RenderingIsSemanticMatch(t *testing.T) {
    plan := productionPrivacyEnvelopePlanForTest()
    canonical := productionCanonicalPrivacyEnvelopeJSONOrLegacyFixture(plan)
    if err := verifyProductionEnvelope(plan, canonical); err != nil {
        t.Fatalf("canonical nftables rendering must verify semantically: %v", err)
    }
}
```

- [ ] **Step 2: Run the focused test and record RED evidence**

Run:

```bash
go test ./internal/network/executor -run 'TestPrivacyEnvelopeCanonicalICMPv6RenderingIsSemanticMatch' -count=1
```

Expected before the fix: FAIL because the authority-bearing verifier still compares presentation spelling.

- [ ] **Step 3: Canonicalize generated ICMPv6 syntax without changing composition version**

Change only the generated expression in `privacyEnvelopePlanFromAuthority`; keep `privacyEnvelopeCompositionVersion = 1`.

- [ ] **Step 4: Verify plan tests**

```bash
go test ./internal/daemon -run 'PrivacyEnvelope.*Composition|PrivacyEnvelope.*Plan' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/privacy_envelope_plan.go internal/daemon/privacy_envelope_plan_test.go internal/network/executor/privacy_envelope_canonicalization_test.go
git commit -m 'test: characterize nftables canonical privacy envelope'
```

---

### Task 2: Build one strict structured semantic verifier

**Files:**
- Create: `internal/network/executor/nftables_state.go`
- Create: `internal/network/executor/nftables_state_test.go`
- Modify: `internal/network/executor/nftables_verify_output.go`

**Interfaces:**
- Produces private types/functions equivalent to:

```go
type nftTableSnapshot struct {
    Family string
    Name string
    Flags []string
    Handle uint64
    Chains map[string]nftChainSnapshot
}

type nftChainSnapshot struct {
    Name string
    Type string
    Hook string
    Priority int
    Policy string
    Device string
    Rules []nftRuleSnapshot
}

func parseNftTableJSON(output, family, table string) (nftTableSnapshot, error)
func verifyNftTableSnapshot(snapshot nftTableSnapshot, plan planner.TunFirewallPlan) error
```

Names may be tightened during implementation, but keep the model private and concrete.

- [ ] **Step 1: Write failing strict-decoder tests**

Cover: missing/unsupported schema, duplicate table/chain identity, `dormant` or any non-empty table flags, changed chain type/hook/priority/policy/device, unexpected set/map/flowtable/named counter/quota/stateful object, unknown rule statement/expression, malformed JSON.

- [ ] **Step 2: Write failing exactness tests**

Cover: extra rule, missing rule, security-relevant rule reorder, changed endpoint, changed TUN interface, verdict, ownership comment, extra predicate, and harmless runtime counter/handle differences.

- [ ] **Step 3: Write semantic-equivalence tests**

Prove only the documented ICMPv6 implicit-family dependency and unordered members of one semantic set normalize; all other missing/additional predicates remain drift.

- [ ] **Step 4: Run RED**

```bash
go test ./internal/network/executor -run 'Nft.*JSON|Nft.*Semantic|PrivacyEnvelopeCanonical' -count=1
```

- [ ] **Step 5: Implement the smallest strict typed decoder and semantic comparison**

Use `encoding/json` with explicit object-kind dispatch. Never use `map[string]any` plus silent default ignoring for security-relevant object/statement kinds. Runtime metadata is parsed only where needed and excluded from composition equality.

- [ ] **Step 6: Convert `VerifyNftablesTableOutput` to structured JSON**

Keep one shared comparison implementation for ordinary `TunFirewallPlan` and Privacy Envelope plans converted to the same expected semantic form.

- [ ] **Step 7: Run GREEN and broader executor tests**

```bash
go test ./internal/network/executor -count=1
```

- [ ] **Step 8: Commit**

```bash
git add internal/network/executor/nftables_state.go internal/network/executor/nftables_state_test.go internal/network/executor/nftables_verify_output.go
git commit -m 'fix: verify owned nftables state semantically'
```

---

### Task 3: Replace stderr-based presence with structured tri-state observation and exclusive create

**Files:**
- Modify: `internal/network/executor/nftables_state.go`
- Modify: `internal/network/executor/privacy_envelope.go`
- Modify: `internal/network/executor/nftables.go`
- Modify: `internal/network/executor/privacy_envelope_test.go`
- Modify/create focused allocation/executor tests under `internal/daemon/**` and `internal/network/executor/**`.

**Interfaces:**
- Produces an internal tri-state table lookup (`absent`, `present`, `unknown/error`) from successful structured table enumeration.
- `PrivacyEnvelopeTableExists` remains the daemon-facing compatibility method but maps only proven structured states; unknown returns an error.

- [ ] **Step 1: Write failing absence/presence/unknown tests**

Prove malformed JSON, unsupported object representation, and observation command failure are never absence.

- [ ] **Step 2: Write the fresh-create race test**

Characterize `observe absent -> foreign same-name object appears -> Apply` and assert no chain/rule mutation can occur in that foreign table.

- [ ] **Step 3: Run RED**

```bash
go test ./internal/network/executor ./internal/daemon -run 'PrivacyEnvelope.*(Presence|Allocation|Create)|Nft.*Presence' -count=1
```

- [ ] **Step 4: Change both fresh apply scripts from `add table` to `create table`**

The `create table` command remains in the same nftables transaction as chains/rules. Do not retry the same identity after EEXIST.

- [ ] **Step 5: Route Privacy Envelope Exists/occupancy and ordinary table observation through structured enumeration**

Do not use `resourceMissing(stderr)` for authoritative absence.

- [ ] **Step 6: Run GREEN**

```bash
go test ./internal/network/executor ./internal/daemon -run 'PrivacyEnvelope|Nftables' -count=1
```

- [ ] **Step 7: Commit**

```bash
git add internal/network/executor internal/daemon
git commit -m 'fix: make nftables presence and creation collision safe'
```

---

### Task 4: Add coherent generation snapshots and one guarded mutation transport

**Files:**
- Create: `internal/network/executor/nftables_mutation_linux.go`
- Create: `internal/network/executor/nftables_mutation_test.go`
- Modify: `internal/network/executor/nftables_state.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Pin `github.com/google/nftables` to a current reviewed revision that includes `GetGen` and `FlushWithGenID`.
- Private mutation API should be no broader than the existing needs, e.g.:

```go
type nftVerifiedSnapshot struct {
    Table nftTableSnapshot
    Generation uint32
}

func observeVerifiedNftTable(ctx context.Context, runner CommandRunner, family, table string, expected planner.TunFirewallPlan) (nftVerifiedSnapshot, error)
func removeNftTableAtGeneration(ctx context.Context, snapshot nftVerifiedSnapshot) error
func replaceNftTableAtGeneration(ctx context.Context, snapshot nftVerifiedSnapshot, next planner.TunFirewallPlan) error
```

Do not expose these outside `internal/network/executor`.

- [ ] **Step 1: Write coherent-observation RED tests**

Inject generation `10 -> 11` around observation and assert the partial snapshot is discarded. Prove bounded observation retry starts from scratch and never turns contention into absence.

- [ ] **Step 2: Write stale mutation RED tests**

Cover:

1. exact verify -> same-name delete/recreate -> Remove refuses;
2. exact verify -> semantic mutation on same table -> Remove refuses;
3. exact verify -> replacement object appears -> Replace refuses;
4. unrelated ruleset generation change returns stale/retryable outcome without mutation.

- [ ] **Step 3: Add the narrow dependency**

Use an exact pinned pseudo-version/revision that contains `GetGen`/`FlushWithGenID`; do not import unrelated higher-level firewall packages.

- [ ] **Step 4: Implement coherent generation capture**

Obtain generation before/after `nft -j` observation; accept the semantic snapshot only when unchanged. Keep retries bounded and read-only.

- [ ] **Step 5: Implement generation-guarded remove**

Build one netlink batch and call `FlushWithGenID(observedGen)`. A stale generation is returned as a typed/internal stale-evidence error; never automatically replay the same batch.

- [ ] **Step 6: Implement the limited plan-to-netlink encoder required for atomic Replace**

Support only Podlaz's existing emitted vocabulary: oifname equality/inequality as required by current plans, IPv4 destination, nfproto, UDP source/destination ports, ICMPv6 type set, counter, accept/drop/reject, base-chain metadata, and nft comment userdata. Unknown planned syntax is an implementation error before mutation.

- [ ] **Step 7: Implement atomic generation-guarded Replace**

One batch must delete the verified old table and create the complete replacement table/chains/rules. There must be no separate `nft -f` second phase.

- [ ] **Step 8: Run GREEN and race-focused tests**

```bash
go test ./internal/network/executor -run 'Generation|Stale|Remove|Replace|Race' -count=1
go test -race ./internal/network/executor -count=1
```

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/network/executor/nftables_mutation_linux.go internal/network/executor/nftables_mutation_test.go internal/network/executor/nftables_state.go
git commit -m 'fix: guard nftables mutations by ruleset generation'
```

---

### Task 5: Wire guarded semantics into both production executors and doctor

**Files:**
- Modify: `internal/network/executor/privacy_envelope.go`
- Modify: `internal/network/executor/nftables.go`
- Modify: `internal/network/executor/privacy_envelope_test.go`
- Modify existing nftables executor tests.
- Modify: `internal/doctor/stale_resources.go`
- Modify existing doctor stale-resource tests.

**Interfaces:**
- `PrivacyEnvelopeExecutor.Verify/Remove/Replace` and `NftablesExecutor.Verify/Rollback` remain their existing public/internal method signatures.
- Internal methods may combine observation+verification+mutation to avoid stale evidence while preserving caller compatibility.

- [ ] **Step 1: Write failing executor behavior tests**

Assert `Verify` invokes structured observation, `Remove/Replace/Rollback` reject drift/stale generation, and ordinary `inet podlaz` uses identical exact semantics.

- [ ] **Step 2: Write failing doctor regression**

Doctor/lifecycle exact verification must consume `nft -j` and the shared semantic verifier; human formatting differences must not warn, true drift must warn.

- [ ] **Step 3: Wire `PrivacyEnvelopeExecutor` to the new helpers**

Keep Apply as exclusive CLI batch unless generation-bound mutation is needed. Remove/Replace use the guarded mutation transport.

- [ ] **Step 4: Wire `NftablesExecutor`**

Verify uses structured semantics; Rollback must re-observe and prove exact transaction-owned composition before generation-guarded deletion rather than deleting by family/name alone.

- [ ] **Step 5: Remove authority-bearing text parser usage**

Delete or demote old parser helpers if no diagnostic-only consumer remains. Search repository-wide for `parseOwnedNftTable`, `verifyExactNftChains`, and human `nft -y list table` authority paths.

- [ ] **Step 6: Run GREEN**

```bash
go test ./internal/network/executor ./internal/doctor -count=1
```

- [ ] **Step 7: Commit**

```bash
git add internal/network/executor internal/doctor
git commit -m 'fix: share exact nftables authority across lifecycle paths'
```

---

### Task 6: Prove crash/restart and v0.2.39 recovery semantics

**Files:**
- Modify: `internal/daemon/privacy_envelope_lifecycle_test.go`
- Modify: `internal/daemon/network_session_privacy_recovery.go` only if tests expose a real ordering gap.
- Modify: `internal/daemon/network_session_replacement_recovery_test.go`
- Modify/create focused package/restart recovery tests under `internal/daemon/**`.

**Interfaces:**
- Existing lifecycle persistence contract remains unchanged: persist `removing` before delete; clear protection only after proven absence.

- [ ] **Step 1: Add crash-boundary tests**

Cover interruption:

1. after exact data-plane cleanup before envelope removal;
2. after protection state becomes `removing` before kernel delete;
3. after kernel delete before `SetProtection(nil)`;
4. during startup/terminal convergence with protection authority retained.

- [ ] **Step 2: Add v0.2.39 compatibility regression**

Persist composition-version-1 authority with old explicit `meta nfproto ipv6 icmpv6 ...` semantics, present the semantically equivalent canonical live table, restart recovery, and prove exact terminal removal succeeds without broad deletion or schema migration.

- [ ] **Step 3: Add genuine-drift negative recovery test**

Change a security-relevant predicate/comment/verdict and prove recovery keeps protection authority and refuses mutation.

- [ ] **Step 4: Run RED/GREEN against executor integration**

```bash
go test ./internal/daemon -run 'PrivacyEnvelope|NetworkSession.*Recovery|Replacement' -count=1
go test -race ./internal/daemon -run 'PrivacyEnvelope|NetworkSession' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/daemon
git commit -m 'test: prove privacy envelope restart-safe recovery'
```

---

### Task 7: Prevent false clean recovery/status publication

**Files:**
- Modify: `internal/daemon/network_session_recovery_plan.go`
- Modify: `internal/daemon/network_session_recovery_status.go`
- Modify: `internal/daemon/network_session_recovery_plan_test.go`
- Modify: `internal/daemon/http_server.go` only if the existing follow-up routing fails the new tests.
- Modify CLI/API tests only if an existing public field needs different population, not a schema change.

**Interfaces:**
- Reuse `api.NetworkSessionRecoveryState.CleanupAuthority = session-protection` and existing startup-scan statuses.

- [ ] **Step 1: Write failing protection-only recovery tests**

Create a current-boot Network Session with protection authority and zero transaction candidates. Assert status is not clean and `recover --execute --yes` exposes/executes Network Session convergence.

- [ ] **Step 2: Write failure-cause preservation test**

Known Privacy Envelope verification/removal failure must remain a typed recovery warning/state using existing public fields where possible, not be collapsed to success or generic internal error.

- [ ] **Step 3: Implement only the projection/routing changes required by the tests**

Do not add a new API field unless the current model provably cannot represent the state; stop and review separately if that occurs.

- [ ] **Step 4: Run GREEN**

```bash
go test ./internal/daemon ./internal/app/cli -run 'Recovery|Recover|Status' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/daemon internal/app/cli internal/api
git commit -m 'fix: keep privacy authority visible to recovery'
```

---

### Task 8: Add real nftables round-trip to normal PR CI

**Files:**
- Create/modify: `internal/network/executor/privacy_envelope_real_nft_test.go`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Real test runs only when `PODLAZ_TEST_REAL_NFT=1` and skips otherwise.

- [ ] **Step 1: Add the integration test**

Use a unique Podlaz-valid table name and RFC/example addresses. Apply production-shaped plan through production Apply, list through production structured observation, verify through production semantic verifier, remove through guarded production Remove, and prove exact absence. Cleanup must run in `t.Cleanup` even on failure and touch only the unique table.

- [ ] **Step 2: Prove sibling `inet podlaz` round-trip uses the same verifier**

Run a transaction-owned firewall plan in an isolated network namespace/test process if required to avoid colliding with the fixed table name.

- [ ] **Step 3: Enable on Ubuntu 24.04 CI**

Install `nftables` in the Go test job and set `PODLAZ_TEST_REAL_NFT=1`. Do not grant broader host-network mutation than needed for the isolated nftables test.

- [ ] **Step 4: Run locally when privileges permit**

```bash
PODLAZ_TEST_REAL_NFT=1 go test ./internal/network/executor -run 'RealNft' -count=1
```

If the local environment lacks CAP_NET_ADMIN, record that as not-run and rely on the explicit CI job; do not claim a pass.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml internal/network/executor/privacy_envelope_real_nft_test.go
git commit -m 'test: exercise real nftables ownership contract in ci'
```

---

### Task 9: Full verification, dependency/scope review, temporary-doc cleanup, and PR

**Files:**
- Delete before final verification: `docs/superpowers/specs/2026-09-07-nftables-ownership-safety-design.md`
- Delete before final verification: `docs/superpowers/plans/2026-09-07-nftables-ownership-safety.md`
- Review all modified files.

- [ ] **Step 1: Run formatting and focused race checks**

```bash
test -z "$(gofmt -l .)"
go test -race ./internal/network/executor ./internal/daemon
```

- [ ] **Step 2: Run repository verification**

```bash
go test ./...
go vet ./...
govulncheck ./...
bash scripts/ci/repository-structure.sh --final
```

Also run repository workflow/shell/package checks required by `AGENTS.md` and the touched CI/package surface.

- [ ] **Step 3: Review dependency choice**

Confirm the pinned `google/nftables` revision is needed only for generation-bound netlink mutation, has no simpler already-present equivalent, and does not introduce unnecessary public abstractions. Run `govulncheck` after the final module graph.

- [ ] **Step 4: Review exact #307 acceptance coverage**

Create a checklist mapping every issue-body and follow-up-comment acceptance criterion to a passing test/CI/explicit not-run real-host release qualification. Do not claim exact-candidate real-host TUN qualification if it was not run.

- [ ] **Step 5: Remove temporary spec/plan and re-run final structure test**

```bash
git rm docs/superpowers/specs/2026-09-07-nftables-ownership-safety-design.md
git rm docs/superpowers/plans/2026-09-07-nftables-ownership-safety.md
bash scripts/ci/repository-structure.sh --final
```

- [ ] **Step 6: Final commit/push**

```bash
git add -A
git commit -m 'fix: make nftables ownership verification race safe'
git push origin agent/nftables-ownership-safety
```

- [ ] **Step 7: Open one PR to `master`**

PR body must state:

- fixes #307;
- supersedes the incomplete runtime approach in draft PR #306 without merging/cherry-picking it blindly;
- semantic JSON verification and exact drift rules;
- exclusive table creation;
- generation-guarded Remove/Replace/Rollback and `ERESTART` behavior;
- v0.2.39 compatibility/recovery evidence;
- exact validation commands and results;
- real-host release qualification still required if not executed;
- no real private user network data included.
