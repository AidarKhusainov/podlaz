# CI/CD Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce Podlaz CI/CD to three active, runnable workflows (`ci.yml`, `integration.yml`, `release.yml`), remove retired self-hosted infrastructure and meta-testing, preserve critical TUN safety specifications, and make hosted/release verification explicit and reproducible.

**Architecture:** Keep PR gating deterministic and secret-free, run installed-product checks on GitHub-hosted Ubuntu 24.04, isolate real-provider proxy checks behind the existing protected GitHub Environment, and make release validate the exact tag SHA and built package. Remove orchestration and harnesses that exist only for the absent self-hosted runner; retain only focused destructive-network scripts that encode safety-critical invariants.

**Tech Stack:** GitHub Actions, Bash, Go 1.26.6, Debian packaging, systemd/journald, Python 3 helpers.

**Spec:** `docs/superpowers/specs/2026-09-04-ci-cd-simplification-design.md`

## Global Constraints

- Preserve product behavior, package/service compatibility, lifecycle ordering, recovery semantics, network ownership, and privacy guarantees.
- `proxy-only` integration must not mutate host networking.
- Destructive TUN/route/rule/DNS/nftables/NetworkManager/suspend-recovery scenarios must not run on general GitHub-hosted runners.
- PR CI stays deterministic and receives no VPN/provider secrets.
- Real-provider integration may run only from trusted `master` or an exact release tag through the protected Environment boundary.
- Active jobs use explicit `ubuntu-24.04`; no active job keeps `ubuntu-22.04`.
- Pin external Actions to these immutable commit SHAs:
  - `actions/checkout`: `3d3c42e5aac5ba805825da76410c181273ba90b1` (`v7`)
  - `actions/setup-go`: `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` (`v7`)
  - `actions/upload-artifact`: `043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` (`v7`)
  - `actions/download-artifact`: `3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c` (`v8`)
  - `actions/attest-build-provenance`: `4d101475d8b20a2381f78447822ac1eab6504dd8` (`v4`)
- Use `persist-credentials: false` for checkout unless authenticated Git operations are actually required later in that job.
- Tests protect behavior or durable repository invariants, not retired workflow wiring or incidental shell implementation shape.
- No real user IPs/domains/profiles/secrets in source, tests, PR text, or logs.
- Remove this plan and its temporary spec before final `repository-structure.sh --final`.

---

### Task 1: Simplify and harden deterministic CI

**Files:**
- Modify: `.github/workflows/ci.yml`
- Delete: `scripts/ci/boot-continuation-contract.sh`
- Test: `scripts/ci/validate-package-workflow-contract-test.sh`
- Test: `scripts/ci/repository-structure-test.sh`

**Interfaces:**
- Consumes: `scripts/ci/go-core.sh`, `scripts/ci/cli-smoke.sh`, `scripts/e2e/cli-contract.sh`, package build/validation scripts.
- Produces: one deterministic PR/master merge-gate workflow with no private credentials and no self-hosted dependency.

- [ ] **Step 1: Characterize current behavior**

```bash
bash scripts/ci/validate-package-workflow-contract-test.sh
bash scripts/ci/repository-structure-test.sh
go test ./scripts/e2e -count=1
```

Expected: PASS before edits.

- [ ] **Step 2: Update trigger and concurrency**

Final trigger:

```yaml
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review, converted_to_draft]
  push:
    branches:
      - master

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
```

- [ ] **Step 3: Pin Actions and move Debian job to Ubuntu 24.04**

Apply the SHAs from Global Constraints. Every checkout uses:

```yaml
with:
  persist-credentials: false
```

Change the package job to `runs-on: ubuntu-24.04`.

- [ ] **Step 4: Remove redundant boot-continuation rerun**

Delete this step and script:

```yaml
- name: Boot continuation contract
  run: bash scripts/ci/boot-continuation-contract.sh
```

`go-core.sh` already runs `go test ./...`; keep `validate-installed-status-test.sh` through workflow lint.

- [ ] **Step 5: Add deterministic CLI behavior contract**

After the completion smoke step add:

```yaml
- name: CLI behavior contract
  run: bash scripts/e2e/cli-contract.sh
```

- [ ] **Step 6: Verify and commit**

```bash
bash scripts/ci/workflow-lint.sh
bash scripts/ci/validate-package-workflow-contract-test.sh
bash scripts/ci/repository-structure-test.sh
go test ./scripts/e2e -count=1
git add .github/workflows/ci.yml scripts/ci
git commit -m "ci: simplify deterministic merge gate"
```

---

### Task 2: Make ordinary-user installed-package acceptance deterministic

**Files:**
- Modify: `scripts/e2e/remote-client-acceptance.sh`
- Modify: `scripts/e2e/remote_client_acceptance_test.go`
- Modify: `scripts/e2e/log_window_contract_test.go`
- Keep behavior: `scripts/e2e/log-window-acceptance.sh`

**Interfaces:**
- Consumes: an installed native package and active `podlazd.service`.
- Produces: provider-independent ordinary-user status/doctor/recover/log acceptance using a deterministic fake Xray and example profile.

- [ ] **Step 1: Change the test first**

Require the remote-client script to preserve ordinary login identity and contain deterministic fixture markers:

```go
required := []string{
    "ordinary-user acceptance must not run as root",
    "ordinary_user_without_podlaz_group",
    "PODLAZ_XRAY_PATH",
    "remote-client.example.net",
    "connect --mode proxy-only",
    "recover --json",
    "logs \"--${mode}\" --since 36h",
}
```

The executable script must not require `PODLAZ_E2E_PROFILE_URI`/list. Remove `TestTunPackageConvergenceRunsRemoteClientAcceptance`.

Run:

```bash
go test ./scripts/e2e -run 'TestRemoteClientAcceptance' -count=1
```

Expected: FAIL before script changes.

- [ ] **Step 2: Add deterministic fake-Xray fixture**

Create it under `E2E_TMP_ROOT`. It accepts only `run -config <path>`, verifies the config is readable, traps TERM/INT, and stays alive. Install an exact temporary systemd drop-in under `/run/systemd/system/podlazd.service.d/` containing:

```ini
[Service]
Environment=PODLAZ_XRAY_PATH=<fixture-path>/xray
```

Use only this fixed example profile:

```text
vless://00000000-0000-0000-0000-000000000077@remote-client.example.net:443?type=tcp&security=tls&encryption=none#remote-client
```

- [ ] **Step 3: Make fixture cleanup exact and fail-closed**

The EXIT trap disconnects if needed, removes only its own drop-in, reloads systemd, restores the daemon baseline, and preserves the original failure unless cleanup is the only failure. Package purge remains job-owned.

- [ ] **Step 4: Remove workflow-binding log test**

Delete only `TestTunPackageConvergenceRunsStrictLogWindowAcceptance`; keep the actual log-window invariant test.

- [ ] **Step 5: Verify and commit**

```bash
go test ./scripts/e2e -run 'Test(RemoteClientAcceptance|StrictLogWindowAcceptance)' -count=1
bash -n scripts/e2e/remote-client-acceptance.sh
bash -n scripts/e2e/log-window-acceptance.sh
shellcheck -x -s bash -P scripts/e2e -e SC1091 scripts/e2e/remote-client-acceptance.sh scripts/e2e/log-window-acceptance.sh
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
- Produces: hosted package/runtime integration on `master`, plus safe manual diagnosis.

- [ ] **Step 1: Create workflow trigger and permissions**

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

Use `ubuntu-24.04`, pinned checkout/setup-go, `persist-credentials: false`, then:

```bash
PODLAZ_E2E_KEEP_PACKAGE=true bash scripts/e2e/package-service.sh
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
bash scripts/e2e/log-window-acceptance.sh
bash scripts/e2e/remote-client-acceptance.sh
```

Always purge package/service state afterward and upload bounded diagnostics.

- [ ] **Step 3: Add temporary feature-branch trigger for runtime proof**

During implementation only, also trigger deterministic integration on `agent/ci-cd-simplification`. Remove that branch from the workflow after one green hosted run and before final PR review.

- [ ] **Step 4: Verify and commit**

```bash
actionlint .github/workflows/integration.yml
bash scripts/ci/workflow-lint.sh
git add .github/workflows/integration.yml
git commit -m "ci: add hosted installed-product integration"
```

---

### Task 4: Add trusted real-provider proxy integration and prebuilt-package support

**Files:**
- Modify: `.github/workflows/integration.yml`
- Modify: `scripts/e2e/data-plane.sh`
- Create: `scripts/e2e/data_plane_package_contract_test.go`

**Interfaces:**
- Consumes: protected provider secrets and optional prebuilt native `.deb`.
- Produces: scheduled/manual real proxy verification and a data-plane harness reusable by release.

- [ ] **Step 1: Write failing package-selection contract**

The test requires `PODLAZ_E2E_PACKAGE_PATH` and asserts that the script has two branches: explicit prebuilt package validation/install, or existing dev-package build when unset.

```bash
go test ./scripts/e2e -run 'TestDataPlanePackageSelection' -count=1
```

Expected: FAIL before implementation.

- [ ] **Step 2: Implement prebuilt package selection**

Add:

```bash
: "${PODLAZ_E2E_PACKAGE_PATH:=}"
```

If set:

```bash
[[ -f "${PODLAZ_E2E_PACKAGE_PATH}" ]] || fail "configured data-plane package is missing"
package_arch="$(dpkg-deb --field "${PODLAZ_E2E_PACKAGE_PATH}" Architecture)"
[[ "${package_arch}" == "${HOST_DEB_ARCH}" ]] || fail "configured data-plane package architecture mismatch"
INSTALL_DEB="${PODLAZ_E2E_PACKAGE_PATH}"
```

If unset, retain current nfpm/dev build and set `INSTALL_DEB="${DEV_DEB}"`. Install `"${INSTALL_DEB}"`.

- [ ] **Step 3: Add trusted real-provider job**

Condition:

```yaml
if: >-
  ${{ github.event_name == 'schedule' ||
      (github.event_name == 'workflow_dispatch' &&
       github.ref == 'refs/heads/master' &&
       inputs.real_provider) }}
```

Use `ubuntu-24.04` and `environment: vpn-e2e`. Pass existing profile/egress secrets only to this job. Default scheduled/manual reliability to existing configured value or `3`, not the retired long soak.

- [ ] **Step 4: Verify and commit**

```bash
go test ./scripts/e2e -run 'TestDataPlanePackageSelection' -count=1
bash -n scripts/e2e/data-plane.sh
bash scripts/ci/workflow-lint.sh
git add .github/workflows/integration.yml scripts/e2e/data-plane.sh scripts/e2e/data_plane_package_contract_test.go
git commit -m "ci: add trusted proxy data-plane integration"
```

---

### Task 5: Qualify the exact release package before publication

**Files:**
- Modify: `.github/workflows/release.yml`
- Reuse: `scripts/e2e/log-window-acceptance.sh`
- Reuse: `scripts/e2e/remote-client-acceptance.sh`
- Reuse: `scripts/e2e/data-plane.sh`

**Interfaces:**
- Consumes: exact tag SHA and `.deb` artifacts produced by `build`.
- Produces: deterministic and real-provider gates on the exact release artifact before attestation/publication.

- [ ] **Step 1: Pin Actions and move release jobs to Ubuntu 24.04**

Apply Global Constraints and `persist-credentials: false`.

- [ ] **Step 2: Add deterministic release integration job**

After `build`, check out the exact tag, download the existing release artifact, install the amd64 `.deb`, start/verify service, run:

```bash
bash scripts/e2e/log-window-acceptance.sh
bash scripts/e2e/remote-client-acceptance.sh
```

Always purge package/test drop-ins afterward.

- [ ] **Step 3: Add exact-tag real-provider job**

Using `environment: vpn-e2e`, download the same release artifact and run:

```bash
PODLAZ_E2E_PACKAGE_PATH="dist/release/podlaz_${VERSION}_linux_amd64.deb" \
PODLAZ_E2E_RELIABILITY_CYCLES="${PODLAZ_E2E_RELEASE_RELIABILITY_CYCLES:-10}" \
  bash scripts/e2e/data-plane.sh
```

Use a workflow variable for release reliability when configured; otherwise the bounded default is 10.

- [ ] **Step 4: Gate publication**

Final publication job depends on `resolve`, `build`, deterministic release integration, and release data-plane. Keep write/OIDC permissions only on final publication.

- [ ] **Step 5: Verify and commit**

```bash
actionlint .github/workflows/release.yml
bash scripts/ci/validate-package-workflow-contract.sh
bash scripts/ci/workflow-lint.sh
git add .github/workflows/release.yml
git commit -m "ci: qualify exact release package before publish"
```

---

### Task 6: Remove retired self-hosted orchestration and wiring tests

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
- Consumes: new active workflow topology.
- Produces: no active or test-only dependency on the absent `vpn-e2e` runner.

- [ ] **Step 1: Capture references before deletion**

```bash
git grep -n -E 'e2e-tun-package-convergence\.yml|e2e-tun-resource-soak\.yml|runs-on: \[self-hosted|vpn-e2e|bootstrap-e2e-runner'
```

Use this output only as PR/task evidence.

- [ ] **Step 2: Delete retired workflow/runner files**

Delete the five files listed above.

- [ ] **Step 3: Remove workflow-wiring tests and preserve real invariants**

Delete tests whose sole contract is that a retired workflow invokes a script. For network recovery, keep the v0.2.29 boundary by checking `network-recovery-package-acceptance.sh` directly for its default and exact-version guard instead of reading a deleted workflow.

- [ ] **Step 4: Clean runner-specific comments**

```bash
git grep -n -E 'self-hosted|vpn-e2e' scripts/e2e scripts/ci
```

Rewrite infrastructure-only wording; keep dedicated-host requirements where destructive semantics genuinely need such a host.

- [ ] **Step 5: Verify and commit**

```bash
go test ./scripts/e2e -count=1
bash scripts/ci/workflow-lint.sh
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
- Delete: `scripts/e2e/tun-resource-soak.sh`
- Delete: `scripts/e2e/tun-resource-soak-policy.json`
- Delete: `scripts/e2e/lib/tun_soak_analysis.py`
- Delete: `scripts/e2e/lib/tun_soak_cleanup.sh`
- Delete: `scripts/e2e/lib/tun_soak_environment.py`
- Delete: `scripts/e2e/lib/tun_soak_health.sh`
- Delete: `scripts/e2e/lib/tun_soak_isolation.py`
- Delete: `scripts/e2e/lib/tun_soak_metrics.py`
- Delete: `scripts/e2e/lib/tun_soak_process.py`
- Delete: `scripts/e2e/lib/tun_soak_status.py`
- Delete: `scripts/e2e/tests/test_tun_resource_soak_contract.py`
- Delete all dedicated `scripts/e2e/tests/test_tun_soak_*.py`
- Modify: `internal/logs/build_args_test.go`
- Modify: `internal/app/cli/logs_matrix_test.go`

**Interfaces:**
- Consumes: focused deterministic integration and retained destructive safety scripts.
- Produces: smaller E2E surface without issue-coverage generator, legacy real-VPN wrappers, dormant soak subsystem, or catch-all server scenario.

- [ ] **Step 1: Prove stale legacy scripts are superseded**

```bash
git grep -n 'coverage-evidence.sh' || true
git grep -n 'real-vpn.sh' || true
git grep -n 'real-vpn-extended.sh' || true
```

References must be limited to retired/meta surfaces before deletion.

- [ ] **Step 2: Audit `server-coverage.sh` by invariant**

Compare its package/service/proxy/TUN/crash/concurrency/DNS/host-disruption behavior with `data-plane.sh`, `package-service.sh`, retained focused TUN acceptance, and Go daemon/recovery tests. Put the migration map in PR notes, not a permanent file. If no safety-critical unique invariant remains, delete the catch-all. If one remains, extract only that invariant into one focused domain-named scenario first.

- [ ] **Step 3: Remove implementation-shape meta-tests**

Delete checks that freeze helper names, literal timeouts, source placement, or retired workflow wiring without executing behavior. Preserve executable helper behavior tests.

- [ ] **Step 4: Delete soak subsystem**

Delete the listed soak script/policy/libraries/tests, then:

```bash
git grep -n -E 'tun-resource-soak|tun_soak_' || true
```

Expected: no maintained source/workflow reference remains.

- [ ] **Step 5: Rename issue-oriented test identifiers without changing behavior**

```text
TestBuildJournalctlArgsIssue160FlagMatrix -> TestBuildJournalctlArgsFlagMatrix
TestRunCLILogsParsesAdditionalIssue160Flags -> TestRunCLILogsParsesSupportedFlags
```

- [ ] **Step 6: Verify and commit**

```bash
go test ./...
python3 -m unittest discover -s scripts/e2e/tests -p 'test_*.py'
bash scripts/ci/workflow-lint.sh
git grep -n -E 'Issue160|issue-160|coverage-evidence|real-vpn|server-coverage|tun-resource-soak|tun_soak_' || true
git add -A scripts/e2e internal
git commit -m "test: remove stale E2E infrastructure"
```

---

### Task 8: Validate hosted runtime, remove temporary docs, and open the PR

**Files:**
- Finalize: `.github/workflows/ci.yml`
- Finalize: `.github/workflows/integration.yml`
- Finalize: `.github/workflows/release.yml`
- Delete: `docs/superpowers/specs/2026-09-04-ci-cd-simplification-design.md`
- Delete: `docs/superpowers/plans/2026-09-04-ci-cd-simplification.md`

**Interfaces:**
- Consumes: all previous tasks.
- Produces: PR-ready repository with exactly three active workflows and no temporary plan/spec files.

- [ ] **Step 1: Run pre-runtime verification**

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
bash scripts/ci/repository-structure.sh
bash scripts/ci/workflow-lint.sh
bash scripts/ci/validate-package-workflow-contract-test.sh
```

- [ ] **Step 2: Push feature branch and obtain deterministic hosted integration evidence**

Push while the temporary `agent/ci-cd-simplification` trigger is present. Inspect the new Integration run and require the deterministic installed-package job to pass. Fix real failures before continuing.

- [ ] **Step 3: Remove temporary branch trigger**

Final `integration.yml` must trigger only on `master`, schedule, and workflow dispatch.

- [ ] **Step 4: Remove temporary design/plan and run final structure guard**

Delete both `docs/superpowers/**` files and run:

```bash
bash scripts/ci/repository-structure.sh --final
```

Expected: PASS.

- [ ] **Step 5: Run final repository verification**

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
bash scripts/ci/workflow-lint.sh
bash scripts/ci/repository-structure.sh --final
```

- [ ] **Step 6: Run final dead-reference/security audit**

```bash
git grep -n -E 'runs-on:.*self-hosted|vpn-e2e.*runner|e2e-tun-package-convergence|e2e-tun-resource-soak|ubuntu-22\.04|actions/(checkout|setup-go|upload-artifact|download-artifact)@v' .github scripts || true
```

A retained `environment: vpn-e2e` is allowed because it is the protected secrets/approval boundary, not a runner label.

- [ ] **Step 7: Commit final cleanup**

```bash
git add -A
git commit -m "chore: finalize CI/CD simplification"
```

- [ ] **Step 8: Open one PR to `master`**

PR body includes the workflow migration map, deleted self-hosted/legacy/meta/soak surfaces, retained non-automated destructive TUN safety harnesses, exact validation performed, and an explicit statement that destructive TUN acceptance was not run because no safe dedicated runner exists.

- [ ] **Step 9: Obtain fresh PR CI evidence**

Inspect CI for the PR head SHA. Fix any actual failures and update the PR. Real-provider pre-merge execution is optional only when the protected Environment cannot safely execute branch code; never expose secrets to PR/fork code merely to obtain evidence.

- [ ] **Step 10: Final diff review**

Confirm:

- exactly `ci.yml`, `integration.yml`, `release.yml` remain as active product workflows;
- no retired-workflow wiring tests remain;
- no product lifecycle/network semantics changed accidentally;
- provider secrets cannot flow into PR jobs;
- release publication is blocked on exact-tag deterministic and real-provider checks;
- external Actions are pinned by immutable SHA;
- no temporary `docs/superpowers/**` files remain.
