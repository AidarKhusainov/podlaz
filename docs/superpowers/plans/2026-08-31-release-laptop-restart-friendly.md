# Restart-Friendly Release Laptop Acceptance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the standalone Bash release-laptop acceptance harness so semantic status changes, transient states, interruptions, and post-checkpoint failures are handled deterministically, restart-safely, and fail-closed.

**Architecture:** Keep `scripts/acceptance/release-laptop.sh` as the only runtime dependency, but turn its lifecycle into an explicit persisted controller. Decisions are based on semantic JSON classifiers plus exact persisted mutation ownership; one guarded failure finalizer captures private evidence before cleanup and shares the exact reconciliation engine with `--abort`, `--restart`, and clean retirement of old runs.

**Tech Stack:** Bash 5+, jq, standard Linux tooling already required by the harness, deterministic fake-host shell regressions, Go repository tests, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-08-31-release-laptop-restart-friendly-design.md`

## Global Constraints

- Machine decisions use semantic state, never presentation strings such as the human-readable `tun` field.
- Unknown/incompatible status schema and known terminal/impossible states fail immediately instead of consuming the scenario timeout.
- Every persistent mutation is write-ahead recorded and mutation retries are preceded by exact live observation plus persisted ownership reconciliation.
- Expected non-zero results from probes are captured and classified explicitly; `set -e` must not accidentally terminate the controller.
- Unexpected failures after checkpoint creation collect private diagnostics before cleanup.
- Automatic cleanup disconnects an owned active acceptance session before package restoration.
- Ambiguous ownership stops cleanup, retains the checkpoint, and returns `FAIL_CLEANUP_FAILED`; no broad route/rule/nft/systemd repair is allowed.
- Real reboot wait phases are intentional pauses and are never auto-aborted by an ordinary rerun.
- Public evidence remains structurally sanitized; private evidence may retain host-local diagnostic values.
- No Python runtime/source checkout dependency is reintroduced.
- PR #272 remains draft and is not merged by this plan.

---

### Task 1: Semantic status classifier and fast waits

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Create: `scripts/acceptance/tests/standalone_restart_friendly.sh`
- Modify: `scripts/acceptance/tests/run.sh`

**Interfaces:**
- Produces: `ra_status_classify_active`, `ra_status_classify_inactive`, and the three-way wait result `TARGET_REACHED | PROGRESS_POSSIBLE | TERMINAL_IMPOSSIBLE`.
- Consumes: `ra_status_json` and stable Podlaz JSON fields (`connection`, `mode`, `tun_health.state`, transaction/cleanup evidence).

- [ ] **Step 1: Write failing semantic-status regressions**

Add tests that source the harness and feed deterministic status payload sequences. Assert that `connection=active`, `mode=tun`, `tun="enabled (podlaz0)"`, verified health, and committed/no-cleanup transaction satisfies active immediately; `revalidating -> verified` progresses; and terminal/impossible evidence exits before the full timeout.

- [ ] **Step 2: Run the focused regression and verify RED**

Run: `bash scripts/acceptance/tests/standalone_restart_friendly.sh`

Expected: FAIL because current `ra_wait_active` compares `.tun=="active"` and waits through terminal states.

- [ ] **Step 3: Implement the minimal semantic classifier**

Replace presentation matching with semantic classification. Treat already-satisfied state as immediate success, `revalidating` as progress, incompatible schema as immediate internal failure, and authoritative terminal/cleanup contradiction as immediate impossible failure.

- [ ] **Step 4: Run focused and existing standalone tests**

Run: `bash scripts/acceptance/tests/standalone_restart_friendly.sh && bash scripts/acceptance/tests/run.sh`

Expected: PASS.

- [ ] **Step 5: Commit**

Commit message: `fix: classify release status semantically`

### Task 2: Consolidated preflight before checkpoint creation

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Test: `scripts/acceptance/tests/standalone_restart_friendly.sh`

**Interfaces:**
- Produces: a read-only `ra_preflight_new` result containing validated candidate/previous package metadata, installed version, selected profile, baseline capability observations, and lower-release eligibility.
- Constraint: no checkpoint or disruptive mutation exists on permanent preflight failure.

- [ ] **Step 1: Add failing preflight regression**

Fake `candidate.version == installed.version`, omit `--previous-deb`, and assert a new run fails before `ra_state_init`, before mutation ledger creation, and without a checkpoint.

- [ ] **Step 2: Verify RED**

Run the focused regression and confirm the old flow reaches artifact/checkpoint preparation before rejecting the missing lower release.

- [ ] **Step 3: Implement the minimal consolidated gate**

Validate candidate, previous release ordering, architecture/checksum/path, profile, required tools/capabilities, clean boundary, and fixture/drop-in freedom before `ra_state_init`. Only initialize artifacts/checkpoint after all permanent preflight conditions pass.

- [ ] **Step 4: Verify GREEN**

Run focused regressions and the full standalone suite.

- [ ] **Step 5: Commit**

Commit message: `fix: fail release preflight before checkpoint`

### Task 3: Persisted scenario boundaries and replay-safe mutations

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Test: `scripts/acceptance/tests/standalone_restart_friendly.sh`
- Test: existing `standalone_recovery.sh` and `standalone_safety.sh`

**Interfaces:**
- Produces: scenario phase helpers for `pending`, `prepared`, `running`, `verifying`, `passed`, `failed` and read-only replay classification.
- Consumes: existing `acquiring/acquired/releasing/released` mutation ledger.

- [ ] **Step 1: Add failing replay regressions**

Cover interrupted replay-safe phase, already-acquired package/profile/route/rule/nft/drop-in state, and verify a rerun does not issue a duplicate mutation.

- [ ] **Step 2: Verify RED**

Run focused tests; current coarse `running-pre-reboot` behavior should fail replay expectations.

- [ ] **Step 3: Implement explicit scenario boundaries**

Persist the boundary before each disruptive scenario mutation; on `running` or `verifying`, reconcile exact live/ledger state before deciding to continue, cleanup/restart, or fail closed.

- [ ] **Step 4: Verify existing recovery contracts remain green**

Run: `bash scripts/acceptance/tests/run.sh`

- [ ] **Step 5: Commit**

Commit message: `feat: make release scenarios replay safe`

### Task 4: Guarded diagnostics-first failure finalizer

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Test: `scripts/acceptance/tests/standalone_restart_friendly.sh`
- Test: `scripts/acceptance/tests/standalone_abort_recovery.sh`

**Interfaces:**
- Produces: one guarded failure finalizer and one shared exact reconciliation/cleanup engine.
- Required private bundle: checkpoint snapshot, `commands.log`, last status JSON, doctor output, systemd status, journal scoped from run start/current boot, package state, mutation ledger, continuation/transactions/boot-attempt, scenario network observations.
- Outcomes: `FAILED_CLEAN` or `FAIL_CLEANUP_FAILED`.

- [ ] **Step 1: Add failing ordering/finalizer regressions**

Assert diagnostics are captured before cleanup mutation, active owned session is disconnected before package restoration, exact cleanup yields `FAILED_CLEAN` and removes checkpoint, ambiguous cleanup yields `FAIL_CLEANUP_FAILED` and retains checkpoint, and a cleanup failure does not recursively invoke a second finalizer.

- [ ] **Step 2: Verify RED**

Run focused regressions; current abort ordering and absence of automatic finalization should fail.

- [ ] **Step 3: Implement diagnostics capture and finalizer guard**

Persist `last_failure`, snapshot authority-bearing state, collect best-effort diagnostics, set a durable/in-memory cleanup guard, then invoke the shared reconciliation engine. Never recurse through ERR handling while finalizing.

- [ ] **Step 4: Implement exact cleanup order**

Disconnect active owned acceptance session first; then release exact NM/fixtures/drop-ins/autostart/synthetic profile; restore candidate only if package authority requires it; prove inactive and ordinary DNS/TCP/HTTPS/direct connectivity; remove checkpoint only after proof.

- [ ] **Step 5: Verify GREEN**

Run focused tests plus `standalone_abort_recovery.sh` and full standalone suite.

- [ ] **Step 6: Commit**

Commit message: `feat: add guarded release failure cleanup`

### Task 5: Restart-friendly invocation and signal handling

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Modify: `scripts/acceptance/tests/standalone_contract.sh`
- Test: `scripts/acceptance/tests/standalone_restart_friendly.sh`

**Interfaces:**
- Produces: `--restart` mode, ordinary-new-run checkpoint classification, and SIGINT/SIGTERM integration with the guarded finalizer.

- [ ] **Step 1: Add failing CLI/restart regressions**

Assert `--restart` is documented, ordinary rerun auto-resumes replay-safe state, exactly-cleanable failed run is retired then starts fresh, reboot wait requires explicit `--resume`/`--restart`, and ambiguous ownership fails closed.

- [ ] **Step 2: Add signal/finalizer regression**

Inject SIGTERM after checkpoint creation and assert diagnostics then safe cleanup; verify reboot pause does not trigger the signal/failure finalizer path.

- [ ] **Step 3: Verify RED**

Run focused contract/restart tests.

- [ ] **Step 4: Implement restart classification**

Add `--restart`; share reconciliation with abort/failure cleanup; on normal new invocation classify existing checkpoint as replay-safe, exactly retirable, reboot-wait, or ambiguous before mutation.

- [ ] **Step 5: Install guarded traps only after checkpoint creation**

SIGINT/SIGTERM invoke evidence-first safe cleanup when possible; expected reboot pauses and successful exits disarm automatic failure cleanup.

- [ ] **Step 6: Verify GREEN and commit**

Run the full standalone suite. Commit message: `feat: add restart-friendly release lifecycle`

### Task 6: Bash probe hardening and privacy-preserving evidence

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Test: `scripts/acceptance/tests/standalone_restart_friendly.sh`
- Test: `scripts/acceptance/tests/standalone_safety.sh`

**Interfaces:**
- Produces: explicit classification helpers for expected non-zero probe commands and sanitized public failure report generation.

- [ ] **Step 1: Add expected-nonzero regressions**

Exercise representative `jq`, `grep`, `curl`, `ip`, `nft`, `systemctl`, and `resolvectl` non-zero states that mean absent/not-yet/blocked and assert the shell remains alive for classification.

- [ ] **Step 2: Verify RED where current `set -e` paths are brittle**

Run focused test.

- [ ] **Step 3: Harden probes minimally**

Route expected non-zero commands through capture/classification helpers; keep truly unexpected command errors fatal. Do not add blanket `|| true` around authority-bearing observations.

- [ ] **Step 4: Assert public report redaction**

Seed fake host-private IP/domain/profile/SSID/credential values in private evidence and assert none appear in public output.

- [ ] **Step 5: Verify GREEN and commit**

Run full standalone suite. Commit message: `fix: classify release host probes safely`

### Task 7: Documentation and repository validation

**Files:**
- Modify: `docs/release.md`
- Modify: PR #272 body
- Test: repository/CI only

**Interfaces:**
- Documents the actual standalone Bash implementation and exact operator restart/failure behavior; does not claim real-laptop qualification was executed by CI.

- [ ] **Step 1: Update release documentation**

Document semantic status matching, automatic diagnostics/cleanup outcomes, normal rerun behavior, `--restart`, reboot-wait semantics, private/public evidence boundaries, and fail-closed ownership behavior.

- [ ] **Step 2: Run standalone recovery/safety tests and shell lint**

Run: `bash scripts/acceptance/tests/run.sh`, `bash -n scripts/acceptance/release-laptop.sh`, and repository shellcheck/lint command from CI.

- [ ] **Step 3: Run Go/CLI contract and Debian package validation**

Run the repository CI-equivalent Go format/tests/vet/CLI contract plus Debian package metadata/content/install/reinstall/purge validation.

- [ ] **Step 4: Verify exact-head GitHub Actions**

Fetch workflow runs for the final PR head SHA and require all PR checks on that exact SHA to complete successfully.

- [ ] **Step 5: Update PR body**

Replace stale Python-orchestration wording with the actual standalone Bash controller, list the restart/failure semantics and validation evidence, keep the PR draft, and do not merge.

- [ ] **Step 6: Final self-review**

Compare the final diff against both release-laptop specs, `AGENTS.md`, `docs/cli.md`, `docs/state-and-security.md`, `docs/debian-package.md`, `docs/development.md`, `docs/release.md`, and `docs/e2e.md`; ensure no personal host values were committed.