# Release Laptop Modularization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 192 KB single-file release acceptance controller with a thin `release-laptop.sh` entrypoint plus focused Bash modules while preserving lifecycle, ownership, cleanup, recovery, checkpoint, and evidence semantics.

**Architecture:** Keep `scripts/acceptance/release-laptop.sh` as the only command the maintainer invokes. It resolves its own directory, validates and sources a fixed ordered module list from `scripts/acceptance/lib/release_laptop/`, then calls the existing `ra_main`. Module extraction is a textual refactor first: function bodies and ordering are preserved, with only loader/help/module-contract changes. #315 behavioral evidence fixes remain outside this PR.

**Tech Stack:** Bash, jq, existing acceptance shell contracts, GitHub Actions CI.

**Spec:** User-approved modularization of `scripts/acceptance/release-laptop.sh`; repository policy is `AGENTS.md` and networking/recovery invariants are `ARCHITECTURE.md`.

## Global Constraints

- Preserve privileged network mutation and cleanup authority exactly; this PR must not change product or recovery semantics.
- Keep `scripts/acceptance/release-laptop.sh` as the single invoked command.
- Source only fixed adjacent regular non-symlink module files in a fixed order; fail before controller execution when a module is missing or has the wrong type.
- Do not add Python runtime dependencies or duplicate a generated 192 KB standalone artifact.
- Keep existing checkpoint/state schemas and CLI flags unchanged.
- Remove this temporary plan before `repository-structure.sh --final`.

---

### Task 1: Lock the modular loader contract

**Files:**
- Modify: `scripts/acceptance/tests/standalone_contract.sh`
- Create: `scripts/acceptance/tests/modular_source_contract.sh`

- [ ] Add a contract asserting every module is a regular non-symlink Bash file, `bash -n` clean, and loaded in the declared order.
- [ ] Add a negative control proving the entrypoint fails before `ra_main` when one module is missing.
- [ ] Run the new contract against the monolith and verify RED for the missing modular loader.

### Task 2: Extract the controller without semantic edits

**Files:**
- Modify: `scripts/acceptance/release-laptop.sh`
- Create: `scripts/acceptance/lib/release_laptop/core.sh`
- Create: `scripts/acceptance/lib/release_laptop/product.sh`
- Create: `scripts/acceptance/lib/release_laptop/ownership.sh`
- Create: `scripts/acceptance/lib/release_laptop/evidence.sh`
- Create: `scripts/acceptance/lib/release_laptop/scenarios.sh`
- Create: `scripts/acceptance/lib/release_laptop/legacy.sh`

- [ ] Extract existing content by semantic section markers, preserving each function body byte-for-byte except loader/help wording needed by the new layout.
- [ ] Keep globals/state/artifact/package helpers in `core.sh`.
- [ ] Keep Podlaz/status/privacy/boot observation in `product.sh`.
- [ ] Keep exact harness-owned mutation/reconciliation helpers in `ownership.sh`.
- [ ] Keep failure bundle/public report/finalization logic in `evidence.sh` so #315 gets a focused edit surface.
- [ ] Keep disruptive scenarios/controller in `scenarios.sh`.
- [ ] Keep supported legacy-checkpoint reconciliation and outer dispatch in `legacy.sh`.
- [ ] Keep only loader validation, module sourcing, `ra_main`, and direct-execution dispatch in `release-laptop.sh`.

### Task 3: Verify behavior preservation

- [ ] Run `bash scripts/acceptance/tests/run.sh`; require all existing contracts plus the modular-source contract to pass.
- [ ] Run shell/workflow lint and repository structure active gate.
- [ ] Run full CI and inspect failures for ordering/source-path regressions rather than changing semantics to satisfy tests.

### Task 4: Final cleanup and review

- [ ] Delete this temporary plan.
- [ ] Verify no temporary generator/export files remain and no duplicate monolithic artifact exists.
- [ ] Verify no private host/profile/network data entered the diff.
- [ ] Run `bash scripts/ci/repository-structure.sh --final`.
- [ ] Update the PR with exact validation evidence and state explicitly that #315 behavior is deferred.
