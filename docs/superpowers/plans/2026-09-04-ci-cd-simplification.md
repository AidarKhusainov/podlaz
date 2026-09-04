# CI/CD Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce Podlaz CI/CD to three active, runnable workflows (`ci.yml`, `integration.yml`, `release.yml`), remove retired self-hosted infrastructure and meta-testing, preserve critical TUN safety specifications, and make hosted/release verification explicit and reproducible.

**Architecture:** Keep PR gating deterministic and secret-free, run installed-product checks on GitHub-hosted Ubuntu 24.04, isolate real-provider proxy checks behind the existing protected GitHub Environment, and make release validate the exact tag SHA and built package. Remove orchestration and harnesses that exist only for the absent self-hosted runner; retain only focused destructive-network scripts that encode safety-critical invariants.

**Tech Stack:** GitHub Actions, Bash, Go 1.26.6, Debian packaging, systemd/journald, Python 3 test helpers.

**Spec:** `docs/superpowers/specs/2026-09-04-ci-c- simplification-design.md`

## Global Constraints

- Preserve product behavior, package/service compatibility, lifecycle ordering, recovery semantics, network ownership, and privacy guarantees.
- `proxy-only` integration must not mutate host networking.
- Destructive TUN/route/rule/DNS/nftables/NetworkManager/suspend-recovery scenarios must not run on general GitHub-hosted runners.
- PR CI must remain deterministic and secret-free.
- Real-provider integration may run only from `master` or an exact release tag and through the existing protected Environment boundary.
- Active jobs use explicit `ubuntu-24.04`; remove `ubuntu-22.04` from active CI/release configuration.
- External Actions are pinned to immutable commit SHAs:
  - `actions/checkout`: `3d3c42e5aac5ba805825da76410c181273ba90b1` (`v7`)
  - `actions/setup-go`: `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` (`v7`)
  - `actions/upload-artifact`: `043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` (`v7`)
  - `actions/download-artifact`: `3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c` (`v8`)
  - `actions/attest-build-provenance`: `4d101475d8b20a2381f78447822ac1eab6504dd8` (`v4`)
- Use `persist-credentials: false` for checkout unless a later step genuinely needs Git credentials.
- Tests should assert behavior or durable repository invariants, not retired workflow wiring or incidental shell implementation shape.
- No real user IPs/domains/profiles/secrets in source, tests, PR text, or logs.
- Final repository state must remove this plan and its temporary design spec because `repository-structure.sh --final` forbids `docs/superpowers/**`.

---

### Task 1: Simplify and harden deterministic CI

**Files:**
- Modify: `.github/workflows/ci.yml`
- Delete: `scripts/ci/boot-continuation-contract.sh`
- Modify only if needed by the changed workflow contract: `scripts/ci/validate-package-workflow-contract.sh`
- Test: `scripts/ci/validate-package-workflow-contract-test.sh`
- Test: `scripts/ci/repository-structure-test.sh`

**Interfaces:**
- Consumes: existing `scripts/ci/go-core.sh`, `scripts/ci/cli-smoke.sh`, `scripts/e2e/cli-contract.sh`, package build/validation scripts.
- Produces: one deterministic PR/master merge-gate workflow with no private credentials and no self-hosted dependency.

- [ ] **Step 1: Characterize current CI behavior**

Run the existing contracts before edits:

```bash
bash scripts/ci/validate-package-workflow-contract-test.sh
bash scripts/ci/repository-structure-test.sh
go test ./scripts/e2e -count=1
```

Expected: PASS on current `master` behavior.

- [ ] **Step 2: Update `ci.yml` trigger and concurrency policy**

Make the trigger exactly:

```yaml
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review, converted_to_draft]
  push:
    branches:
      - master
```

Use one per-ref group and cancel superseded PR/master runs:

```yaml
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
```

- [ ] **Step 3: Pin Actions and move hosted jobs to Ubuntu 24.04**

Replace action tags with the SHAs in Global Constraints. For every checkout add:

```yaml
with:
  persist-credentials: false
```

Change the Debian package job from `ubuntu-22.04` to `ubuntu-24.04`.

- [ ] **Step 4: Remove redundant boot-continuation CI rerun**

Delete the workflow step:

```yaml
- name: Boot continuation contract
  run: bash scripts/ci/boot-continuation-contract.sh
```

Delete `scripts/ci/boot-continuation-contract.sh` after confirming its Go tests are already included by `go test ./...` and `validate-installed-status-test.sh` remains invoked by workflow lint.

- [ ] **Step 5: Add CLI contract to the deterministic test job**

After `CLI smoke contract`, run:

```yaml
- name: CLI behavior contract
  run: bash scripts/e2e/cli-contract.sh
```

Do not run `coverage-evidence.sh`.

- [ ] **Step 6: Verify Task 1**

Run:

```bash
bash scripts/ci/workflow-lint.sh
bash scripts/ci/validate-package-workflow-contract-test.sh
bash scripts/ci/repository-structure-test.sh
go test ./scripts/e2e -count=1
```

Expected: all PASS and no reference to `foundation` or `boot-continuation-contract.sh` remains in active CI.

- [ ] **Step 7: Commit Task 1**

```bash
git add .github/workflows/ci.yml scripts/ci
 git commit -m "ci: simplify deterministic merge gate"
```

---

### Task 2: Make ordinary-user installed-package acceptance deterministic

**Files:**
- Modify: `scripts/e2e/remote-client-acceptance.sh`
- Modify: `scripts/e2e/remote_client_acceptance_test.go`
- Keep behavior: `scripts/e2e/log-window-acceptance.sh`
- Modify: `scripts/e2e/log_window_contract_test.go`
- Reuse: `scripts/e2e/package-service.sh`

**Interfaces:**
- Consumes: an already installed native Podlaz package and active `podlazd.service`.
- Produces: provider-independent ordinary-user status/doctor/recover/log acceptance using a deterministic fake Xray process and RFC/example profile data.

- [ ] **Step 1: Rewrite the remote-client contract test first**

Keep the ordinary-user invariants and replace the external-profile/workflow-wiring expectations with deterministic-fixture expectations. The test must require fragments equivalent to:

```go
required := []string{
    "ordinary-user acceptance must not run as root",
    "ordinary_user_without_podlaz_group",
    "PODLAZ_XRAY_PATH",
    "remote-client.example.net",
    "connect --mode proxy-only",
    "recover --json",
    "logs \"--${mode}\" --name 36h",
}
```

Use the actual existing CLI syntax `--since 36h` in the final test; the key requirement is that no `PODLAZ_E2E_PROFILE_URI`/real-provider input is mandatory and no workflow file is inspected.

Remove `TestTunPackageConvergenceRunsRemoteClientAcceptance` entirely.

- [ ] **Step 2: Run the focused test and confirm it fails**

```bash
go test ./scripts/e2e -run 'TestRemoteClientAcceptance' -count=1
```

Expected: FAIL because the script still requires external profile input and contains self-hosted wording.

- [ ] **Step 3: Add a deterministic fake-Xray fixture inside `remote-client-acceptance.sh`**

Use a private temp directory under `E2E_TMP_ROOT` and a systemd drop-in under `/run/systemd/system/podlazd.service.d/`.

The fake Xray executable must accept only:

```text
run -config <path>
```

verify the config is readable, trap TERM/INT, and remain alive until stopped. Point the service at it using:

```ini
[Service]
Environment=PODLAZ_XRAY_PATH=<fixture-path>/xray
```

Use a fixed example URI such as:

```text
vless://00000000-0000-0000-0000-000000000077@remote-client.example.net:443?type=tcp&security=tls&encryption=none#remote-client
```

Do not require `PODLAZ_E2E_PROFILE_URI` or `PODLAZ_E2E_PROFILE_URI_LIST`.

- [ ] **Step 4: Make cleanup fail-closed for the fixture**

The EXIT trap must:

1. disconnect if connected;
2. remove only the exact remote-client systemd drop-in created by the scenario;
3. `systemctl daemon-reload`;
4. restart/start `podlazd.service` only if needed to restore the installed-package baseline;
5. preserve the original test exit code unless cleanup itself is the only failure.

Do not purge the package here; job-level integration cleanup owns package removal.

- [ ] **Step 5: Remove self-hosted wording and workflow-binding assertions from log-window tests**

Delete only `TestTunPackageConvergenceRunsStrictLogWindowAcceptance`; preserve the actual log-window acceptance behavior test.

- [ ] **Step 6: Verify Task 2**

```bash
go test ./scripts/e2e -run 'Test(RemoteClientAcceptance|StrictLogWindowAcceptance)' -count=1
bash -n scripts/e2e/remote-client-acceptance.sh
bash -n scripts/e2e/log-window-acceptance.sh
shellcheck -x -s bash -P scripts/e2e -e SC1091 scripts/e2e/remote-client-acceptance.sh scripts/e2e/log-window-acceptance.sh
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add scripts/e2e/remote-client-acceptance.sh scripts/e2e/remote_client_acceptance_test.go scripts/e2e/log_window_contract_test.go
 git commit -m "test: make installed client acceptance deterministic"
```

---

### Task 3: Add hosted installed-product integration

**Files:**
- Create: `.github/workflows/integration.yml`
- Reuse: `scripts/e2e/package-service.sh`
- Reuse: `scripts/e2e/log-window-acceptance.sh`
- Reuse: `scripts/e2e/remote-client-acceptance.sh`

**Interfaces:**
- Consumes: deterministic scripts from Tasks 1-2.
- Produces: a hosted `ubuntu-24.04` installed-product job on `master`, plus manual diagnosis without provider secrets by default.

- [ ] **Step 1: Add workflow triggers**

Use:

```yaml
name: Integration

on:
  push:
    branches:
      - master
  schedule:
    - cron: '17 3 * * *'
  workflow_dispatch:
    inputs:
      real_provider:
        description: Run the real-provider proxy data-plane check from master
        required: false
        default: false
        type: boolean
```

Set:

```yaml
permissions:
  contents: read

concurrency:
  group: integration-${{ github.ref }}-${{ github.event_name }}
  cancel-in-progress: ${{ github.event_name == 'push' }}
```

- [ ] **Step 2: Add deterministic installed-package job**

Condition:

```yaml
if: ${{ github.event_name == 'push' || github.event_name == 'workflow_dispatch' }}
```

Use `ubuntu-24.04`, pinned checkout/setup-go, and `persist-credentials: false`.

Run `package-service.sh` with:

```yaml
env:
  PODLAZ_E2E_KEEP_PACKAGE: 'true'
```

Then restore/start the service and run:

```bash
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
bash scripts/e2e/log-window-acceptance.sh
bash scripts/e2-incomplete
```

The final command in implementation must be exactly:

```bash
bash scripts/e2e/remote-client-acceptance.sh
```

Always purge package/service state afterward with exact package/systemd cleanup and upload bounded diagnostics.

- [ ] **Step 3: Add a temporary branch trigger only for pre-merge runtime proof**

During implementation validation, temporarily include:

```yaml
push:
  branches:
    - master
    - agent/ci-cd-simplification
```

After obtaining one successful deterministic integration run, remove the feature branch line before final PR review. This temporary trigger must not survive the final diff.

- [ ] **Step 4: Verify workflow structure locally**

```bash
actionlint .github/workflows/integration.yml
bash scripts/ci/workflow-lint.sh
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```bash
git add .github/workflows/integration.yml
 git commit -m "ci: add hosted installed-product integration"
```

---

### Task 4: Add trusted real-provider proxy integration and prebuilt-package support

**Files:**
- Modify: `.github/workflows/integration.yml`
- Modify: `scripts/e2e/data-plane.sh`
- Modify/add focused contract test in: `scripts/e2e/runtime_config_redaction_test.go` or a new domain-named `scripts/e2e/data_plane_package_contract_test.go`

**Interfaces:**
- Consumes: `PODLAZ_E2E_PROFILE_URI`/list from protected Environment, optional prebuilt native `.deb` path.
- Produces: scheduled/manual real proxy verification on trusted `master`, and a data-plane harness reusable by release without rebuilding a dev package.

- [ ] **Step 1: Add a failing package-selection contract test**

Test that `data-plane.sh` supports an explicit package environment variable, named:

```text
PODLAZ_E2E_PACKAGE_PATH
```

The contract must require two paths:

- when set, the script validates the file and native architecture and installs that package without invoking `scripts/build-deb.sh`;
- when unset, existing dev-package build behavior remains.

Run:

```bash
go test ./scripts/e2e -run 'TestDataPlanePackageSelection' -count=1
```

Expected: FAIL before implementation.

- [ ] **Step 2: Implement optional prebuilt package selection**

At startup add:

```bash
: "${PODLAZ_E2B_PACKAGE_PATH:=}"
```

Use the correct final variable name `PODLAZ_E2E_PACKAGE_PATH` everywhere; the misspelled form above is intentionally not allowed to remain.

Set an `INSTALL_DEB` variable. If a prebuilt path is configured:

```bash
[[ -f "${PODLAZ_E2E_PACKAGE_PATH}" ]] || fail "configured data-plane package is missing"
package_arch="$(dpkg-deb --field "${PODLAZ_E2E_PACKAGE_PATH}" Architecture)"
[[ "${package_arch}" == "${HOST_DEB_ARCH}" ]] || fail "configured data-plane package architecture mismatch"
INSTALL_DEB="${PODLAZ_E2E_PACKAGE_PATH}"
```

Otherwise retain the current nfpm/dev build and set `INSTALL_DEB="${DEV_DEB}"`.

Install `"${INSTALL_DEB}"`.

- [ ] **Step 3: Add real-provider job to `integration.yml`**

Run only when:

```yaml
if: >-
  ${{ github.event_name == 'schedule' ||
      (github.event_name == 'workflow_dispatch' &&
       github.ref == 'refs/heads/master' &&
       inputs.real_provider) }}
```

Use `ubuntu-24.04` and `environment: vpn-e2e` as the existing secret/approval boundary. Pass the existing profile/egress secrets and variables. Do not pass any secret to the deterministic job.

- [ ] **Step 4: Bound provider reliability cost**

For scheduled/manual integration, default `PODLAZ_E2E_RELIABILITY_CYCLES` to a small bounded value (for example existing configured value or `3`), not the 100-cycle release stress value.

- [ ] **Step 5: Verify Task 4**

```bash
go test ./scripts/e2e -run 'TestDataPlanePackageSelection' -count=1
bash -n scripts/e2e/data-plane.sh
bash scripts/ci/workflow-lint.sh
```

Expected: PASS; no `pull_request` path can reach provider secrets.

- [ ] **Step 6: Commit Task 4**

```bash
git add .github/workflows/integration.yml scripts/e2e/data-plane.sh scripts/e2e/*data*package*test.go
 git commit -m "ci: add trusted proxy data-plane integration"
```

---

### Task 5: Make release validate exact-tag installed runtime and real data plane

**Files:**
- Modify: `.github/workflows/release.yml`
- Reuse: `scripts/e2e/log-window-acceptance.sh`
- Reuse: `scripts/e2e/remote-client-acceptance.sh`
- Reuse: `scripts/e2e/data-plane.sh`

**Interfaces:**
- Consumes: exact tag SHA and release `.deb` artifacts produced by `build`.
- Produces: deterministic installed-runtime and real-provider gates on the exact release package before attestation/publication.

- [ ] **Step 1: Pin release Actions and move all hosted jobs to Ubuntu 24.04**

Apply the immutable SHAs from Global Constraints to checkout/setup-go/upload/download/attestation and add `persist-credentials: false` to checkout.

- [ ] **Step 2: Preserve build artifact as the authority for downstream release tests**

Keep the existing release package build/validation and uploaded artifact bundle. Do not rebuild a dev `.deb` in downstream release qualification jobs.

- [ ] **Step 3: Add deterministic release integration job**

After `build`, create a job that:

1. checks out the exact resolved tag;
2. downloads `podlaz-release-artifacts-${version}`;
3. installs the amd64 release `.deb`;
4. starts/verifies `podlazd.service`;
5. runs `log-window-acceptance.sh`;
6. runs deterministic `remote-client-acceptance.sh`;
7. always purges the package and exact test drop-ins.

- [ ] **Step 4: Add exact-tag real-provider release job**

After deterministic release integration, create a job using `environment: vpn-e2e` that downloads the same release artifact and runs:

```bash
PODLAZ_E2E_PACKAGE_PATH="dist/release/podlaz_${VERSION}_linux_amd64.deb" \
PODLAZ_E2E_RELIABILITY_CYCLES="${PODLAZ_E2E_RELEASE_RELIABILITY_CYCLES:-10}" \
  bash scripts/e2e/data-plane.sh
```

Set the configured release reliability value through workflow variables if available; otherwise use a bounded default that is materially stronger than daily integration but not the retired three-hour soak.

- [ ] **Step 5: Gate publication on both integration jobs**

`attest-and-publish` must depend on:

```yaml
needs:
  - resolve
  - build
  - release-integration
  - release-data-plane
```

Keep publication permissions only on the final job.

- [ ] **Step 6: Verify Task 5**

```bash
actionlint .github/workflows/release.yml
bash scripts/ci/validate-package-workflow-contract.sh
bash scripts/ci/workflow-lint.sh
```

Expected: PASS; every release action is pinned; no active release job uses Ubuntu 22.04; publication cannot run before exact-tag package integration and real-provider data-plane checks.

- [ ] **Step 7: Commit Task 5**

```bash
git add .github/workflows/release.yml
 git commit -m "ci: qualify exact release package before publish"
```

---

### Task 6: Remove retired self-hosted orchestration and workflow-binding tests

**Files:**
- Delete: `.github/workflows/e2e.yml`
- Delete: `.github/workflows/e2e-tun-package-convergence.yml`
- Delete: `.github/workflows/e2e-tun-resource-soak.yml`
- Delete: `.github/actionlint.yaml`
- Delete: `scripts/ops/bootstrap-e2e-runner.sh`
- Modify: `scripts/e2e/boot_continuation_acceptance_contract_test.go`
- Modify: `scripts/e2e/network_reconciliation_acceptance_contract_test.go`
- Modify: `scripts/e2e/privacy_envelope_lifecycle_acceptance_contract_test.go`
- Modify: `scripts/e2e/network_recovery_acceptance_contract_test.go`
- Modify: `scripts/e2e/log_window_contract_test.go`
- Modify: `scripts/e2e/remote_client_acceptance_test.go`

**Interfaces:**
- Consumes: new active workflow topology from Tasks 1-5.
- Produces: no active or test-only dependency on the absent `vpn-e2e` self-hosted runner.

- [ ] **Step 1: Capture the current reference list before deletion**

Run:

```bash
git grep -n -E 'e2e-tun-package-convergence\.yml|e2e-tun-resource-soak\.yml|runs-on: \[self-hosted|vpn-e2e|bootstrap-e2e-runner'
```

Save the output in the task notes/PR evidence, not as a permanent repository file.

- [ ] **Step 2: Delete retired workflow/runner files**

Delete the five files listed above.

- [ ] **Step 3: Remove workflow-wiring tests but preserve product invariants**

Delete tests whose sole assertion is that a retired workflow invokes an acceptance script.

For network recovery, replace the workflow-based v0.2.29 check with a script-contract check against `network-recovery-package-acceptance.sh` requiring:

```text
PODLAZ_E2E_BASE_VERSION:=v0.2.29
[[ "${PODLAZ_E2E_BASE_VERSION}" == "v0.2.29" ]]
```

Do not remove the historical compatibility boundary itself.

- [ ] **Step 4: Remove runner-specific comments where semantics are actually runner-agnostic**

Search:

```bash
git grep -n -E 'self-hosted|vpn-e2e' scripts/e2e scripts/ci
```

Rewrite comments that only describe old GitHub Actions infrastructure. Keep explicit dedicated-host requirements in destructive scripts such as network reconciliation where the environment is semantically required.

- [ ] **Step 5: Verify Task 6**

```bash
go test ./scripts/e2e -count=1
bash scripts/ci/workflow-lint.sh
```

Expected: PASS; no test tries to open a deleted workflow.

- [ ] **Step 6: Commit Task 6**

```bash
git add -A .github scripts/ops scripts/e2e
 git commit -m "ci: remove retired self-hosted orchestration"
```

---

### Task 7: Remove stale meta/legacy/soak infrastructure and audit `server-coverage.sh`

**Files:**
- Delete: `scripts/e2e/coverage-evidence.sh`
- Delete: `scripts/e2e/real-vpn.sh`
- Delete: `scripts/e2e/real-vpn-extended.sh`
- Audit then delete: `scripts/e2e/server-coverage.sh`
- Modify/delete: `scripts/e2e/scenario_infrastructure_contract_test.go`
- Modify: `scripts/e2e/shared_infrastructure_contract_test.go`
- Modify: `scripts/e2e/redaction_scan_test.go`
- Delete soak root files: `scripts/e2e/tun-resource-soak.sh`, `scripts/e2e/tun-resource-soak-policy.json`
- Delete soak libraries: `scripts/e2e/lib/tun_soak_analysis.py`, `scripts/e2e/lib/tun_soak_cleanup.sh`, `scripts/e2e/lib/tun_soak_environment.py`, `scripts/e2e/lib/tun_soak_health.sh`, `scripts/e2e/lib/tun_soak_isolation.py`, `scripts/e2e/lib/tun_soak_metrics.py`, `scripts/e2e/lib/tun_soak_process.py`, `scripts/e2e/lib/tun_soak_status.py`
- Delete soak tests under `scripts/e2e/tests/` whose names begin `test_tun_soak_` plus `test_tun_resource_soak_contract.py`
- Rename issue-oriented tests: `internal/logs/build_args_test.go`, `internal/app/cli/logs_matrix_test.go`

**Interfaces:**
- Consumes: focused deterministic integration plus retained destructive safety scripts.
- Produces: smaller E2E surface with no issue-coverage report generator, legacy real-VPN wrappers, dormant soak subsystem, or catch-all server test.

- [ ] **Step 1: Prove `coverage-evidence.sh` and legacy real-VPN scripts are superseded**

Run:

```bash
git grep -n 'coverage-evidence.sh'
git grep -n 'real-vpn.sh'
git grep -n 'real-vpn-extended.sh'
```

Expected before deletion: references are limited to retired orchestration/meta-tests or the files themselves.

- [ ] **Step 2: Build a temporary invariant map for `server-coverage.sh`**

Compare its behaviors against:

- `scripts/e2e/data-plane.sh` for real proxy lifecycle/egress/cleanup;
- `scripts/e2e/package-service.sh` for package/service/authorization;
- focused TUN acceptance scripts for recovery, coexistence, privacy envelope, protected gateway, failure/rollback, stale-link, boot continuation;
- Go tests for daemon concurrency/recovery/status semantics.

Record only in implementation notes/PR body which unique behaviors are intentionally dropped as non-critical legacy coverage (for example broad multi-profile/IPv6 observation) and which are superseded. Do not create a permanent coverage-map file.

- [ ] **Step 3: Delete `server-coverage.sh` if no safety-critical unique invariant remains**

If a safety-critical unique invariant is found, extract only that invariant into one focused domain-named scenario before deletion. Do not preserve the catch-all script.

- [ ] **Step 4: Delete implementation-shape meta-tests**

Remove checks in `scenario_infrastructure_contract_test.go` that assert exact helper names, literal timeouts, or source placement. Preserve genuinely executable helper behavior tests by moving them to existing focused test files only when they assert observable behavior.

Keep `shared_infrastructure_contract_test.go` tests that actually execute shell helpers; remove source-text-only assertions that merely freeze internal function names when no durable interface requires them.

- [ ] **Step 5: Delete the dormant soak subsystem**

Delete the root script/policy, all `tun_soak_*` libraries, and all dedicated soak tests. Then run:

```bash
git grep -n -E 'tun-resource-soak|tun_soak_' || true
```

Expected: no maintained source/workflow reference remains.

- [ ] **Step 6: Rename issue-oriented Go test identifiers**

Rename:

```go
TestBuildJournalctlArgsIssue160FlagMatrix
```

to a domain-oriented name such as:

```go
TestBuildJournalctlArgsFlagMatrix
```

and:

```go
TestRunCLILogsParsesAdditionalIssue160Flags
```

to:

```go
TestRunCLILogsParsesSupportedFlags
```

Do not change test behavior.

- [ ] **Step 7: Verify Task 7**

```bash
go test ./...
python3 -m unittest discover -s scripts/e2e/tests -p 'test_*.py'
bash scripts/ci/workflow-lint.sh
git grep -n -E 'Issue160|issue-160|coverage-evidence|real-vpn|server-coverage|tun-resource-soak|tun_soak_' || true
```

Expected: tests PASS and any remaining grep hits are intentional historical text outside maintained artifacts or none at all.

- [ ] **Step 8: Commit Task 7**

```bash
git add -A scripts/e2e internal
 git commit -m "test: remove stale E2E infrastructure"
```

---

### Task 8: Finalize workflow security, validate runtime, remove temporary docs, and open PR

**Files:**
- Modify as needed after runtime evidence: `.github/workflows/ci.yml`, `.github/workflows/integration.yml`, `.github/workflows/release.yml`
- Delete: `docs/superpowers/specs/2026-09-04-ci-cd-simplification-design.md`
- Delete: `docs/superpowers/plans/2026-09-04-ci-cd/` no; delete exact file `docs/superpowers/plans/2026-09-04-ci-cd-simplification.md`
- Update canonical prose only if a durable invariant changed: `ARCHITECTURE.md` or `AGENTS.md`

**Interfaces:**
- Consumes: all previous tasks.
- Produces: final PR-ready repository with exactly three active workflows and no temporary design/plan prose.

- [ ] **Step 1: Run complete local/repository verification available in the execution environment**

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
bash scripts/ci/repository-structure.sh
bash scripts/ci/workflow-lint.sh
bash scripts/ci/validate-package-workflow-contract-test.sh
```

Expected: PASS.

- [ ] **Step 2: Push the feature branch and open one PR to `master`**

PR body must include:

- workflow migration map;
- deleted self-hosted/legacy/meta/soak surfaces;
- retained non-automated destructive TUN safety harnesses;
- exact validation performed;
- explicit statement that destructive TUN acceptance was not run because no safe dedicated runner exists;
- no real endpoint/IP/profile data.

- [ ] **Step 3: Obtain fresh PR CI evidence**

Inspect all CI jobs for the PR head SHA. Fix any actual failures, rerun verification, and update the PR.

- [ ] **Step 4: Obtain one hosted deterministic integration run before merge**

If `integration.yml` is not dispatchable before it lands on the default branch, use the temporary feature-branch push trigger from Task 3. Require a green run of the deterministic installed-package job.

After the green run, remove `agent/ci-cd-simplification` from the workflow trigger and push the final change.

- [ ] **Step 5: Run real-provider integration only if protected credentials are available**

Run it only from trusted `master`/exact tag context. If the current GitHub Actions product prevents a pre-merge trusted run, record it as not executed rather than exposing secrets to PR code.

- [ ] **Step 6: Final dead-reference/security audit**

Run:

```bash
git grep -n -E 'runs-on:.*self-hosted|vpn-e2e.*runner|e2e-tun-package-convergence|e2e-tun-resource-soak|ubuntu-22\.04|actions/(checkout|setup-go|upload-artifact|download-artifact)@v' .github scripts || true
```

Expected: no active-runner/retired-workflow/Ubuntu-22.04/action-tag references. A retained `environment: vpn-e2e` reference is allowed because it is the protected secrets/approval boundary, not a runner label.

- [ ] **Step 7: Remove temporary spec and plan and run final structure guard**

Delete both `docs/superpowers/**` files created for this change, then run:

```bash
bash scripts/ci/repository-structure.sh --final
```

Expected: PASS.

- [ ] **Step 8: Final repository verification**

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
bash scripts/ci/workflow-lint.sh
bash scripts/ci/repository-structure.sh --final
```

Expected: all PASS.

- [ ] **Step 9: Review final PR diff**

Verify:

- exactly `ci.yml`, `integration.yml`, `release.yml` remain as active product workflows;
- no deleted-workflow test wiring remains;
- no accidental product lifecycle/network semantics changed;
- no provider secret can flow into pull-request jobs;
- release publication is blocked on exact-tag deterministic and real-provider integration;
- all external Actions use immutable SHAs;
- no temporary `docs/superpowers/**` files remain.

- [ ] **Step 10: Commit final cleanup**

```bash
git add -A
 git commit -m "chore: finalize CI/CD simplification"
```
