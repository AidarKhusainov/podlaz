from __future__ import annotations

from pathlib import Path
import uuid

from .artifacts import ArtifactStore
from .checkpoint import CheckpointStore, MutationLedger
from .command import CommandRunner
from .fixtures import FixtureLease, FIXTURE_A, FIXTURE_B
from .host_events import SuspendEvent, WifiLease
from .lifecycle import LifecycleScenarios
from .model import (
    AcceptanceError,
    AmbiguousState,
    Checkpoint,
    MutationState,
    Qualification,
    RunConfig,
    RunMode,
    ScenarioOutcome,
    ScenarioRecord,
    UserIdentity,
)
from .packages import PackageInspector
from .persistent import capture_boot_manifest
from .privacy import PrivacyObserver
from .product import ProductClient, RuntimeState
from .reboot import PHASE_COMPLETE, RebootCoordinator
from .report import QualificationEvaluator, ReportWriter
from .resources import ResourceSampler
from .soak import SoakScheduler


PHASE_PREPARING_LOWER = "preparing-lower-release"
PHASE_RUNNING_PRE_REBOOT = "running-pre-reboot"


class ReleaseAcceptance:
    def __init__(self, user: UserIdentity, checkpoint_path: Path):
        self.user = user
        self.checkpoint_store = CheckpointStore(checkpoint_path, user)

    def run_new(self, config: RunConfig) -> Qualification | None:
        if self.checkpoint_store.exists():
            raise AmbiguousState(
                "an unfinished release acceptance checkpoint already exists; use --resume or --abort"
            )
        if config.candidate is None:
            raise AcceptanceError("candidate .deb is required")

        bootstrap_runner = CommandRunner()
        bootstrap_packages = PackageInspector(bootstrap_runner)
        candidate = bootstrap_packages.inspect(config.candidate)
        previous = (
            bootstrap_packages.inspect(config.previous_deb)
            if config.previous_deb
            else None
        )
        installed_before = bootstrap_packages.installed_version()
        run_id = uuid.uuid4().hex
        artifact_root = config.artifact_dir or (
            self.user.state_home / "podlaz" / "release-acceptance" / run_id
        )
        artifacts = ArtifactStore.create(artifact_root, self.user)
        runner = CommandRunner(evidence=artifacts)
        packages = PackageInspector(runner)
        product = ProductClient(runner, self.user)
        self._require_initial_inactive(runner)

        checkpoint = Checkpoint(
            schema_version="podlaz.release-acceptance-checkpoint.v1",
            run_id=run_id,
            phase=PHASE_PREPARING_LOWER,
            user={
                "name": self.user.name,
                "uid": self.user.uid,
                "gid": self.user.gid,
                "home": str(self.user.home),
            },
            candidate={
                "path": str(candidate.path),
                "package": candidate.package,
                "version": candidate.version,
                "architecture": candidate.architecture,
                "sha256": candidate.sha256,
                "device": candidate.device,
                "inode": candidate.inode,
            },
            previous_boot_id=RuntimeState.boot_id(),
            original_autostart=capture_boot_manifest(),
            private={
                "artifact_root": str(artifact_root),
                "installed_before": installed_before,
                "run_config": self._serialize_run_config(config),
                "soak_minutes": config.soak_minutes,
                "user_forced_partial": (not config.reboot_phases),
            },
        )
        self.checkpoint_store.replace(checkpoint)

        try:
            ledger = MutationLedger(self.checkpoint_store)
            self._prepare_lower_release(
                packages, candidate, previous, installed_before, ledger
            )
            self._set_phase(PHASE_RUNNING_PRE_REBOOT)
            return self._run_pre_reboot(
                config, candidate, artifacts, runner, packages, product
            )
        except Exception as error:
            self._record_failure("harness_runtime", error)
            self._best_effort_safe_finalizer(product)
            self._write_current_report(artifacts)
            raise

    def resume(self) -> Qualification | None:
        checkpoint = self.checkpoint_store.load()
        artifacts = ArtifactStore.create(
            Path(checkpoint.private["artifact_root"]), self.user
        )
        runner = CommandRunner(evidence=artifacts)
        product = ProductClient(runner, self.user)

        if checkpoint.phase == PHASE_PREPARING_LOWER:
            packages = PackageInspector(runner)
            config = self._deserialize_run_config(checkpoint)
            if config.candidate is None:
                raise AmbiguousState("persisted run configuration lost candidate path")
            candidate = packages.inspect(config.candidate)
            self._require_recorded_candidate(checkpoint, candidate)
            previous = (
                packages.inspect(config.previous_deb) if config.previous_deb else None
            )
            ledger = MutationLedger(self.checkpoint_store)
            try:
                record = checkpoint.mutations.get("package_setup")
                ready = False
                if record is not None and record.state != MutationState.RELEASED:
                    if previous is None:
                        raise AmbiguousState(
                            "package_setup authority exists without persisted previous package"
                        )
                    ready = self._reconcile_package_setup_for_resume(
                        packages, candidate, previous, ledger
                    )
                if not ready:
                    self._prepare_lower_release(
                        packages,
                        candidate,
                        previous,
                        checkpoint.private.get("installed_before"),
                        ledger,
                    )
                self._set_phase(PHASE_RUNNING_PRE_REBOOT)
                return self._run_pre_reboot(
                    config, candidate, artifacts, runner, packages, product
                )
            except Exception as error:
                self._record_failure("harness_runtime", error)
                self._best_effort_safe_finalizer(product)
                self._write_current_report(artifacts)
                raise

        if checkpoint.phase == PHASE_RUNNING_PRE_REBOOT:
            raise AmbiguousState(
                "pre-reboot acceptance execution was interrupted after lower-package setup; "
                "exact scenario continuation is not provable, use --abort"
            )

        reboot = RebootCoordinator(self.checkpoint_store, runner, product)
        phase = reboot.resume()
        if phase != PHASE_COMPLETE:
            return None
        reboot.restore_original_policy()
        self._record("final_restoration", ScenarioOutcome.PASS)
        return self._finalize(artifacts)

    def _run_pre_reboot(
        self,
        config: RunConfig,
        candidate,
        artifacts: ArtifactStore,
        runner: CommandRunner,
        packages: PackageInspector,
        product: ProductClient,
    ) -> Qualification | None:
        ledger = MutationLedger(self.checkpoint_store)
        privacy = PrivacyObserver(runner)
        lifecycle = LifecycleScenarios(runner, product, packages, ledger, privacy)
        fixture_a = FixtureLease(runner, ledger, FIXTURE_A)
        fixture_b = FixtureLease(runner, ledger, FIXTURE_B)
        soak = SoakScheduler(
            product=product,
            lifecycle=lifecycle,
            privacy=privacy,
            sampler=ResourceSampler(runner),
            fixture_b=fixture_b,
            wifi=WifiLease(runner, ledger),
            suspend=SuspendEvent(runner),
        )

        selected_profile = product.select_profile(config.profile)
        checkpoint = self.checkpoint_store.load()
        checkpoint.private["selected_profile"] = selected_profile
        self.checkpoint_store.replace(checkpoint)

        privacy.baseline()
        fixture_a.acquire()
        self._record("preconnect_collision", ScenarioOutcome.PASS)

        product.connect_tun(selected_profile)
        lifecycle.wait_verified_active()
        if packages.installed_version() != candidate.version:
            lifecycle.install_candidate_over_active_lower(candidate)
        self._release_package_setup_if_restored(packages, candidate, ledger)
        self._record("lower_release_upgrade_continuity", ScenarioOutcome.PASS)
        self._require_privacy(privacy, "candidate_protected_data_plane")
        self._record("candidate_protected_data_plane", ScenarioOutcome.PASS)

        lifecycle.graceful_restart()
        self._record("graceful_daemon_restart", ScenarioOutcome.PASS)
        lifecycle.unexpected_daemon_death()
        self._record("unexpected_daemon_death", ScenarioOutcome.PASS)

        lifecycle.durable_rollback_interruption(
            Path("/run/podlaz/release-acceptance-rollback")
        )
        self._record("durable_rollback_interruption", ScenarioOutcome.PASS)

        lifecycle.explicit_stop_start()
        self._record("explicit_stop_start_no_reconnect", ScenarioOutcome.PASS)
        product.connect_tun(selected_profile)
        lifecycle.wait_verified_active()
        lifecycle.reinstall_candidate(candidate)
        self._record("candidate_reinstall_continuity", ScenarioOutcome.PASS)
        self._require_privacy(privacy, "privacy_candidate_windows")
        self._record("privacy_candidate_windows", ScenarioOutcome.PASS)

        soak_result = soak.run(
            config.soak_minutes,
            allow_wifi=config.allow_wifi_reconnect,
            allow_suspend=config.allow_suspend,
        )
        checkpoint = self.checkpoint_store.load()
        checkpoint.private["soak_measured_seconds"] = soak_result.measured_seconds
        checkpoint.private["resource_summary"] = soak_result.resource_summary
        if soak_result.forces_partial:
            checkpoint.private["user_forced_partial"] = True
        self.checkpoint_store.replace(checkpoint)
        self._record(
            "resource_soak",
            ScenarioOutcome.PASS,
            evidence={
                "measured_seconds": soak_result.measured_seconds,
                "events": list(soak_result.events),
                "wifi_outcome": soak_result.wifi_outcome,
                "suspend_outcome": soak_result.suspend_outcome,
            },
        )

        product.disconnect()
        lifecycle.wait_inactive()
        if privacy.observe_ordinary().outcome != ScenarioOutcome.PASS:
            raise AmbiguousState("ordinary network was not restored after disconnect")
        fixture_a.verify()
        fixture_a.release()
        self._record("disconnect_cleanup", ScenarioOutcome.PASS)

        product.connect_tun(selected_profile)
        lifecycle.wait_verified_active()
        self._runtime_terminal_failure(runner, lifecycle, ledger, privacy)
        self._record("runtime_terminal_convergence", ScenarioOutcome.PASS)

        if not config.reboot_phases:
            for name in (
                "reboot_autostart_off",
                "reboot_autostart_on",
                "explicit_disconnect_no_same_boot_retry",
                "reboot_terminal_autostart",
                "terminal_no_same_boot_retry",
            ):
                self._record(name, ScenarioOutcome.SKIP_USER_REQUEST)
            return self._finish_without_reboots(product, artifacts)

        RebootCoordinator(
            self.checkpoint_store, runner, product
        ).prepare_autostart_off()
        return None

    def abort(self) -> str:
        checkpoint = self.checkpoint_store.load()
        artifacts = ArtifactStore.create(
            Path(checkpoint.private["artifact_root"]), self.user
        )
        runner = CommandRunner(evidence=artifacts)
        product = ProductClient(runner, self.user)
        packages = PackageInspector(runner)
        ledger = MutationLedger(self.checkpoint_store)
        try:
            candidate = packages.inspect(Path(checkpoint.candidate["path"]))
            self._require_recorded_candidate(checkpoint, candidate)
            package_setup = checkpoint.mutations.get("package_setup")
            if package_setup is not None and package_setup.state != MutationState.RELEASED:
                previous = packages.inspect(Path(package_setup.identity["previous_path"]))
                self._reconcile_package_setup(
                    packages, candidate, previous, ledger
                )
            elif packages.installed_version() != candidate.version:
                packages.install_exact(candidate)
            try:
                product.disconnect()
            except AcceptanceError:
                pass
            self._cleanup_owned_mutations(runner)
            self._require_all_mutations_released()
            RebootCoordinator(
                self.checkpoint_store, runner, product
            ).restore_original_policy()
            checkpoint = self.checkpoint_store.load()
            checkpoint.phase = "aborted-clean"
            checkpoint.scenarios["final_restoration"] = ScenarioRecord(
                "final_restoration", ScenarioOutcome.PASS
            )
            self.checkpoint_store.replace(checkpoint)
            self._write_current_report(artifacts)
            self.checkpoint_store.remove()
            return "ABORTED_CLEAN"
        except Exception as error:
            checkpoint = self.checkpoint_store.load()
            checkpoint.private["cleanup_failed"] = True
            checkpoint.scenarios["final_restoration"] = ScenarioRecord(
                "final_restoration", ScenarioOutcome.FAIL, str(error)
            )
            self.checkpoint_store.replace(checkpoint)
            self._write_current_report(artifacts)
            return "ABORT_CLEANUP_FAILED"

    def _prepare_lower_release(
        self, packages, candidate, previous, installed_before, ledger
    ) -> None:
        if installed_before and installed_before != candidate.version:
            if packages.installed_version() != installed_before:
                raise AmbiguousState(
                    "installed lower release changed before lower-release setup"
                )
            if packages.runner.run(
                (
                    "dpkg",
                    "--compare-versions",
                    installed_before,
                    "lt",
                    candidate.version,
                ),
                timeout=5,
            ).returncode != 0:
                raise AmbiguousState("installed Podlaz is not strictly lower than candidate")
            return
        if previous is None:
            raise AmbiguousState(
                "full lower-release qualification requires an installed lower release or --previous-deb"
            )
        if not packages.compare_lt(previous, candidate):
            raise AmbiguousState("--previous-deb is not strictly lower than candidate")
        ledger.begin_acquire(
            "package_setup",
            "previous_package",
            {
                "previous_path": str(previous.path),
                "previous_package": previous.package,
                "previous_version": previous.version,
                "previous_architecture": previous.architecture,
                "previous_sha256": previous.sha256,
                "previous_device": previous.device,
                "previous_inode": previous.inode,
                "candidate_path": str(candidate.path),
                "candidate_package": candidate.package,
                "candidate_version": candidate.version,
                "candidate_architecture": candidate.architecture,
                "candidate_sha256": candidate.sha256,
                "candidate_device": candidate.device,
                "candidate_inode": candidate.inode,
            },
        )
        packages.install_exact(previous)
        ledger.mark_acquired("package_setup")

    def _validate_package_setup_identity(self, record, candidate, previous) -> None:
        if record.kind != "previous_package":
            raise AmbiguousState("package_setup has unexpected mutation kind")
        expected = record.identity
        actual = {
            "previous_path": str(previous.path),
            "previous_package": previous.package,
            "previous_version": previous.version,
            "previous_architecture": previous.architecture,
            "previous_sha256": previous.sha256,
            "previous_device": previous.device,
            "previous_inode": previous.inode,
            "candidate_path": str(candidate.path),
            "candidate_package": candidate.package,
            "candidate_version": candidate.version,
            "candidate_architecture": candidate.architecture,
            "candidate_sha256": candidate.sha256,
            "candidate_device": candidate.device,
            "candidate_inode": candidate.inode,
        }
        if any(expected.get(key) != value for key, value in actual.items()):
            raise AmbiguousState("package_setup package identity changed")

    def _reconcile_package_setup_for_resume(
        self, packages, candidate, previous, ledger: MutationLedger
    ) -> bool:
        checkpoint = self.checkpoint_store.load()
        record = checkpoint.mutations.get("package_setup")
        if record is None or record.state == MutationState.RELEASED:
            return False
        self._validate_package_setup_identity(record, candidate, previous)
        installed = packages.installed_version()

        if installed == previous.version:
            if record.state == MutationState.ACQUIRING:
                ledger.mark_acquired("package_setup")
                return True
            if record.state == MutationState.ACQUIRED:
                return True
            if record.state == MutationState.RELEASING:
                self._reconcile_package_setup(
                    packages, candidate, previous, ledger
                )
                return False
            raise AmbiguousState(
                f"unsupported package_setup state: {record.state.value}"
            )

        if installed == candidate.version:
            if record.state == MutationState.ACQUIRED:
                ledger.begin_release("package_setup")
                ledger.mark_released("package_setup")
            elif record.state in {MutationState.ACQUIRING, MutationState.RELEASING}:
                ledger.mark_released("package_setup")
            else:
                raise AmbiguousState(
                    f"unsupported package_setup state: {record.state.value}"
                )
            return False

        raise AmbiguousState(
            "package_setup installed version is neither exact previous nor candidate"
        )

    def _reconcile_package_setup(
        self, packages, candidate, previous, ledger: MutationLedger
    ) -> None:
        checkpoint = self.checkpoint_store.load()
        record = checkpoint.mutations.get("package_setup")
        if record is None or record.state == MutationState.RELEASED:
            return
        self._validate_package_setup_identity(record, candidate, previous)

        installed = packages.installed_version()
        if installed == candidate.version:
            if record.state == MutationState.ACQUIRED:
                ledger.begin_release("package_setup")
                ledger.mark_released("package_setup")
            elif record.state in {MutationState.ACQUIRING, MutationState.RELEASING}:
                ledger.mark_released("package_setup")
            return
        if installed != previous.version:
            raise AmbiguousState(
                "package_setup installed version is neither exact previous nor candidate"
            )

        if record.state == MutationState.ACQUIRING:
            ledger.mark_acquired("package_setup")
            record = self.checkpoint_store.load().mutations["package_setup"]
        if record.state == MutationState.ACQUIRED:
            ledger.begin_release("package_setup")
        elif record.state != MutationState.RELEASING:
            raise AmbiguousState(
                f"unsupported package_setup state: {record.state.value}"
            )
        packages.install_exact(candidate)
        if packages.installed_version() != candidate.version:
            raise AmbiguousState(
                "candidate package did not converge during package_setup reconciliation"
            )
        ledger.mark_released("package_setup")

    def _release_package_setup_if_restored(
        self, packages, candidate, ledger: MutationLedger
    ) -> None:
        checkpoint = self.checkpoint_store.load()
        record = checkpoint.mutations.get("package_setup")
        if record is None or record.state == MutationState.RELEASED:
            return
        if packages.installed_version() != candidate.version:
            raise AmbiguousState(
                "candidate is not restored while package_setup authority remains"
            )
        if record.state == MutationState.ACQUIRED:
            ledger.begin_release("package_setup")
            ledger.mark_released("package_setup")
        elif record.state == MutationState.RELEASING:
            ledger.mark_released("package_setup")
        elif record.state == MutationState.ACQUIRING:
            ledger.mark_released("package_setup")
        else:
            raise AmbiguousState(
                f"unsupported package_setup state: {record.state.value}"
            )

    def _runtime_terminal_failure(self, runner, lifecycle, ledger, privacy) -> None:
        hook_dir = Path("/run/podlaz/release-acceptance-terminal")
        override = Path(
            "/etc/systemd/system/podlazd.service.d/99-release-acceptance-terminal.conf"
        )
        ledger.begin_acquire(
            "terminal_hook",
            "systemd_dropin",
            {"path": str(override), "hook_dir": str(hook_dir)},
        )
        hook_dir.mkdir(parents=True, mode=0o700, exist_ok=True)
        override.parent.mkdir(parents=True, exist_ok=True)
        override.write_text(
            "[Service]\n"
            "Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE=true\n"
            f"Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE_DIR={hook_dir}\n"
            "Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE=true\n"
            f"Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_DIR={hook_dir}\n"
            "Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_TIMEOUT_SECONDS=180\n",
            encoding="utf-8",
        )
        runner.run(
            ("systemctl", "daemon-reload"), timeout=30
        ).require_success("daemon-reload terminal hook")
        ledger.mark_acquired("terminal_hook")
        runner.run(
            ("systemctl", "restart", "podlazd.service"), timeout=90
        ).require_success("activate terminal hook")
        lifecycle.wait_verified_active()
        (hook_dir / "terminal-failure.trigger").write_text(
            "trigger\n", encoding="utf-8"
        )
        lifecycle._wait_file(hook_dir / "terminal-data-plane-clean.ready", 90)
        self._require_privacy(privacy, "runtime_terminal_privacy_window")
        (hook_dir / "terminal-data-plane-clean.continue").write_text(
            "continue\n", encoding="utf-8"
        )
        lifecycle.wait_inactive(timeout=120)
        lifecycle.release_systemd_hook("terminal_hook", override, hook_dir)
        if privacy.observe_ordinary().outcome != ScenarioOutcome.PASS:
            raise AmbiguousState(
                "ordinary network did not recover after terminal convergence"
            )

    def _finish_without_reboots(
        self, product: ProductClient, artifacts: ArtifactStore
    ) -> Qualification:
        RebootCoordinator(
            self.checkpoint_store, CommandRunner(evidence=artifacts), product
        ).restore_original_policy()
        self._record("final_restoration", ScenarioOutcome.PASS)
        return self._finalize(artifacts)

    def _finalize(self, artifacts: ArtifactStore) -> Qualification:
        checkpoint = self.checkpoint_store.load()
        checkpoint.phase = "complete"
        self.checkpoint_store.replace(checkpoint)
        qualification = QualificationEvaluator().evaluate(checkpoint)
        ReportWriter(artifacts).write(checkpoint, qualification)
        if qualification != Qualification.FAIL:
            self.checkpoint_store.remove()
        return qualification

    def _write_current_report(self, artifacts: ArtifactStore) -> None:
        checkpoint = self.checkpoint_store.load()
        ReportWriter(artifacts).write(
            checkpoint, QualificationEvaluator().evaluate(checkpoint)
        )

    def _record(
        self,
        name: str,
        outcome: ScenarioOutcome,
        *,
        reason: str = "",
        evidence: dict | None = None,
    ) -> None:
        checkpoint = self.checkpoint_store.load()
        checkpoint.scenarios[name] = ScenarioRecord(
            name, outcome, reason, evidence or {}
        )
        self.checkpoint_store.replace(checkpoint)

    def _record_failure(self, name: str, error: Exception) -> None:
        try:
            self._record(name, ScenarioOutcome.FAIL, reason=str(error))
        except Exception:
            pass

    def _set_phase(self, phase: str) -> None:
        checkpoint = self.checkpoint_store.load()
        checkpoint.phase = phase
        self.checkpoint_store.replace(checkpoint)

    def _require_recorded_candidate(self, checkpoint: Checkpoint, candidate) -> None:
        expected = checkpoint.candidate
        if (
            str(candidate.path) != expected.get("path")
            or candidate.package != expected.get("package", "podlaz")
            or candidate.version != expected.get("version")
            or candidate.architecture != expected.get("architecture")
            or candidate.sha256 != expected.get("sha256")
            or candidate.device != expected.get("device")
            or candidate.inode != expected.get("inode")
        ):
            raise AmbiguousState("candidate package identity changed since checkpoint")

    @staticmethod
    def _serialize_run_config(config: RunConfig) -> dict:
        return {
            "candidate": str(config.candidate) if config.candidate else None,
            "previous_deb": str(config.previous_deb) if config.previous_deb else None,
            "profile": config.profile,
            "soak_minutes": config.soak_minutes,
            "allow_wifi_reconnect": config.allow_wifi_reconnect,
            "allow_suspend": config.allow_suspend,
            "reboot_phases": config.reboot_phases,
        }

    @staticmethod
    def _deserialize_run_config(checkpoint: Checkpoint) -> RunConfig:
        raw = checkpoint.private.get("run_config")
        if not isinstance(raw, dict):
            raise AmbiguousState("checkpoint has no resumable run configuration")
        candidate = raw.get("candidate")
        previous = raw.get("previous_deb")
        return RunConfig(
            mode=RunMode.RESUME,
            candidate=Path(candidate) if candidate else None,
            previous_deb=Path(previous) if previous else None,
            profile=raw.get("profile"),
            soak_minutes=int(raw.get("soak_minutes", 60)),
            allow_wifi_reconnect=bool(raw.get("allow_wifi_reconnect", True)),
            allow_suspend=bool(raw.get("allow_suspend", True)),
            reboot_phases=bool(raw.get("reboot_phases", True)),
        )

    def _require_all_mutations_released(self) -> None:
        checkpoint = self.checkpoint_store.load()
        pending = [
            name
            for name, record in checkpoint.mutations.items()
            if record.state != MutationState.RELEASED
        ]
        if pending:
            raise AmbiguousState(
                "owned mutation cleanup is incomplete: " + ", ".join(sorted(pending))
            )

    @staticmethod
    def _require_privacy(privacy: PrivacyObserver, name: str) -> None:
        verdict = privacy.observe_protected()
        if verdict.outcome != ScenarioOutcome.PASS:
            raise AmbiguousState(f"{name}: {verdict.reason}")

    @staticmethod
    def _require_initial_inactive(runner: CommandRunner) -> None:
        result = runner.run(
            (
                "curl",
                "--fail",
                "--silent",
                "--max-time",
                "5",
                "--unix-socket",
                "/run/podlaz/podlazd.sock",
                "http://localhost/v1/status",
            ),
            timeout=7,
        )
        if result.returncode != 0:
            raise AmbiguousState("podlazd status unavailable at initial boundary")
        import json

        payload = json.loads(result.stdout)
        if payload.get("connection") != "inactive" or payload.get("tun") != "disabled":
            raise AmbiguousState(
                "initial Podlaz state must be conclusively disconnected"
            )

    def _cleanup_owned_mutations(self, runner: CommandRunner) -> None:
        ledger = MutationLedger(self.checkpoint_store)
        checkpoint = self.checkpoint_store.load()
        for name, record in reversed(list(checkpoint.mutations.items())):
            if record.state == MutationState.RELEASED or record.kind == "previous_package":
                continue
            if record.kind == "systemd_dropin":
                if record.state == MutationState.ACQUIRED:
                    ledger.begin_release(name)
                path = Path(record.identity["path"])
                hook_dir = Path(record.identity["hook_dir"])
                path.unlink(missing_ok=True)
                runner.run(
                    ("systemctl", "daemon-reload"), timeout=30
                ).require_success("abort daemon-reload")
                if hook_dir.exists():
                    for child in hook_dir.iterdir():
                        if child.is_symlink() or not child.is_file():
                            raise AmbiguousState(
                                f"ambiguous hook cleanup entry: {child.name}"
                            )
                        child.unlink()
                    hook_dir.rmdir()
                state = self.checkpoint_store.load().mutations[name].state
                if state in {MutationState.ACQUIRING, MutationState.RELEASING}:
                    ledger.mark_released(name)
            elif record.kind == "network_fixture":
                spec = FIXTURE_A if name == "fixture_a" else FIXTURE_B
                FixtureLease(runner, ledger, spec).release()
            elif record.kind == "networkmanager_connection":
                if record.state == MutationState.ACQUIRED:
                    ledger.begin_release(name)
                connection = str(record.identity.get("connection") or "")
                if not connection:
                    raise AmbiguousState(
                        "NetworkManager mutation lost exact connection identity"
                    )
                runner.run(
                    ("nmcli", "connection", "up", connection), timeout=60
                ).require_success("restore exact NetworkManager connection")
                state = self.checkpoint_store.load().mutations[name].state
                if state in {MutationState.ACQUIRING, MutationState.RELEASING}:
                    ledger.mark_released(name)
            else:
                raise AmbiguousState(
                    f"unsupported owned mutation kind during cleanup: {record.kind}"
                )
            checkpoint = self.checkpoint_store.load()

    @staticmethod
    def _best_effort_safe_finalizer(product: ProductClient) -> None:
        try:
            product.disconnect()
        except Exception:
            pass
