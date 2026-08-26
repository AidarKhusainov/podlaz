# Release Laptop Acceptance Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build one safe, resumable maintainer-laptop harness that qualifies an already-built Podlaz Debian release through the complete package, lifecycle, privacy, coexistence, soak, terminal, reboot, and restoration contract.

**Architecture:** Keep `scripts/acceptance/release-laptop.sh` as the only user-facing entrypoint and delegate typed orchestration to a Python 3 standard-library package under `scripts/acceptance/release_acceptance`. All host mutations pass through an injected command runner and a crash-atomic checkpoint/ledger; deterministic unit tests use fake runners, fake filesystems, and fake clocks, while the real script only coordinates installed release binaries and exact host evidence.

**Tech Stack:** Bash 4+, Python 3 standard library (`argparse`, `dataclasses`, `enum`, `json`, `pathlib`, `subprocess`, `os`, `fcntl`, `hashlib`, `statistics`), Go test wrappers already used by `scripts/e2e`, shellcheck.

**Spec:** `docs/superpowers/specs/2026-08-26-release-laptop-acceptance-design.md`

## Global Constraints

- Test prebuilt native `podlaz` `.deb` files only; never build Podlaz or install/update dependencies.
- Permit only exact supplied-package `dpkg -i`; never invoke apt, dependency repair, broad route/rule/nft cleanup, or generic product recovery.
- Run normal Podlaz CLI commands as the original `SUDO_USER`; retain privileged host orchestration in the root harness.
- Every mutation that can outlive the process uses durable `acquiring/acquired/releasing/released` authority before and after mutation.
- Fail closed on ambiguous identity, ownership, package, process, network, checkpoint, artifact-path, or privacy evidence.
- A failed/timeout external direct probe is never leak-protection proof without exact local Privacy Envelope/packet-path evidence.
- Public output must structurally redact host interfaces and exclude profiles, endpoints, credentials, addresses, SSIDs, UUIDs, transaction/session/boot IDs, and private checkpoint values.
- Preserve existing isolated-runner foreign-process veto semantics; laptop attribution uses only positive exact Podlaz daemon/Xray evidence.
- The candidate stays installed; all other harness-owned state and original autostart/service/Wi-Fi boundaries are restored exactly.

## File Map

- `scripts/acceptance/release-laptop.sh`: root/argument/bootstrap boundary and only public entrypoint.
- `scripts/acceptance/release_acceptance/cli.py`: argument modes and stable exit codes.
- `scripts/acceptance/release_acceptance/model.py`: strict checkpoint, ledger, scenario, report, and identity types.
- `scripts/acceptance/release_acceptance/command.py`: bounded subprocess execution and original-user Podlaz calls.
- `scripts/acceptance/release_acceptance/artifacts.py`: safe artifact tree, private evidence, structural redaction.
- `scripts/acceptance/release_acceptance/checkpoint.py`: durable atomic checkpoint and mutation reconciliation.
- `scripts/acceptance/release_acceptance/packages.py`: `.deb` provenance/order and crash-atomic previous-package setup.
- `scripts/acceptance/release_acceptance/product.py`: installed CLI/status/doctor/session/autostart adapters.
- `scripts/acceptance/release_acceptance/privacy.py`: direct-uplink tripwire plus local envelope proof.
- `scripts/acceptance/release_acceptance/lifecycle.py`: upgrade/reinstall/restart/crash/rollback/stop-start scenarios.
- `scripts/acceptance/release_acceptance/fixtures.py`: exact Fixture A/B allocation, mutation, verification, cleanup.
- `scripts/acceptance/release_acceptance/resources.py`: positive process/cgroup attribution, samples, scoped metrics.
- `scripts/acceptance/release_acceptance/host_events.py`: NetworkManager Wi-Fi and timed suspend orchestration.
- `scripts/acceptance/release_acceptance/soak.py`: fake-clock-compatible 60-minute scheduler/workload.
- `scripts/acceptance/release_acceptance/reboot.py`: boot checkpoint phases and terminal profile lifecycle.
- `scripts/acceptance/release_acceptance/report.py`: qualification evaluation and three sanitized reports.
- `scripts/acceptance/release_acceptance/orchestrator.py`: ordered phase state machine, resume, abort, finalizer.
- `scripts/acceptance/tests/`: stdlib `unittest` suite and fakes, split by component.
- `scripts/acceptance/acceptance_test.go`: makes Python and shell contract tests part of `go test ./...`.
- `docs/e2e.md`, `docs/release.md`: operator workflow, safety boundary, result semantics.

---

### Task 1: Typed foundation, command boundary, and public entrypoint

**Files:**
- Create: `scripts/acceptance/release-laptop.sh`
- Create: `scripts/acceptance/release_acceptance/__init__.py`
- Create: `scripts/acceptance/release_acceptance/model.py`
- Create: `scripts/acceptance/release_acceptance/command.py`
- Create: `scripts/acceptance/release_acceptance/cli.py`
- Create: `scripts/acceptance/tests/fakes.py`
- Create: `scripts/acceptance/tests/test_cli.py`
- Create: `scripts/acceptance/acceptance_test.go`

**Interfaces:**
- Produces: `CommandRunner.run(argv, *, timeout, user=None, env=None) -> CommandResult`; `RunMode.NEW|RESUME|ABORT`; strict `RunConfig`; CLI exits `0` only for completed/clean abort, `1` runtime failure, `2` usage/preflight failure.

- [ ] **Step 1: Write failing CLI and command-runner tests**

```python
def test_resume_rejects_new_inputs(self):
    with self.assertRaises(SystemExit) as raised:
        parse_args(["--resume", "candidate.deb"])
    self.assertEqual(raised.exception.code, 2)

def test_podlaz_command_is_bound_to_original_user(self):
    runner = FakeRunner()
    runner.run_as_user(1000, ["/usr/bin/podlaz", "status", "--json"])
    self.assertEqual(runner.calls[0].argv[:4], ("runuser", "-u", "maintainer", "--"))
```

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./scripts/acceptance -run 'TestAcceptancePython|TestReleaseLaptopEntrypoint' -v`
Expected: FAIL because the entrypoint/package and wrapper do not exist.

- [ ] **Step 3: Implement minimal typed CLI and bounded runner**

```python
@dataclass(frozen=True)
class CommandResult:
    argv: tuple[str, ...]
    returncode: int
    stdout: str
    stderr: str

class CommandRunner:
    def run(self, argv: Sequence[str], *, timeout: float, user: UserIdentity | None = None) -> CommandResult:
        command = ["runuser", "-u", user.name, "--", *argv] if user else list(argv)
        completed = subprocess.run(command, text=True, capture_output=True, timeout=timeout, check=False)
        return CommandResult(tuple(argv), completed.returncode, completed.stdout, completed.stderr)
```

The shell script must require root plus a non-root `SUDO_USER`, resolve its own real directory without trusting `PATH`, reject symlinked entry execution, set `PYTHONDONTWRITEBYTECODE=1`, and `exec python3 -m release_acceptance.cli "$@"`.

- [ ] **Step 4: Run tests and shellcheck**

Run: `go test ./scripts/acceptance -v && shellcheck scripts/acceptance/release-laptop.sh`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance
git commit -m "test: add release acceptance harness foundation"
```

### Task 2: Safe artifact tree and deterministic redaction

**Files:**
- Create: `scripts/acceptance/release_acceptance/artifacts.py`
- Create: `scripts/acceptance/tests/test_artifacts.py`
- Modify: `scripts/acceptance/release_acceptance/model.py`

**Interfaces:**
- Consumes: `UserIdentity`, `CommandResult`.
- Produces: `ArtifactStore.create(path, user)`, `write_private(name, data)`, `write_public_json(name, payload)`, `RoleMap.interface(real_name, role)`.

- [ ] **Step 1: Test unsafe paths, ownership, symlinks, modes, and redaction parity**

```python
def test_existing_symlink_component_is_rejected(self):
    self.fs.symlink("elsewhere", self.root / "reports")
    with self.assertRaises(UnsafePath):
        ArtifactStore.create(self.root / "reports" / "run", self.user, fs=self.fs)

def test_public_payload_contains_roles_not_private_identifiers(self):
    public = self.store.sanitize({"interface": "wlp-real", "profile_id": "secret-id"}, self.roles)
    self.assertEqual(public["interface"], "wifi_uplink")
    self.assertNotIn("secret-id", json.dumps(public))
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_artifacts -v`
Expected: FAIL with missing `artifacts` module.

- [ ] **Step 3: Implement descriptor-relative, no-follow path validation and public/private writers**

Use `os.open(..., O_DIRECTORY|O_NOFOLLOW)`, `os.stat(..., follow_symlinks=False)`, explicit UID/GID/modes, atomic same-directory writes, bounded UTF-8/JSON sizes, and a final structural scanner shared by all three public reports.

- [ ] **Step 4: Run focused tests**

Run: `python3 -m unittest scripts.acceptance.tests.test_artifacts -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance/release_acceptance scripts/acceptance/tests
git commit -m "feat: add safe acceptance artifact boundary"
```

### Task 3: Durable checkpoint and generic mutation ledger

**Files:**
- Create: `scripts/acceptance/release_acceptance/checkpoint.py`
- Create: `scripts/acceptance/tests/test_checkpoint.py`
- Modify: `scripts/acceptance/release_acceptance/model.py`

**Interfaces:**
- Produces: `CheckpointStore.load/create/replace/remove`; `MutationLedger.begin_acquire`, `mark_acquired`, `begin_release`, `mark_released`, `reconcile`; `MutationState` enum.

- [ ] **Step 1: Write strict schema and crash-atomic filesystem tests**

```python
def test_commit_flushes_file_then_rename_then_directory(self):
    self.store.replace(self.checkpoint)
    self.assertEqual(self.fs.events[-3:], ["fsync:file", "replace:checkpoint", "fsync:directory"])

def test_acquiring_ambiguous_live_state_retains_authority(self):
    with self.assertRaises(AmbiguousMutation):
        self.ledger.reconcile("fixture", live=self.foreign_state)
    self.assertEqual(self.store.load().mutations["fixture"].state, MutationState.ACQUIRING)
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_checkpoint -v`
Expected: FAIL.

- [ ] **Step 3: Implement versioned bounded decoding and durable replacement**

```python
class MutationState(str, Enum):
    ACQUIRING = "acquiring"
    ACQUIRED = "acquired"
    RELEASING = "releasing"
    RELEASED = "released"

def atomic_replace(path: Path, payload: bytes, fs: FileSystem) -> None:
    temp = fs.create_temp(path.parent, mode=0o600)
    fs.write_all(temp, payload); fs.fsync(temp); fs.replace(temp, path); fs.fsync_directory(path.parent)
```

- [ ] **Step 4: Test every generic crash point and lock exclusivity**

Run: `python3 -m unittest scripts.acceptance.tests.test_checkpoint -v`
Expected: PASS for before/acquiring/mutated/acquired/releasing/released boundaries.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance/release_acceptance scripts/acceptance/tests
git commit -m "feat: add crash-atomic acceptance checkpoint"
```

### Task 4: Package provenance, Debian ordering, and previous-package restoration

**Files:**
- Create: `scripts/acceptance/release_acceptance/packages.py`
- Create: `scripts/acceptance/tests/test_packages.py`
- Modify: `scripts/acceptance/release_acceptance/orchestrator.py` (create skeleton)

**Interfaces:**
- Produces: `PackageInspector.inspect(path) -> DebIdentity`; `compare_versions(previous, candidate)`; `PackageSetup.acquire/reconcile/release`.

- [ ] **Step 1: Write package validation/order and three P1 crash-point tests**

```python
def test_previous_installed_before_commit_is_adopted_then_candidate_restored(self):
    setup = self.fixture.crash_at("previous_installed_before_acquired_commit")
    resumed = setup.reconcile()
    self.assertEqual(resumed.state, MutationState.ACQUIRED)
    setup.release()
    self.assertEqual(self.runner.installed_version, "0.2.30")

def test_foreign_installed_version_fails_closed(self):
    self.runner.installed_version = "0.2.29+local"
    with self.assertRaises(AmbiguousPackageState):
        self.setup.reconcile()
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_packages -v`
Expected: FAIL.

- [ ] **Step 3: Implement exact metadata/digest/checksum/version/package-state checks**

Use only `dpkg-deb --field`, `dpkg --print-architecture`, `dpkg-query`, `dpkg --compare-versions`, `sha256sum`, and exact `dpkg -i`. Validate regular no-symlink paths and sibling `SHA256SUMS` exact filename records. Persist candidate path device/inode plus digest before downgrade; never infer installed file digest from version alone.

- [ ] **Step 4: Add a forbidden-command assertion**

```python
for call in self.runner.calls:
    self.assertNotIn(call.argv[0:2], [("apt", "install"), ("apt-get", "install")])
```

Run: `python3 -m unittest scripts.acceptance.tests.test_packages -v`
Expected: PASS, including all three specified package crash points.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance
git commit -m "feat: validate and restore release package setup"
```

### Task 5: Product/user/profile/autostart adapters

**Files:**
- Create: `scripts/acceptance/release_acceptance/product.py`
- Create: `scripts/acceptance/tests/test_product.py`
- Modify: `scripts/acceptance/release_acceptance/model.py`

**Interfaces:**
- Produces: `ProductClient.status/doctor/connect/disconnect/autostart`; `ProfileLease.acquire/reconcile/release`; typed `ProductSnapshot`.

- [ ] **Step 1: Test original-user execution, unambiguous profile selection, status exit handling, and dynamic profile identity**

```python
def test_profile_crash_after_create_adopts_only_unique_baseline_addition(self):
    lease = self.fixture.crash_after_profile_create()
    self.assertEqual(lease.reconcile().profile_id, "generated-id")

def test_multiple_matching_additions_fail_closed(self):
    self.runner.profile_additions = ["id-a", "id-b"]
    with self.assertRaises(AmbiguousProfileState):
        self.lease.reconcile()
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_product -v`
Expected: FAIL.

- [ ] **Step 3: Implement bounded JSON adapters and exclusive profile-state lock**

All normal CLI calls use `/usr/bin/podlaz` through the original-user runner. Parse only documented versioned JSON where available; retain raw output privately; never copy profile URI/config into public state.

- [ ] **Step 4: Run tests**

Run: `python3 -m unittest scripts.acceptance.tests.test_product -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance
git commit -m "feat: add acceptance product state adapters"
```

### Task 6: Combined privacy proof

**Files:**
- Create: `scripts/acceptance/release_acceptance/privacy.py`
- Create: `scripts/acceptance/tests/test_privacy.py`

**Interfaces:**
- Produces: `PrivacyObserver.baseline`, `observe_protected`, `observe_ordinary`; typed `PrivacyVerdict` with functional and local-evidence fields.

- [ ] **Step 1: Test the strict truth table**

```python
def test_timeout_without_local_envelope_is_inconclusive_failure(self):
    verdict = self.observer.classify(direct=Probe.TIMEOUT, local=None, protected=True)
    self.assertEqual(verdict.outcome, ScenarioOutcome.FAIL)

def test_direct_success_during_protection_is_immediate_leak(self):
    verdict = self.observer.classify(direct=Probe.SUCCESS, local=self.valid_local, protected=True)
    self.assertEqual(verdict.reason, "direct_egress_leak")
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_privacy -v`
Expected: FAIL.

- [ ] **Step 3: Implement bound-uplink probes and exact local evidence matching**

Require a reachable pre-connect direct baseline; bind functional probes to the privately identified physical/default uplink; validate exact current session/transaction/envelope composition from installed product evidence. Store response bodies nowhere.

- [ ] **Step 4: Run tests**

Run: `python3 -m unittest scripts.acceptance.tests.test_privacy -v`
Expected: PASS for every functional/local evidence combination.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance
git commit -m "feat: add deterministic release privacy proof"
```

### Task 7: Package and daemon lifecycle scenarios

**Files:**
- Create: `scripts/acceptance/release_acceptance/lifecycle.py`
- Create: `scripts/acceptance/tests/test_lifecycle.py`

**Interfaces:**
- Produces: separate scenario functions for lower upgrade, graceful restart, SIGKILL recovery, durable rollback interruption, explicit stop/start, candidate reinstall, runtime terminal convergence.

- [ ] **Step 1: Test ordering and forbidden repair for every scenario**

```python
def test_upgrade_connects_lower_then_installs_candidate_without_second_connect(self):
    self.lifecycle.lower_upgrade()
    self.assertEqual(self.runner.operations, ["connect:previous", "dpkg:candidate", "observe:candidate"])

def test_durable_rollback_kills_only_after_rolling_back_authority(self):
    self.lifecycle.durable_rollback_interruption()
    self.assertLess(self.runner.index("observe:rolling_back"), self.runner.index("kill:mainpid"))
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_lifecycle -v`
Expected: FAIL.

- [ ] **Step 3: Implement bounded evidence-driven scenario functions**

Use exact systemd `MainPID` identity/start time before signals. Graceful restart uses `systemctl restart`; unexpected death uses `systemctl kill --kill-who=main --signal=SIGKILL`; rollback/terminal hooks are acquired only through exact reviewed release-built drop-in/marker seams and the generic ledger.

- [ ] **Step 4: Run tests**

Run: `python3 -m unittest scripts.acceptance.tests.test_lifecycle -v`
Expected: PASS; ordinary crash cannot satisfy durable rollback and no scenario calls recover/manual network repair.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance
git commit -m "feat: orchestrate release lifecycle scenarios"
```

### Task 8: Exact coexistence fixtures A and B

**Files:**
- Create: `scripts/acceptance/release_acceptance/fixtures.py`
- Create: `scripts/acceptance/tests/test_fixtures.py`

**Interfaces:**
- Produces: `FixtureAllocator.plan_a/plan_b`; `FixtureLease.acquire/churn/release`; exact tuple models for links, addresses, routes, rules, DNS, nftables.

- [ ] **Step 1: Test pre-existing preservation, live collision recheck, exact cleanup, and ambiguous drift**

```python
def test_historical_occupant_is_preserved_and_candidate_must_allocate_elsewhere(self):
    fixture = self.allocator.plan_a(self.inventory)
    fixture.acquire()
    self.assertTrue(self.host.preexisting_occupant_unchanged())
    self.assertNotIn(self.product.allocation(), fixture.occupied_candidates)

def test_release_never_flushes_namespace(self):
    self.fixture.release()
    self.assertFalse(any(call.argv[:3] == ("ip", "route", "flush") for call in self.runner.calls))
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_fixtures -v`
Expected: FAIL.

- [ ] **Step 3: Implement inventory-first deterministic allocation and ledger-backed exact mutation**

Every object records baseline and exact tuple before mutation, rechecks immediately before create/delete, and releases reverse dependency order. Fixture A exists before candidate connect; Fixture B uses disjoint documentation-safe identities and appears only during active soak.

- [ ] **Step 4: Run tests**

Run: `python3 -m unittest scripts.acceptance.tests.test_fixtures -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance
git commit -m "feat: add exact coexistence acceptance fixtures"
```

### Task 9: Laptop-safe process attribution and scoped resource metrics

**Files:**
- Create: `scripts/acceptance/release_acceptance/resources.py`
- Create: `scripts/acceptance/tests/test_resources.py`
- Modify: `scripts/e2e/lib/tun_soak_process.py` only if factoring a positive shared primitive is demonstrably smaller; otherwise leave it unchanged.

**Interfaces:**
- Produces: `ResourceSampler.identity/sample/summarize`; `ResourceSummary` with sampled-window and lifetime fields.

- [ ] **Step 1: Test positive identity, foreign-process coexistence, cgroup ambiguity, and metric scopes**

```python
def test_foreign_xray_outside_service_is_not_adopted_or_rejected(self):
    identity = self.sampler.identity(self.proc_with_foreign_xray)
    self.assertEqual(identity.xray.pid, 220)

def test_window_peak_uses_memory_current_not_memory_peak(self):
    summary = summarize(self.samples)
    self.assertEqual(summary.soak_memory_current_sampled_peak_bytes, 300)
    self.assertEqual(summary.service_cgroup_lifetime_memory_peak_bytes, 900)
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_resources -v`
Expected: FAIL.

- [ ] **Step 3: Implement exact daemon/transaction/Xray/cgroup identity and sampling**

Require `/usr/bin/podlazd`, `/usr/lib/podlaz/xray`, exact MainPID/start time/cgroup, one committed transaction, transaction `config_ref` in Xray cmdline, parent relationship, and same service cgroup. Reject concrete ambiguity inside the Podlaz cgroup; never scan a laptop-wide product-name blacklist.

- [ ] **Step 4: Run focused and isolated-runner regression tests**

Run: `python3 -m unittest scripts.acceptance.tests.test_resources -v && go test ./scripts/e2e -run 'Soak|Foreign' -v`
Expected: PASS and existing isolated-runner behavior unchanged.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance scripts/e2e/lib/tun_soak_process.py
git commit -m "feat: sample laptop-safe Podlaz resources"
```

### Task 10: Soak scheduler, workload, Wi-Fi, and suspend

**Files:**
- Create: `scripts/acceptance/release_acceptance/host_events.py`
- Create: `scripts/acceptance/release_acceptance/soak.py`
- Create: `scripts/acceptance/tests/test_host_events.py`
- Create: `scripts/acceptance/tests/test_soak.py`

**Interfaces:**
- Produces: injected `Clock`; `SoakScheduler.run(minutes)`; `WifiLease`; `SuspendEvent`.

- [ ] **Step 1: Test exact canonical timeline and skip classifications**

```python
def test_canonical_hour_contains_each_event_once(self):
    result = self.scheduler.run(minutes=60)
    self.assertEqual(result.events, ["fixture_b@20", "wifi@30", "suspend@40"])
    self.assertEqual(result.measured_seconds, 3600)

def test_shortened_soak_caps_qualification(self):
    self.assertTrue(self.scheduler.run(minutes=2).forces_partial)
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_host_events scripts.acceptance.tests.test_soak -v`
Expected: FAIL.

- [ ] **Step 3: Implement monotonic scheduling and exact host-event leases**

Warm-up is minutes 0-5 inside the hour. Sample every 60 seconds, run low-volume protected workload, doctor about every 10 minutes, privacy checks around recovery events. Wi-Fi uses exact NetworkManager connection down/up with ledger restoration; suspend uses bounded RTC wake only on a local supported host. Capability skip and user skip remain distinct.

- [ ] **Step 4: Run tests**

Run: `python3 -m unittest scripts.acceptance.tests.test_host_events scripts.acceptance.tests.test_soak -v`
Expected: PASS without real sleep/network mutation.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance
git commit -m "feat: schedule release soak and host churn"
```

### Task 11: Reboot phases, terminal profile, resume, and abort

**Files:**
- Create: `scripts/acceptance/release_acceptance/reboot.py`
- Create: `scripts/acceptance/tests/test_reboot.py`
- Create: `scripts/acceptance/tests/test_abort.py`
- Modify: `scripts/acceptance/release_acceptance/orchestrator.py`

**Interfaces:**
- Produces: `RebootCoordinator.prepare/resume`; `AbortCoordinator.abort -> ABORTED_CLEAN|ABORT_CLEANUP_FAILED`.

- [ ] **Step 1: Test three real boot transitions and same-boot no-retry authority**

```python
def test_resume_requires_new_boot_id_and_same_candidate(self):
    with self.assertRaises(StaleResume):
        self.reboot.resume(current_boot_id=self.checkpoint.previous_boot_id)

def test_terminal_attempt_fingerprint_survives_daemon_restart(self):
    before = self.product.attempt_fingerprint()
    self.reboot.verify_terminal_same_boot_no_retry()
    self.assertEqual(self.product.attempt_fingerprint(), before)
```

- [ ] **Step 2: Test abort ordering and retained checkpoint on ambiguity**

```python
def test_abort_restores_candidate_before_using_candidate_cli(self):
    self.abort.abort()
    self.assertLess(self.runner.index("dpkg:candidate"), self.runner.index("podlaz:disconnect"))

def test_abort_ambiguity_retains_checkpoint(self):
    self.fixture.make_mutation_ambiguous()
    self.assertEqual(self.abort.abort(), "ABORT_CLEANUP_FAILED")
    self.assertTrue(self.checkpoint.exists())
```

- [ ] **Step 3: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_reboot scripts.acceptance.tests.test_abort -v`
Expected: FAIL.

- [ ] **Step 4: Implement strict phase state machine and reverse-dependency restoration**

Implement `awaiting-autostart-off-reboot`, `awaiting-autostart-on-reboot`, and `awaiting-terminal-autostart-reboot`; no test service/cron/login hook. Abort first reconciles package setup, then product session, NM, fixtures, fault seams, autostart, synthetic profile, ordinary network, artifacts, and finally removes checkpoint only after proof.

- [ ] **Step 5: Run tests**

Run: `python3 -m unittest scripts.acceptance.tests.test_reboot scripts.acceptance.tests.test_abort -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add scripts/acceptance
git commit -m "feat: add resumable reboot and safe abort phases"
```

### Task 12: Qualification evaluator and sanitized reports

**Files:**
- Create: `scripts/acceptance/release_acceptance/report.py`
- Create: `scripts/acceptance/tests/test_report.py`
- Modify: `scripts/acceptance/release_acceptance/model.py`

**Interfaces:**
- Produces: `QualificationEvaluator.evaluate(outcomes, coverage) -> QUALIFIED_PASS|PARTIAL_PASS|FAIL`; writers for `summary.txt`, `report.json`, `requirements-observation.json`.

- [ ] **Step 1: Test verdict matrix, schemas, scope labels, and redaction**

```python
def test_user_skip_or_short_soak_can_never_qualify(self):
    self.assertEqual(self.evaluate(skip_user=True), Qualification.PARTIAL_PASS)
    self.assertEqual(self.evaluate(soak_minutes=59), Qualification.PARTIAL_PASS)

def test_inconclusive_privacy_and_cleanup_failure_are_fail(self):
    self.assertEqual(self.evaluate(privacy="INCONCLUSIVE"), Qualification.FAIL)
    self.assertEqual(self.evaluate(restoration="FAIL"), Qualification.FAIL)
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_report -v`
Expected: FAIL.

- [ ] **Step 3: Implement versioned reports and observation-only requirements schema**

Emit every scenario status, actual soak duration, skip class, privacy evidence class, ledger terminal states, sampled-window metrics, lifetime high-water metrics, and cleanup result. Run the structural scanner before atomic publication; no automatic upload.

- [ ] **Step 4: Run tests**

Run: `python3 -m unittest scripts.acceptance.tests.test_report -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance
git commit -m "feat: report release qualification results"
```

### Task 13: End-to-end orchestration contract with a fully fake host

**Files:**
- Modify: `scripts/acceptance/release_acceptance/orchestrator.py`
- Modify: `scripts/acceptance/release_acceptance/cli.py`
- Create: `scripts/acceptance/tests/test_orchestrator.py`
- Create: `scripts/acceptance/tests/test_forbidden_commands.py`

**Interfaces:**
- Consumes: all preceding components.
- Produces: `ReleaseAcceptance.run_new/resume/abort`; stable checkpoint phase transitions and final result.

- [ ] **Step 1: Write full happy-path and injected-failure orchestration tests**

```python
def test_full_fake_run_reaches_three_reboots_and_qualified_pass(self):
    self.harness.run_new(self.config)
    self.harness.resume(new_boot("b")); self.harness.resume(new_boot("c")); result = self.harness.resume(new_boot("d"))
    self.assertEqual(result.qualification, Qualification.QUALIFIED_PASS)

def test_any_failure_still_runs_owned_finalizer_and_remains_fail(self):
    self.host.fail_at("wifi:restore")
    result = self.harness.run_new(self.config)
    self.assertEqual(result.qualification, Qualification.FAIL)
    self.assertTrue(result.checkpoint_retained)
```

- [ ] **Step 2: Confirm failures**

Run: `python3 -m unittest scripts.acceptance.tests.test_orchestrator scripts.acceptance.tests.test_forbidden_commands -v`
Expected: FAIL.

- [ ] **Step 3: Implement explicit ordered phases and finalizer semantics**

The orchestrator records a scenario failure before cleanup, never promotes failure during cleanup, never retries connect except where the spec explicitly starts a new scenario, and never calls a forbidden command. Every external command has a bounded timeout and private evidence record.

- [ ] **Step 4: Run all deterministic acceptance tests**

Run: `go test ./scripts/acceptance -v`
Expected: PASS; Python suite invoked once by the Go wrapper.

- [ ] **Step 5: Commit**

```bash
git add scripts/acceptance
git commit -m "feat: complete release laptop acceptance orchestration"
```

### Task 14: Operator documentation and complete validation

**Files:**
- Modify: `docs/e2e.md`
- Modify: `docs/release.md`
- Modify: `docs/superpowers/specs/2026-08-26-release-laptop-acceptance-design.md` only if implementation exposed a real contract mismatch.

**Interfaces:**
- Documents the exact commands, prerequisites, disruptive stages, reboot/resume/abort flow, artifact privacy, qualification meanings, and cleanup expectations.

- [ ] **Step 1: Add documentation contract tests before prose changes**

Add Go assertions in `scripts/acceptance/acceptance_test.go` for all public commands and `QUALIFIED_PASS|PARTIAL_PASS|FAIL`, then run:

Run: `go test ./scripts/acceptance -run TestReleaseLaptopDocumentationContract -v`
Expected: FAIL until docs are updated.

- [ ] **Step 2: Document canonical and optional invocations**

```text
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb --profile <profile-id>
sudo ./scripts/acceptance/release-laptop.sh --resume
sudo ./scripts/acceptance/release-laptop.sh --abort
```

State explicitly that the harness is laptop-disruptive, requires three user-controlled reboots for full qualification, never uploads artifacts, never repairs dependencies/networking, and leaves the candidate installed.

- [ ] **Step 3: Run documentation and deterministic tests**

Run: `go test ./scripts/acceptance ./scripts/e2e -v`
Expected: PASS.

- [ ] **Step 4: Run repository validation ladder**

Run:

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
shellcheck scripts/acceptance/release-laptop.sh
python3 -m compileall -q scripts/acceptance/release_acceptance scripts/acceptance/tests
```

Expected: every command exits 0. If `govulncheck` is unavailable, do not install it; record that exact limitation in the PR.

- [ ] **Step 5: Perform final static safety scans**

Run:

```bash
rg -n 'apt(-get)? (update|upgrade|install)|--fix-broken|ip (route|rule) flush|nft flush ruleset|recover --execute|systemctl restart (NetworkManager|systemd-resolved)' scripts/acceptance
rg -n 'example\.com|192\.0\.2\.|198\.51\.100\.|203\.0\.113\.' scripts/acceptance docs/e2e.md docs/release.md
```

Expected: first scan has no executable forbidden command; any matches are test assertions/docs explaining the prohibition. Second scan contains only RFC documentation addresses/names and no real user identifiers.

- [ ] **Step 6: Commit**

```bash
git add docs scripts/acceptance
git commit -m "docs: document laptop release qualification"
```

### Task 15: PR evidence and ready-for-review gate

**Files:**
- Modify: PR #272 body and review-thread replies through GitHub.

- [ ] **Step 1: Rebase/check current master changes without discarding branch work**

Run: `git fetch origin master && git merge-base --is-ancestor origin/master HEAD`
Expected: inspect result; rebase only through the repository's normal non-destructive workflow if needed.

- [ ] **Step 2: Re-run the full validation ladder on the final head**

Run the exact commands from Task 14 Step 4 and retain command/exit summaries.

- [ ] **Step 3: Review the final diff against every spec success criterion**

Check each spec bullet maps to implementation plus a deterministic test or an explicitly real-host-only observation. Do not claim a real laptop PASS without actual harness artifacts.

- [ ] **Step 4: Update PR body**

Include summary, architecture, exact safety/rollback behavior, deterministic checks run, real-host limitations, documentation changes, and the statement that full Wi-Fi/suspend/root-network/package/reboot evidence is produced by a maintainer run rather than hosted CI.

- [ ] **Step 5: Request review and mark ready only after checks pass**

Do not merge. Keep the branch reviewable and resolve threads only when the corresponding code and tests are present.

## Plan Self-Review Result

- Spec coverage: package/provenance, upgrade/reinstall, lifecycle variants, privacy proof, crash-atomic ledger, dynamic profile, artifacts/redaction, fixtures A/B, resources, soak, Wi-Fi/suspend, terminal behavior, three reboot phases, abort/final restoration, qualification/reporting, docs, and validation each map to an explicit task.
- Placeholder scan: every task contains concrete files, interfaces, commands, expected outcomes, and implementation direction; no deferred or referential-only step remains.
- Interface consistency: all host effects flow through `CommandRunner`; persistent effects flow through `MutationLedger`; phase-level code consumes typed `ProductSnapshot`, `PrivacyVerdict`, `ResourceSummary`, and `ScenarioOutcome`; the orchestrator alone evaluates/serializes the final result.
