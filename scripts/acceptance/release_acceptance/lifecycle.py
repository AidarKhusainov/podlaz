from __future__ import annotations

import json
from pathlib import Path
import time

from .checkpoint import MutationLedger
from .command import CommandRunner
from .model import AmbiguousState, DebIdentity
from .packages import PackageInspector
from .privacy import PrivacyObserver
from .product import ProductClient, RuntimeState


class LifecycleScenarios:
    def __init__(
        self,
        runner: CommandRunner,
        product: ProductClient,
        packages: PackageInspector,
        ledger: MutationLedger,
        privacy: PrivacyObserver,
    ):
        self.runner = runner
        self.product = product
        self.packages = packages
        self.ledger = ledger
        self.privacy = privacy

    def install_candidate_over_active_lower(self, candidate: DebIdentity) -> None:
        previous_pid = self.main_pid()
        self.packages.install_exact(candidate)
        self.wait_service_active()
        current_pid = self.main_pid()
        if current_pid == previous_pid:
            raise AmbiguousState("candidate package did not replace podlazd MainPID")
        self.wait_verified_active()
        self._require_privacy()

    def graceful_restart(self) -> None:
        before = self.main_pid()
        self.runner.run(("systemctl", "restart", "podlazd.service"), timeout=90).require_success("restart podlazd")
        self.wait_new_main_pid(before)
        self.wait_verified_active()
        self._require_privacy()

    def unexpected_daemon_death(self) -> None:
        before = self.main_pid()
        self.runner.run(
            ("systemctl", "kill", "--kill-who=main", "--signal=SIGKILL", "podlazd.service"),
            timeout=10,
        ).require_success("SIGKILL podlazd main")
        self.wait_new_main_pid(before)
        self.wait_verified_active()
        self._require_privacy()

    def explicit_stop_start(self) -> None:
        self.runner.run(("systemctl", "stop", "podlazd.service"), timeout=90).require_success("stop podlazd")
        self.runner.run(("systemctl", "start", "podlazd.service"), timeout=90).require_success("start podlazd")
        self.wait_service_active()
        self.wait_inactive()

    def reinstall_candidate(self, candidate: DebIdentity) -> None:
        before = self.main_pid()
        self.packages.install_exact(candidate)
        self.wait_new_main_pid(before)
        self.wait_verified_active()
        self._require_privacy()

    def durable_rollback_interruption(self, hook_dir: Path) -> None:
        override = Path("/etc/systemd/system/podlazd.service.d/99-release-acceptance-rollback.conf")
        identity = {"path": str(override), "hook_dir": str(hook_dir)}
        self.ledger.begin_acquire("rollback_hook", "systemd_dropin", identity)
        hook_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
        override.parent.mkdir(parents=True, exist_ok=True)
        override.write_text(
            "[Service]\n"
            "Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE=true\n"
            f"Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR={hook_dir}\n"
            "Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_TIMEOUT_SECONDS=180\n",
            encoding="utf-8",
        )
        self.runner.run(("systemctl", "daemon-reload"), timeout=30).require_success("daemon-reload rollback hook")
        self.ledger.mark_acquired("rollback_hook")

        # First restart activates the release-built hook while preserving the active session.
        self.runner.run(("systemctl", "restart", "podlazd.service"), timeout=90).require_success("activate rollback hook")
        self.wait_verified_active()

        for marker in ("rollback-pause.ready", "rollback-pause.continue"):
            (hook_dir / marker).unlink(missing_ok=True)
        (hook_dir / "rollback-pause.arm").write_text("armed\n", encoding="utf-8")

        old_pid = self.main_pid()
        # The restart must remain in flight while the daemon is paused after durable
        # rolling_back authority was committed. A synchronous call would miss this seam.
        restart = self.runner.start(("systemctl", "restart", "podlazd.service"))
        self._wait_file(hook_dir / "rollback-pause.ready", 60)
        self._require_exact_rolling_back_authority()
        self.runner.run(("kill", "-KILL", str(old_pid)), timeout=5).require_success("interrupt rolling_back daemon")

        # The client may observe a failed stop job because the exact MainPID was killed.
        # That result is evidence only; no reset-failed/start repair is allowed here.
        restart.wait(timeout=30)
        self.wait_new_main_pid(old_pid, timeout=120)
        self.wait_verified_active(timeout=180)
        self._require_privacy()
        self.release_systemd_hook("rollback_hook", override, hook_dir)

    def release_systemd_hook(self, name: str, override: Path, hook_dir: Path) -> None:
        self.ledger.begin_release(name)
        override.unlink(missing_ok=True)
        self.runner.run(("systemctl", "daemon-reload"), timeout=30).require_success("daemon-reload after hook removal")
        if hook_dir.exists():
            for child in hook_dir.iterdir():
                if child.is_symlink() or not child.is_file():
                    raise AmbiguousState(f"unexpected hook-dir entry during cleanup: {child.name}")
                child.unlink()
            hook_dir.rmdir()
        self.ledger.mark_released(name)

    def main_pid(self) -> int:
        result = self.runner.run(
            ("systemctl", "show", "-p", "MainPID", "--value", "podlazd.service"), timeout=5
        ).require_success("read podlazd MainPID")
        try:
            pid = int(result.stdout.strip())
        except ValueError as error:
            raise AmbiguousState("invalid podlazd MainPID") from error
        if pid <= 1:
            raise AmbiguousState("podlazd MainPID is not active")
        return pid

    def wait_new_main_pid(self, old: int, timeout: float = 60) -> int:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                current = self.main_pid()
            except AmbiguousState:
                time.sleep(0.2)
                continue
            if current != old:
                return current
            time.sleep(0.2)
        raise AmbiguousState("podlazd MainPID did not change")

    def wait_service_active(self, timeout: float = 60) -> None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.runner.run(("systemctl", "is-active", "--quiet", "podlazd.service"), timeout=5).returncode == 0:
                return
            time.sleep(0.2)
        raise AmbiguousState("podlazd.service did not become active")

    def wait_verified_active(self, timeout: float = 120) -> None:
        self._wait_daemon_status("active", "active", require_verified=True, timeout=timeout)

    def wait_inactive(self, timeout: float = 90) -> None:
        self._wait_daemon_status("inactive", "disabled", require_verified=False, timeout=timeout)

    def _wait_daemon_status(self, connection: str, tun: str, *, require_verified: bool, timeout: float) -> None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            result = self.runner.run(
                (
                    "curl", "--fail", "--silent", "--max-time", "5",
                    "--unix-socket", "/run/podlaz/podlazd.sock", "http://localhost/v1/status",
                ),
                timeout=7,
            )
            if result.returncode == 0:
                try:
                    payload = json.loads(result.stdout)
                except json.JSONDecodeError:
                    payload = {}
                health = payload.get("tun_health") or {}
                if (
                    payload.get("connection") == connection
                    and payload.get("tun") == tun
                    and (not require_verified or health.get("state") == "verified")
                ):
                    return
            time.sleep(0.2)
        raise AmbiguousState(f"daemon status did not converge to {connection}/{tun}")

    def _require_privacy(self) -> None:
        verdict = self.privacy.observe_protected()
        if verdict.outcome.value != "PASS":
            raise AmbiguousState(verdict.reason)

    def _require_exact_rolling_back_authority(self) -> None:
        matches = 0
        for path in RuntimeState.TRANSACTIONS.glob("*.json"):
            try:
                tx = json.loads(path.read_text(encoding="utf-8"))
            except Exception:
                continue
            if tx.get("owner") != "podlaz" or tx.get("state") != "rolling_back":
                continue
            rollback = tx.get("rollback") or {}
            if any(
                any(isinstance(item, dict) and item.get("owner") == "podlaz" for item in rollback.get(key) or [])
                for key in (
                    "tun_addresses", "routes", "policy_rules", "dns", "nftables",
                    "generated_configs", "child_processes",
                )
            ):
                matches += 1
        if matches != 1:
            raise AmbiguousState(f"expected one exact rolling_back authority, found {matches}")

    @staticmethod
    def _wait_file(path: Path, timeout: float) -> None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if path.is_file() and not path.is_symlink():
                return
            time.sleep(0.1)
        raise AmbiguousState(f"expected marker did not appear: {path.name}")
