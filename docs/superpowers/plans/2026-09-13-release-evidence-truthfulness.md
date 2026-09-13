# Release Evidence Truthfulness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `scripts/acceptance/release-laptop.sh` report failed scenarios truthfully, treat semantic diagnostic exits as captured evidence, derive bundle completeness from required/applicable evidence, and preserve the bounded replay diagnostic privately.

**Architecture:** Keep the standalone Bash controller and existing immutable failure-bundle boundary. Extend the failure-component metadata model minimally so capture observation and applicability are distinct, preserve current fail-closed cleanup behavior, and add the replay diagnostic as private evidence only. Public reporting derives scenario outcome from controller state when `.outcome` is absent but terminal `state=failed` is already authoritative.

**Tech Stack:** Bash, jq, existing standalone acceptance tests.

**Spec:** GitHub issue #315 plus the follow-up confirmed replay-diagnostic omission comment.

## Global Constraints

- Do not change product lifecycle/recovery semantics in this workstream.
- Do not add retries or destructive lifecycle/network actions to improve reporting.
- Preserve private/public evidence separation; no replay diagnostic content may enter public artifacts.
- Preserve restart-friendly checkpoint semantics; prepared/running/verifying states must not be blindly rendered as terminal failure.
- Keep `release-laptop.sh` standalone and source-checkout independent.
- Remove this temporary plan before the final repository-structure gate.

---

### Task 1: Reproduce the four confirmed reporting/evidence defects

**Files:**
- Modify: `scripts/acceptance/tests/standalone_release_evidence.sh`

**Interfaces:**
- Consumes: existing `ra_failure_bundle_capture`, `ra_report_write`, `ra_artifacts_init_new` seams.
- Produces: failing behavioral assertions for scenario outcome, semantic `doctor` exit handling, applicability-aware completeness, and replay-diagnostic capture/privacy.

- [ ] **Step 1: Add a failed-scenario reporting regression**

Create a checkpoint fixture where `lower_release_upgrade.state="failed"` and `.outcome` is absent. Generate the public report and assert:

```bash
assert_eq "$(jq -r '.scenarios.lower_release_upgrade.outcome' "$RA_PUBLIC_DIR/report.json")" FAIL
```

Add a control scenario with no admitted state/outcome and assert it remains `NOT_EXERCISED`; keep an existing typed skip as a separate control.

- [ ] **Step 2: Run the focused standalone evidence test and verify RED**

Run the repository acceptance test entry that executes `standalone_release_evidence.sh` (or the script directly in CI-compatible test mode).

Expected: FAIL because the report currently uses `.outcome // "NOT_EXERCISED"` even when `state=failed`.

- [ ] **Step 3: Add the semantic doctor-exit regression**

Extend the fake `podlaz` command so `doctor --tun --json` can return exit `3` with valid v1 JSON:

```json
{"schema_version":"v1","status":"unhealthy","primary_classification":"network_apply_failure"}
```

Capture a failure bundle and assert the doctor component is a successful observation rather than `command_failed`, while preserving the semantic diagnostic status.

- [ ] **Step 4: Add applicability/completeness regression**

For the lower-release-upgrade fixture, keep `boot-autostart-attempt.json` absent and assert it is represented as verified absence/not-applicable and does not make the bundle partial. Add a negative control where a required component command actually fails and assert `capture_status=partial`.

- [ ] **Step 5: Add private replay-diagnostic capture regression**

Point a test-only replay-diagnostic path at a regular `0600` JSON fixture containing only bounded example values. Assert the immutable private bundle contains its copy and metadata marks it captured. Assert recursive grep over `$RA_PUBLIC_DIR` finds none of a private marker contained only in that fixture.

- [ ] **Step 6: Run the focused test again and verify all new assertions fail for the intended missing behavior**

Expected failures must correspond to current `NOT_EXERCISED`, `doctor=command_failed`, absence degrading completeness, and missing replay-diagnostic capture.

---

### Task 2: Add truthful failure-component observation/applicability semantics

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Test: `scripts/acceptance/tests/standalone_release_evidence.sh`

**Interfaces:**
- Consumes: current component map built by `ra_failure_component_set`.
- Produces: component entries carrying capture observation plus requirement/applicability, while preserving existing `status` compatibility where practical.

- [ ] **Step 1: Extend the component helper minimally**

Change the helper contract from a flat status-only entry to a bounded entry that can distinguish observation and applicability, for example:

```bash
ra_failure_component_set "$components" "$name" "$observation" "$applicability"
```

with values limited to:

```text
observation: captured | verified_absent | command_failed | unavailable
applicability: required | optional | not_applicable
```

If the existing `status` field is retained for compatibility, derive it deterministically from `observation`; do not maintain two independent truth sources.

- [ ] **Step 2: Make private-file absence observable rather than an inspection failure**

Update the private-file copy helper so a proven missing regular-file surface returns `verified_absent`; symlink/unexpected type/read/copy failures remain genuine failure states and never become absence.

- [ ] **Step 3: Encode scenario applicability for boot-attempt and replay-diagnostic evidence**

For a lower-release-upgrade failure, boot attempt is `not_applicable`; the replay diagnostic is `required` when the current scenario/state represents connect replay/package-upgrade failure evidence. Keep requirements conservative for states where applicability cannot be established.

- [ ] **Step 4: Derive `capture_status` from required evidence only**

Replace `all(.value.status=="captured")` with a predicate equivalent to:

```text
all required components are captured or validly verified absent for that predicate
```

Optional/not-applicable absence must not degrade completeness; command/validation/inspection failure of required evidence must.

- [ ] **Step 5: Run the focused evidence test and verify the applicability/completeness matrix turns GREEN**

---

### Task 3: Capture semantic doctor results and the bounded replay diagnostic

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Test: `scripts/acceptance/tests/standalone_release_evidence.sh`

**Interfaces:**
- Consumes: canonical `podlaz doctor --tun --json` v1 semantic exit behavior and daemon-owned replay diagnostic.
- Produces: immutable private evidence without changing product state.

- [ ] **Step 1: Capture doctor output independently from semantic exit status**

Run `podlaz doctor --tun --json`, always persist its stdout/stderr capture plus exit code, and for documented semantic exits (`0` and `3`) validate the returned v1 JSON. Treat a valid `rc=3` unhealthy/unavailable payload as captured semantic evidence. Transport failure, timeout, invalid JSON, or incompatible schema remains incomplete/failed evidence.

- [ ] **Step 2: Add a dedicated replay-diagnostic path constant**

Add a private constant for the daemon replay diagnostic next to the existing runtime authority paths. Do not expose its contents publicly.

- [ ] **Step 3: Validate and copy the replay diagnostic before cleanup**

Require a regular non-symlink bounded file and validate the expected bounded schema/owner plus the typed top-level replay fields used by #314. Copy it into the immutable private failure directory. Proven absence and inspection failure remain distinct.

- [ ] **Step 4: Run the focused evidence test and verify doctor and replay capture regressions turn GREEN**

---

### Task 4: Make public scenario rendering follow controller truth

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Test: `scripts/acceptance/tests/standalone_release_evidence.sh`
- Test: `scripts/acceptance/tests/standalone_safety.sh`

**Interfaces:**
- Consumes: scenario `{state,outcome,reason}` controller fields.
- Produces: one bounded derived public outcome used consistently by text summary and `report.json`.

- [ ] **Step 1: Add a private helper for derived scenario outcome**

Semantics:

```text
explicit outcome present -> use it
state=failed and no outcome -> FAIL
never admitted/no state -> NOT_EXERCISED
prepared/running/verifying without established outcome -> non-terminal/in-progress representation already supported by controller/report contract; never synthesize PASS/FAIL
```

Do not map every non-passed state to `FAIL`.

- [ ] **Step 2: Use the helper in both text and JSON report generation**

Remove duplicated `.outcome // "NOT_EXERCISED"` logic where it can contradict controller state.

- [ ] **Step 3: Assert cleanup failure remains a separate top-level qualification**

The same fixture must show scenario `FAIL` while top-level qualification remains `FAIL_CLEANUP_FAILED` when exact cleanup refuses/fails.

- [ ] **Step 4: Run focused acceptance tests and verify GREEN**

Run at least:

```bash
bash scripts/acceptance/tests/standalone_release_evidence.sh
bash scripts/acceptance/tests/standalone_safety.sh
bash scripts/acceptance/tests/run.sh
```

---

### Task 5: Final verification and temporary-plan removal

**Files:**
- Delete: `docs/superpowers/plans/2026-09-13-release-evidence-truthfulness.md`

**Interfaces:**
- Produces: final branch with no temporary prose and one coherent #315 harness fix.

- [ ] **Step 1: Review diff for product-semantic leakage**

Confirm no daemon/network/recovery source changed and no new mutation/retry was introduced.

- [ ] **Step 2: Run shell syntax and acceptance contract tests**

Run the canonical acceptance test suite plus shell syntax checks for modified scripts.

- [ ] **Step 3: Run repository-level verification appropriate to the touched surface**

Run the repository shell/workflow checks and `bash scripts/ci/repository-structure.sh --final`. Run broader Go verification if the canonical CI entry for this branch includes it.

- [ ] **Step 4: Remove this temporary plan and rerun the final repository-structure gate**

- [ ] **Step 5: Open one PR targeting `master`**

PR body must reference #315, state that product lifecycle/recovery semantics are intentionally unchanged, and list exact validation executed/not executed.
