# Repository Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Issues #280–#287 in one behavior-preserving pull request that materially reduces permanent documentation, historical context, issue-oriented naming, duplicated E2E infrastructure, accidental code/test complexity, and daemon orchestration working-set size.

**Architecture:** Treat the cleanup as a sequence of preservation-first migrations. Capture baseline evidence before destructive changes; distill contracts into four permanent knowledge surfaces; rename permanent tests/scripts by invariant; consolidate only semantically identical helpers; simplify Go composition and proven duplication without redesigning product behavior; then remove temporary workflow artifacts and verify the final tree against baseline mappings and context-budget targets.

**Tech Stack:** Go, Bash, GitHub Actions YAML, Debian packaging scripts, shellcheck, govulncheck, standard repository search/tooling.

**Spec:** `docs/superpowers/specs/2026-09-02-repository-simplification-design.md`

**Issues:** #280, #281, #282, #283, #284, #285, #286, umbrella #287.

## Global Constraints

- Do not intentionally change public CLI commands, flags, exit codes, JSON semantics, authorization behavior, TUN/network ownership, transaction/rollback/recovery/privacy semantics, boot-autostart semantics, or daemon privilege boundaries.
- Preserve exact durable ownership as the only privileged mutation/cleanup authority and preserve fail-closed behavior on ambiguous evidence.
- Preserve meaningful race, crash, rollback, recovery, privacy, coexistence, lifecycle, package, and E2E regression coverage.
- If cleanup exposes a genuine product defect, keep the existing behavior in this PR and track the behavior change separately. Issue #273 remains outside this cleanup scope.
- Do not add real user IPs, domains, SSIDs, profile IDs, credentials, subscription data, or private endpoint values to code, tests, docs, Issues, PR evidence, or fixtures.
- Optimize task-specific working-set size, not minimum file count or minimum line count.
- Temporary simplification spec/plan files must be removed before the final PR is complete.
- Use frequent reviewable commits; each task below should leave the branch testable.

---

### Task 1: Capture the preservation baseline and migration inventory

**Files:**
- Create temporarily: `docs/superpowers/repository-simplification-baseline.md`
- Inspect: `README.md`, `AGENTS.md`, `docs/**`, `scripts/e2e/**`, `scripts/ci/**`, `.github/workflows/**`, `internal/**`, `packaging/**`

**Interfaces:**
- Produces: one temporary baseline document containing exact inventories and migration tables consumed by Tasks 2–10.
- Consumes: current `master` behavior and file layout only; do not edit product sources in this task.

- [ ] **Step 1: Record documentation inventory**

In the temporary baseline file, record every permanent prose file under the repository root/docs tree with byte size and category: public contract, engineering invariant, agent workflow, historical/process artifact, generated/installed reference, or candidate obsolete/duplicate.

Required summary fields:

```text
permanent_prose_file_count=<n>
permanent_prose_bytes=<n>
superpowers_spec_count=<n>
superpowers_spec_bytes=<n>
superpowers_plan_count=<n>
superpowers_plan_bytes=<n>
```

- [ ] **Step 2: Record issue-oriented artifact inventory**

Search permanent source/test/workflow paths and test function names for `issue[0-9]+`, `Issue[0-9]+`, and issue-specific workflow labels/artifact names. Record a table:

```text
old artifact | protected invariant | callers/workflows | proposed domain name | action
```

Do not choose names from issue titles alone; inspect each script/test to state what it actually proves.

- [ ] **Step 3: Record E2E duplication inventory**

Search for repeated readiness, package, diagnostics, network-observation, execution, assertion, and cleanup helpers. At minimum compare all `wait_for_daemon_socket*`, service-ready checks, package provenance helpers, `runuser` wrappers, status polling, and diagnostics capture.

Record for each candidate:

```text
helper concept | current definitions | semantic differences | consolidation decision
```

- [ ] **Step 4: Record Go/test simplification candidates**

Inventory large/mixed orchestration functions and proven duplicate/dead candidates. `internal/daemon/server.go` must be explicitly mapped by responsibility: setup, revalidation/reconciliation, session/boot startup, HTTP registration, listener serving, shutdown.

For each deletion candidate record the proof method: no references/runtime registration, superseded identical implementation, or test-backed merge.

- [ ] **Step 5: Record representative context-budget baseline**

Use one consistent byte-to-token approximation for four task classes:

```text
CLI/public output
Daemon lifecycle/recovery
TUN/networking
Package/E2E
```

The estimate should include the documentation the current `AGENTS.md` tells an agent to read plus the most directly relevant source/test files. State the approximation method; do not imply tokenizer precision.

- [ ] **Step 6: Commit baseline evidence**

```bash
git add docs/superpowers/repository-simplification-baseline.md
git commit -m "docs: capture repository simplification baseline"
```

Expected: only the temporary baseline file changes.

---

### Task 2: Build the four permanent knowledge surfaces (#280)

**Files:**
- Modify: `README.md`
- Modify: `docs/cli.md`
- Create: `ARCHITECTURE.md`
- Modify: `AGENTS.md`
- Test/update as needed: documentation/CLI contract tests under `internal/**` and `scripts/e2e/**`

**Interfaces:**
- Produces: the only four permanent prose knowledge surfaces used by later doc-deletion tasks.
- Consumes: Task 1 document classification and current contracts from existing docs/code/tests.

- [ ] **Step 1: Add a failing repository knowledge-surface contract test**

Add a focused Go test in the existing repository-level contract-test location (prefer `scripts/e2e` if that is where prose/path contracts already live) that checks:

```text
README.md exists
docs/cli.md exists
ARCHITECTURE.md exists
AGENTS.md exists
```

At this stage do not require other docs to be absent yet; that belongs to Task 3.

Run the focused test and confirm it fails because `ARCHITECTURE.md` does not exist.

- [ ] **Step 2: Write concise `ARCHITECTURE.md`**

Keep it intentionally compact. Required sections:

```text
System boundaries
State and authority model
Privileged networking lifecycle
Current-health vs durable-state distinction
Failure/recovery rules
Privacy/redaction rules
Source-tree map
```

Required explicit invariants:

```text
CLI owns user intent and never directly owns privileged network mutation.
podlazd owns privileged network mutation.
Live-state resemblance never grants mutation/cleanup authority.
Apply -> verify -> commit and rollback/recovery are fail-closed.
Network Session, transaction, boot-autostart policy, boot attempt, product terminal state and current TUN health are distinct.
Ambiguous ownership/evidence never grants cleanup permission.
Public logs/tests/evidence must not expose private endpoint/profile/network data.
```

Do not copy implementation walkthroughs or issue history.

- [ ] **Step 3: Rewrite `AGENTS.md` as workflow + router**

Remove duplicated source-of-truth/canonical-context lists. Default reading must become approximately:

```text
Always: AGENTS.md + README.md
Public CLI task: + docs/cli.md
Cross-cutting lifecycle/network/state safety task: + ARCHITECTURE.md
Then inspect task-relevant code/tests; do not preload unrelated docs.
```

Keep PR workflow, safety discipline, validation expectations, and source-tree routing. Remove product-detail duplication now owned by code/tests/`ARCHITECTURE.md`/`docs/cli.md`.

- [ ] **Step 4: Distill `README.md`**

Keep product purpose, supported capabilities, high-level architecture, installation/quick start, and links only to `docs/cli.md`, `ARCHITECTURE.md`, and `AGENTS.md` where appropriate. Do not turn README into release/E2E/package engineering documentation.

- [ ] **Step 5: Distill public CLI material into `docs/cli.md`**

Compare CLI code/tests and existing docs. Retain only current public commands, flags, aliases/completion behavior, JSON/output/exit-code contract, confirmations and user-visible safety semantics. Remove issue narrative and internal implementation detail.

- [ ] **Step 6: Run focused contracts**

```bash
go test ./scripts/e2e -run 'Knowledge|Docs|CLI' -count=1
```

Use exact available test regex after the new test is named. Also run relevant CLI package tests discovered from repository inventory.

Expected: PASS.

- [ ] **Step 7: Commit permanent knowledge surfaces**

```bash
git add README.md docs/cli.md ARCHITECTURE.md AGENTS.md scripts/e2e internal
git commit -m "docs: reduce permanent knowledge surface"
```

---

### Task 3: Retire historical and overlapping documentation (#281)

**Files:**
- Delete after distillation: obsolete docs identified by Task 1, including completed `docs/superpowers/specs/*` and `docs/superpowers/plans/*` except the temporary active simplification artifacts until Task 10
- Delete/update as justified: `docs/README.md`, `docs/state-and-security.md`, `docs/e2e.md`, `docs/debian-package.md`, `docs/release.md`, `docs/daemon-api.md`, `docs/e2e-proxy-data-plane.md`, `docs/packaged-tun-runtime.md`, `docs/provider-xray-profiles.md`, `docs/tun-connect-manual-test.md`, `docs/tun-uplink-revalidation.md`, `docs/man/**`
- Modify references: `.github/workflows/**`, `packaging/**`, `scripts/**`, `internal/**`, `README.md`, `AGENTS.md`

**Interfaces:**
- Consumes: four permanent knowledge surfaces from Task 2 and Task 1 deletion map.
- Produces: active tree with no obsolete/history prose competing with code/current contracts.

- [ ] **Step 1: Strengthen the knowledge-surface contract test to fail on extra permanent prose**

The test must enumerate tracked `.md`/man prose paths while allowing only temporary simplification workflow files explicitly until Task 10. It should fail while old docs are still present.

- [ ] **Step 2: Distill each deletion candidate**

For every document in Task 1, mark its current statements as:

```text
public contract -> README/docs/cli
engineering invariant -> ARCHITECTURE
workflow -> AGENTS
implementation detail enforced by code/tests -> delete prose
obsolete/history/evidence -> delete
```

Update the four retained files only if a genuinely unique current contract is otherwise lost.

- [ ] **Step 3: Remove completed Superpowers artifacts**

Delete all implemented specs/plans from active history storage. Keep only the active simplification design/plan temporarily because execution still depends on them.

- [ ] **Step 4: Remove overlapping docs and man pages**

Delete the candidates approved by the distillation table. Before each deletion, search for path references and update code/workflows/package manifests accordingly.

If package content explicitly installs a man page or removed doc, either remove that packaging entry if the installed reference is no longer part of the agreed product surface or preserve a generated/non-prose equivalent only if product/package tests prove it is required. Do not silently break Debian content expectations.

- [ ] **Step 5: Replace prose-grep tests with contract tests**

For tests such as issue-specific docs contract tests, retain the underlying behavior assertion in code/API/CLI tests where possible. Delete tests whose only purpose is asserting obsolete prose phrases/paths after the real invariant is covered elsewhere.

- [ ] **Step 6: Search for stale paths**

Repository search must find no non-historical references to deleted documentation paths.

- [ ] **Step 7: Run documentation/package-focused checks**

Run the knowledge-surface contract, CLI contract tests, package-content tests, and workflow-reference checks identified in Task 1.

Expected: PASS.

- [ ] **Step 8: Commit documentation retirement**

```bash
git add -A
git commit -m "docs: retire historical and overlapping documentation"
```

---

### Task 4: Rename permanent Issue-oriented tests and E2E artifacts by invariant (#282)

**Files:**
- Rename: issue-oriented `scripts/e2e/issue*-*.sh`
- Rename: issue-oriented `scripts/e2e/issue*_test.go`
- Rename/move: `scripts/e2e/lib/issue*.sh`, `scripts/e2e/lib/issue*_state.py` where behavior-oriented names are clear
- Rename/update: `scripts/ci/issue*-contract.sh`
- Modify: `.github/workflows/**`
- Modify: any code/test references and selectors

**Interfaces:**
- Consumes: old→new mapping from Task 1.
- Produces: stable domain-oriented names consumed by Task 5 helper consolidation and final migration evidence.

- [ ] **Step 1: Add a failing naming guard test/script**

Add a lightweight repository check that fails on new permanent files matching patterns such as:

```text
scripts/e2e/issue[0-9]*
scripts/ci/issue[0-9]*
internal/**/*issue[0-9]*
```

Allow only explicitly historical comments/Issue links, not permanent filenames. Run it and confirm FAIL on current names.

- [ ] **Step 2: Finalize the domain taxonomy**

Use the Task 1 table to choose exact names that state the invariant. Do not merge two scenarios because they share an Issue number. Record every rename in the baseline migration table.

- [ ] **Step 3: Rename scripts/helpers/tests**

Use Git-preserving renames where possible. Update test function names from `TestIssueNNN...` to names such as `TestActiveSessionUpgrade...`, `TestPrivacyEnvelope...`, etc., based on actual protected behavior.

- [ ] **Step 4: Update workflows and test selectors**

Update `.github/workflows/e2e*.yml`, `scripts/ci/**`, Go `-run` selectors, artifact names, shell source paths, and any generated evidence labels that depend on filenames.

Do not change runtime scenario order, mutation logic, retry semantics, or assertions.

- [ ] **Step 5: Run rename-focused checks**

Run repository naming guard, `go test ./scripts/e2e/...` as supported, shell syntax/lint on renamed scripts, and workflow reference searches.

Expected: PASS and no old permanent issue-oriented filenames remain.

- [ ] **Step 6: Commit taxonomy migration**

```bash
git add -A
git commit -m "test: rename regressions by product invariant"
```

---

### Task 5: Consolidate duplicated E2E shell infrastructure (#283)

**Files:**
- Modify/create cohesively under: `scripts/e2e/lib/**`
- Modify callers under: `scripts/e2e/**`
- Modify tests under: `scripts/e2e/*_test.go`

**Interfaces:**
- Consumes: renamed domain-oriented scenarios from Task 4 and duplication table from Task 1.
- Produces: focused shared helpers with explicit semantics and callers free of exact duplicate implementations.

- [ ] **Step 1: Write contract tests for the first shared readiness primitive**

Before extraction, add deterministic tests that inspect or execute the helper contract for the semantic variants actually found. The public helper names must distinguish at least:

```text
wait_for_daemon_socket
wait_for_daemon_ready
wait_for_service_active
```

Only implement the helpers that current callers genuinely need.

- [ ] **Step 2: Extract daemon/service readiness helpers**

Move semantically identical polling to a focused daemon/service library. Prefer an explicit bounded duration/deadline parameter or clearly named default over copy-pasted loop counts, while preserving each caller's effective timeout.

- [ ] **Step 3: Run affected readiness scenarios/contracts**

Run deterministic contract tests and shellcheck for all changed callers. Expected: PASS.

- [ ] **Step 4: Extract package provenance/execution helpers**

Consolidate exact duplicate package identity/provenance and normal-user Podlaz execution helpers where semantics match. Keep scenario-specific release/candidate authority local when it differs.

- [ ] **Step 5: Extract diagnostics/network observation helpers**

Consolidate read-only observations and diagnostics capture only where command set, redaction, failure tolerance and output contract match. Do not centralize scenario-specific mutation or broad cleanup.

- [ ] **Step 6: Extract common assertion/reporting primitives**

Move only exact shared assertions/error formatting. Avoid a generic `utils.sh` or abstraction that requires callers to pass dozens of mode flags.

- [ ] **Step 7: Verify apparent duplicates intentionally left local**

Update the migration table with cases that remain separate because readiness definition, ownership, cleanup authority, diagnostics ordering, or failure semantics differ.

- [ ] **Step 8: Run E2E shell validation**

Run shellcheck/repository shell lint and deterministic Go contract tests for every modified script family. Syntax-check all sourced libraries.

- [ ] **Step 9: Commit E2E consolidation**

```bash
git add scripts/e2e scripts/ci .github/workflows
git commit -m "test: consolidate shared e2e infrastructure"
```

---

### Task 6: Simplify repository-wide production/test code with evidence (#284)

**Files:**
- Modify/delete: exact candidates recorded in Task 1 across `internal/**`, tests, and supporting scripts
- Exclude `internal/daemon/server.go` composition-root work reserved for Task 7 unless a prerequisite extraction is unavoidable

**Interfaces:**
- Consumes: dead/duplicate/responsibility inventory from Task 1.
- Produces: smaller accidental-complexity surface without package-graph churn or coverage loss.

- [ ] **Step 1: For each dead-code candidate, prove non-use before deletion**

Use repository search plus runtime registration/call-path inspection. Record the proof in the migration table. If a candidate participates through interfaces, build tags, init registration, command dispatch, workflow execution, or packaging, it is not dead merely because direct references are absent.

- [ ] **Step 2: Delete one proven-dead cluster and run focused tests**

Remove the smallest cohesive dead cluster first, run its package tests, then commit or continue only if PASS.

- [ ] **Step 3: Consolidate repeated state/error/test helpers**

For each near-duplicate, compare semantics and error classification. Extract one implementation only when callers require the same behavior. Use domain-specific names; do not introduce generic helper packages.

- [ ] **Step 4: Consolidate duplicated test fixtures/builders**

Move identical fixture setup into focused test helpers while keeping regression-specific assertions in the tests that own them.

- [ ] **Step 5: Split mixed-responsibility large files only where locality improves**

A split is justified when common tasks can thereafter inspect one responsibility-focused file instead of an unrelated large implementation. Keep package boundaries unchanged unless dependency direction becomes materially clearer.

- [ ] **Step 6: Run package-focused tests after each substantial change**

For each touched package:

```bash
go test ./path/to/package -count=1
```

Run race-focused tests if the affected code is concurrency-sensitive and existing test setup supports it.

- [ ] **Step 7: Commit repository-wide code/test simplification**

```bash
git add -A
git commit -m "refactor: remove accidental code and test complexity"
```

---

### Task 7: Refactor daemon composition root without changing lifecycle semantics (#285)

**Files:**
- Modify: `internal/daemon/server.go`
- Create/modify responsibility-focused files inside `internal/daemon`, exact names derived from current code, e.g. `runtime_wiring.go`, `http_server.go`, `shutdown.go`, `startup_runtime.go` only when each has one cohesive purpose
- Modify existing daemon tests rather than duplicating covered regressions

**Interfaces:**
- Consumes: current `Server` API and existing lifecycle/session/revalidation/boot components unchanged.
- Produces: `Server.Run` as high-level composition with extracted private wiring; no public API or package boundary change required.

- [ ] **Step 1: Characterize `Server.Run` ordering with existing/focused tests**

Map and ensure tests cover:

```text
operation-lock setup before lifecycle mutation admission
revalidation/retry/coordinator wiring
Network Session continuation before boot-autostart convergence
startup mutation gate behavior
HTTP handler authorization wiring
listener startup and event-source startup
shutdown ordering/final network mutation
status/doctor publication stability
```

Add only missing characterization tests. Run them before refactoring and confirm PASS.

- [ ] **Step 2: Extract TUN/revalidation runtime wiring**

Create a private cohesive constructor/type that builds reconciliation runtime, health runtime, coordinator, retry scheduler and automatic executor dependencies while preserving the current initialization/order constraints. Do not change algorithms or state models.

Run focused revalidation/reconciliation/operation-lock tests. Expected: PASS.

- [ ] **Step 3: Extract Network Session/boot startup wiring**

Move continuation store, session lifecycle, startup gate, manifest/attempt store and boot startup orchestration into a private component whose inputs/outputs are explicit. Preserve same-boot continuation and gate-release semantics exactly.

Run boot-autostart/network-session regressions. Expected: PASS.

- [ ] **Step 4: Extract HTTP handler/server wiring**

Move status/doctor/recover/boot/lifecycle route registration and listener serving into focused functions without changing paths, methods, JSON encoding, authorization, fallback socket behavior, or logging classifications.

Run handler/authorization/client contract tests. Expected: PASS.

- [ ] **Step 5: Isolate shutdown coordination**

Keep existing shutdown intent, HTTP quiescence, operation serialization and final lifecycle mutation order. If shutdown code is already isolated, simplify call boundaries rather than moving for aesthetics.

Run shutdown/restart/upgrade regressions. Expected: PASS.

- [ ] **Step 6: Review final `Server.Run`**

It should read as a composition sequence and high-level lifecycle, not inline implementation of each subsystem. Reject any extraction that adds more indirection than locality benefit.

- [ ] **Step 7: Run daemon tests**

```bash
go test ./internal/daemon -count=1
go test ./internal/daemon -race -count=1
```

If `-race` is infeasible in the available environment, record that for Task 9 instead of weakening tests.

- [ ] **Step 8: Commit daemon structural refactor**

```bash
git add internal/daemon
git commit -m "refactor: simplify daemon composition root"
```

---

### Task 8: Add lightweight anti-regression guardrails (#286)

**Files:**
- Modify/add: repository contract test under `scripts/e2e` or another existing repository-policy test location
- Modify: `.github/workflows/ci.yml` or existing CI contract script only if needed to execute the check

**Interfaces:**
- Consumes: final naming/documentation structure from Tasks 2–7.
- Produces: cheap checks preventing recurrence of the highest-value context-debt patterns.

- [ ] **Step 1: Finalize a permanent knowledge-surface check**

The test must reject new permanent prose outside:

```text
README.md
AGENTS.md
ARCHITECTURE.md
docs/cli.md
```

while ignoring generated artifacts/vendor/license files as appropriate. By Task 10 temporary simplification files will also be absent.

- [ ] **Step 2: Finalize issue-oriented filename guard**

Reject permanent `issueNNN` source/test/script filenames in maintained product/test directories. Do not prohibit Issue references in commit history or a short diagnostic comment.

- [ ] **Step 3: Add broken-reference check only if simple**

Prefer a small deterministic test for repository-owned links/paths used by retained docs and workflows. Do not add a custom documentation framework.

- [ ] **Step 4: Ensure CI runs the guards**

Wire them into an existing Go/shell contract stage instead of creating another heavyweight workflow where possible.

- [ ] **Step 5: Run guard tests and commit**

```bash
git add scripts .github
git commit -m "test: guard repository context hygiene"
```

---

### Task 9: Execute full preservation validation and compute before/after metrics (#286/#287)

**Files:**
- Modify temporarily: `docs/superpowers/repository-simplification-baseline.md`
- No product changes in this task unless a validation failure proves an earlier refactor was incorrect; fix the originating task semantics, not the test.

**Interfaces:**
- Consumes: final code/docs/test structure from Tasks 2–8.
- Produces: final migration evidence and metrics for PR description.

- [ ] **Step 1: Run formatting and unit/integration tests**

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 2: Run vulnerability validation**

```bash
govulncheck ./...
```

Expected: PASS or only documented non-actionable findings already present at baseline; do not hide new findings.

- [ ] **Step 3: Run shell/workflow/package checks**

Run repository shellcheck/lint commands, deterministic E2E contract tests, workflow-reference validation, and Debian package/content tests affected by doc/man/script renames. Use the exact commands discovered in CI/Makefile/scripts during Task 1.

Expected: PASS.

- [ ] **Step 4: Run targeted race/concurrency validation**

At minimum rerun concurrency-sensitive daemon tests affected by Task 7. If full `go test -race ./...` is practical, run it; otherwise record the narrower executed scope.

- [ ] **Step 5: Decide whether real-host E2E is required**

If Tasks 4–5 only renamed scripts/extracted read-only/readiness helpers with deterministic equivalence, document why destructive real-host execution is not required. If shared host-mutation or cleanup behavior changed, execute the corresponding real-host validation before completion or explicitly mark the PR unverified/blocking.

- [ ] **Step 6: Compute final documentation/context metrics**

Using the same Task 1 method, record:

```text
final_permanent_prose_file_count
final_permanent_prose_bytes
final_superpowers_completed_spec_count=0
final_superpowers_completed_plan_count=0
before/after context estimate for each representative task class
```

- [ ] **Step 7: Complete migration tables**

Ensure mappings cover:

```text
deleted doc -> retained contract location or no-prose reason
old issue artifact -> new behavior artifact
duplicate helper -> shared helper or intentional separation
Go responsibility -> old/new location
dead code -> deletion proof
```

- [ ] **Step 8: Commit validation evidence updates**

The evidence file remains temporary until Task 10, so this commit is allowed on the feature branch:

```bash
git add docs/superpowers/repository-simplification-baseline.md
git commit -m "test: record repository simplification validation"
```

---

### Task 10: Remove temporary workflow artifacts and prepare the single closing PR (#280–#287)

**Files:**
- Delete: `docs/superpowers/specs/2026-09-02-repository-simplification-design.md`
- Delete: `docs/superpowers/plans/2026-09-02-repository-simplification.md`
- Delete: `docs/superpowers/repository-simplification-baseline.md`
- Modify if needed: final knowledge-surface guard to require exactly the four permanent prose files
- PR metadata/body only for migration evidence; do not create a replacement permanent cleanup report

**Interfaces:**
- Consumes: Task 9 evidence.
- Produces: final tree satisfying #280–#287 with history retained in commits/Issues/PR rather than active docs.

- [ ] **Step 1: Copy required migration/metrics evidence into the draft PR body**

The PR body must include concise sections:

```text
Summary
Issues closed: #280–#287
Behavior-preservation contract
Documentation distillation map
Test/E2E rename map
E2E helper consolidation
Go structural simplification
Dead-code removals with proof
Before/after context metrics
Validation results
Unexecuted/real-host validation gaps
```

Do not include private host/network values.

- [ ] **Step 2: Delete temporary design, plan and baseline files**

Remove all three temporary workflow artifacts. If their parent `docs/superpowers` directories become empty, remove the empty structure from the final tree.

- [ ] **Step 3: Run permanent knowledge-surface guard**

It must now pass with only:

```text
README.md
AGENTS.md
ARCHITECTURE.md
docs/cli.md
```

as permanent prose knowledge surfaces according to the guard's defined scope.

- [ ] **Step 4: Run final full validation again on the exact PR head**

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
```

Also rerun all shell/workflow/package/context guard commands from Task 9.

Expected: PASS on exact final head.

- [ ] **Step 5: Commit artifact retirement**

```bash
git add -A
git commit -m "chore: finalize repository simplification"
```

- [ ] **Step 6: Open one draft PR from `agent/repository-simplification` to `master`**

Title should describe repository simplification, not a product feature. The body must use closing references for #280–#287 only when their acceptance criteria are actually met on the final head.

- [ ] **Step 7: Verify final PR diff and CI before claiming completion**

Check changed-file list, PR diff, CI status, remaining `issueNNN` permanent filenames, remaining obsolete doc references, and final prose inventory. Do not mark ready/complete if any child Issue criterion is unmet.

---

## Plan self-review result

- Spec coverage: all documentation, historical-artifact, naming, E2E duplication, repository code/test simplification, daemon composition, safety-preservation, validation, metrics, guardrail, one-PR, and temporary-artifact-removal requirements are mapped to Tasks 1–10.
- No placeholder/TODO steps remain; exact deletion/rename candidates intentionally come from the baseline inventory because the spec requires evidence-based classification rather than guessed deletion.
- Package/public/runtime behavior is preserved by explicit non-goals and focused/full validation gates.
- Task dependencies are ordered so destructive deletion happens only after contract distillation and baseline mapping, and the temporary plan/spec remain available until final validation evidence is captured.