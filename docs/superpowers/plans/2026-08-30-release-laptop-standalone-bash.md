# Standalone Bash Release-Laptop Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Python-backed release-laptop harness with one downloadable pure-Bash executable while preserving durable resume/abort safety and release qualification behavior.

**Architecture:** `scripts/acceptance/release-laptop.sh` becomes the entire runtime. JSON checkpoint/report handling uses `jq`; all host mutation logic is implemented as prefixed Bash functions with write-ahead mutation authority and exact fail-closed cleanup. Repository-only shell tests exercise the script through a deterministic fake host command layer.

**Tech Stack:** Bash, jq, standard Linux CLI tools, Go test bridge, shellcheck, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-08-30-release-laptop-standalone-bash-design.md`

## Global Constraints

- One downloadable runtime file: `scripts/acceptance/release-laptop.sh`.
- No Python invocation, runtime `.py` modules, virtualenv, or source checkout required by the operator.
- No `apt`/`apt-get`, dependency repair, Podlaz source build, or previous-package download.
- No broad route/rule/nftables flush.
- No automatic reboot.
- Exact write-ahead-owned cleanup only; ambiguity fails closed and retains checkpoint.
- Real user IPs/domains/profile IDs/SSIDs must not be committed or written to sanitized reports.

---

### Task 1: Lock the one-file distribution contract with RED tests

**Files:**
- Modify: `scripts/acceptance/acceptance_test.go`
- Create: `scripts/acceptance/tests/standalone_contract.sh`

**Interfaces:**
- Consumes: repository tree.
- Produces: CI assertions that the runtime is one Bash file and has no Python dependency.

- [ ] Add tests asserting `release-laptop.sh` has executable mode, passes `bash -n`, contains no `python`, `release_acceptance`, or runtime source-file imports, and that no `.py` runtime files remain after migration.
- [ ] Run the acceptance test through `go test ./scripts/acceptance` and confirm RED against the current Python-backed implementation.
- [ ] Commit the RED test separately.

### Task 2: Implement standalone state, CLI, package identity, and reporting core

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Test: `scripts/acceptance/tests/standalone_contract.sh`
- Create: `scripts/acceptance/tests/standalone_recovery.sh`

**Interfaces:**
- Produces shell functions `ra_cli_parse`, `ra_state_load`, `ra_state_replace`, `ra_mut_begin_acquire`, `ra_mut_mark_acquired`, `ra_mut_begin_release`, `ra_mut_mark_released`, `ra_pkg_inspect`, `ra_pkg_install_exact`, `ra_report_write`.

- [ ] Add fake-host tests for new/resume/abort parsing, atomic checkpoint creation, package full-identity persistence, and candidate/previous mismatch refusal.
- [ ] Confirm RED.
- [ ] Implement the minimal standalone core using Bash + jq and same-directory atomic checkpoint writes.
- [ ] Confirm GREEN.
- [ ] Commit.

### Task 3: Port exact host mutation recovery

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Test: `scripts/acceptance/tests/standalone_recovery.sh`

**Interfaces:**
- Produces `ra_cleanup_owned_mutations`, `ra_nm_reconcile`, `ra_fixture_observe_*`, `ra_fixture_release_partial`, `ra_terminal_profile_reconcile`.

- [ ] Add RED cases for acquiring/releasing fixture cleanup, foreign route/link/nft refusal, NetworkManager reconnection, systemd drop-in cleanup, and synthetic terminal-profile crash windows.
- [ ] Implement exact observation before mutation and fail-closed cleanup.
- [ ] Confirm GREEN and commit.

### Task 4: Port lifecycle, privacy, soak, and reboot state machine

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Create: `scripts/acceptance/tests/standalone_flow.sh`

**Interfaces:**
- Produces `ra_run_new`, `ra_run_resume`, `ra_run_abort`, `ra_privacy_require_protected`, `ra_lifecycle_*`, `ra_soak_run`, `ra_reboot_*`, `ra_qualification_evaluate`.

- [ ] Add deterministic fake-host RED flow covering lower-release setup, candidate upgrade, lifecycle scenarios, fixture/coexistence, shortened test-mode soak, three persisted reboot boundaries, terminal phase, final restoration, and qualification result.
- [ ] Implement the state machine in Bash with checkpoint persistence before every reboot/mutation boundary.
- [ ] Confirm GREEN and commit.

### Task 5: Remove Python runtime and replace Python regressions

**Files:**
- Delete: `scripts/acceptance/release_acceptance/*.py`
- Delete: `scripts/acceptance/tests/test_*.py`
- Modify: `scripts/acceptance/acceptance_test.go`

**Interfaces:**
- Consumes standalone shell regressions from Tasks 1-4.
- Produces repository with no Python runtime dependency under `scripts/acceptance/`.

- [ ] Switch the Go bridge from Python unittest discovery to the shell regression entrypoints.
- [ ] Delete all obsolete Python runtime and Python tests.
- [ ] Run `go test ./scripts/acceptance` and shellcheck; confirm GREEN.
- [ ] Commit.

### Task 6: Update operator documentation and PR validation evidence

**Files:**
- Modify: `docs/release.md`
- Modify: `docs/superpowers/specs/2026-08-26-release-laptop-acceptance-design.md`
- Modify: `docs/superpowers/plans/2026-08-26-release-laptop-acceptance.md`

**Interfaces:**
- Produces a documented single-file download/run workflow.

- [ ] Replace checkout-oriented/operator wording with single-file download instructions and explicit `jq`/host-tool preflight.
- [ ] State that Python is not required.
- [ ] Run full repository CI on the exact final head and inspect all job conclusions.
- [ ] Update PR #272 body with exact final SHA, standalone distribution contract, and CI run evidence; do not claim real-laptop `QUALIFIED_PASS`.
