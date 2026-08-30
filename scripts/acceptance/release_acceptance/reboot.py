from __future__ import annotations

import hashlib
import json
from pathlib import Path

from .checkpoint import CheckpointStore
from .command import CommandRunner
from .model import AmbiguousState, Checkpoint, ScenarioOutcome, ScenarioRecord
from .persistent import restore_boot_manifest
from .product import ProductClient, RuntimeState


PHASE_AUTOSTART_OFF = "awaiting-autostart-off-reboot"
PHASE_AUTOSTART_ON = "awaiting-autostart-on-reboot"
PHASE_TERMINAL = "awaiting-terminal-autostart-reboot"
PHASE_COMPLETE = "reboot-phases-complete"


class RebootCoordinator:
    def __init__(self, store: CheckpointStore, runner: CommandRunner, product: ProductClient):
        self.store = store
        self.runner = runner
        self.product = product

    def prepare_autostart_off(self) -> None:
        checkpoint = self.store.load()
        self.product.autostart_disable()
        checkpoint.phase = PHASE_AUTOSTART_OFF
        checkpoint.previous_boot_id = RuntimeState.boot_id()
        self.store.replace(checkpoint)

    def resume(self) -> str:
        checkpoint = self.store.load()
        current_boot = RuntimeState.boot_id()
        if current_boot == checkpoint.previous_boot_id:
            raise AmbiguousState("--resume requires a real new boot_id")
        self._verify_candidate(checkpoint)
        if checkpoint.phase == PHASE_AUTOSTART_OFF:
            self._resume_autostart_off(checkpoint, current_boot)
            return PHASE_AUTOSTART_ON
        if checkpoint.phase == PHASE_AUTOSTART_ON:
            self._resume_autostart_on(checkpoint, current_boot)
            return PHASE_TERMINAL
        if checkpoint.phase == PHASE_TERMINAL:
            self._resume_terminal(checkpoint, current_boot)
            return PHASE_COMPLETE
        raise AmbiguousState(f"checkpoint is not resumable from phase {checkpoint.phase!r}")

    def _resume_autostart_off(self, checkpoint: Checkpoint, current_boot: str) -> None:
        self._require_inactive()
        checkpoint.scenarios["reboot_autostart_off"] = ScenarioRecord(
            "reboot_autostart_off", ScenarioOutcome.PASS
        )
        profile = checkpoint.private["selected_profile"]
        self.product.autostart_enable(profile)
        checkpoint.phase = PHASE_AUTOSTART_ON
        checkpoint.previous_boot_id = current_boot
        self.store.replace(checkpoint)

    def _resume_autostart_on(self, checkpoint: Checkpoint, current_boot: str) -> None:
        self._require_verified_active()
        attempt = RuntimeState.boot_attempt()
        if (
            attempt.get("schema_version") != "podlaz.boot-autostart-attempt.v1"
            or attempt.get("state") != "succeeded"
        ):
            raise AmbiguousState(
                "successful boot did not persist succeeded autostart authority"
            )
        fingerprint = self._attempt_fingerprint(attempt)
        self.product.disconnect()
        self._require_inactive()
        self.runner.run(
            ("systemctl", "restart", "podlazd.service"), timeout=90
        ).require_success("same-boot no-retry daemon restart")
        self._require_inactive()
        if self._attempt_fingerprint(RuntimeState.boot_attempt()) != fingerprint:
            raise AmbiguousState(
                "explicit disconnect changed consumed boot attempt authority"
            )
        checkpoint.scenarios["reboot_autostart_on"] = ScenarioRecord(
            "reboot_autostart_on", ScenarioOutcome.PASS
        )
        checkpoint.scenarios["explicit_disconnect_no_same_boot_retry"] = ScenarioRecord(
            "explicit_disconnect_no_same_boot_retry", ScenarioOutcome.PASS
        )

        baseline_ids = sorted(self.product.profile_ids())
        checkpoint.private["terminal_profile_acquisition"] = {
            "state": "acquiring",
            "baseline_ids": baseline_ids,
        }
        self.store.replace(checkpoint)

        terminal_profile = self.product.create_terminal_profile()
        if terminal_profile in baseline_ids:
            raise AmbiguousState(
                "terminal profile creation reused a pre-existing profile identity"
            )
        checkpoint = self.store.load()
        checkpoint.private["terminal_profile_acquisition"] = {
            "state": "acquired",
            "baseline_ids": baseline_ids,
            "profile_id": terminal_profile,
        }
        checkpoint.private["terminal_profile"] = terminal_profile
        self.store.replace(checkpoint)

        self.product.autostart_disable()
        self.product.autostart_enable(terminal_profile)
        checkpoint = self.store.load()
        checkpoint.phase = PHASE_TERMINAL
        checkpoint.previous_boot_id = current_boot
        self.store.replace(checkpoint)

    def _resume_terminal(self, checkpoint: Checkpoint, current_boot: str) -> None:
        self._require_inactive()
        attempt = RuntimeState.boot_attempt()
        if (
            attempt.get("schema_version") != "podlaz.boot-autostart-attempt.v1"
            or attempt.get("state") != "terminal"
        ):
            raise AmbiguousState(
                "terminal boot did not persist terminal autostart authority"
            )
        fingerprint = self._attempt_fingerprint(attempt)
        self.runner.run(
            ("systemctl", "restart", "podlazd.service"), timeout=90
        ).require_success("terminal no-retry daemon restart")
        self._require_inactive()
        if self._attempt_fingerprint(RuntimeState.boot_attempt()) != fingerprint:
            raise AmbiguousState(
                "terminal boot attempt changed after same-boot daemon restart"
            )
        checkpoint.scenarios["reboot_terminal_autostart"] = ScenarioRecord(
            "reboot_terminal_autostart", ScenarioOutcome.PASS
        )
        checkpoint.scenarios["terminal_no_same_boot_retry"] = ScenarioRecord(
            "terminal_no_same_boot_retry", ScenarioOutcome.PASS
        )
        checkpoint.phase = PHASE_COMPLETE
        checkpoint.previous_boot_id = current_boot
        self.store.replace(checkpoint)

    def restore_original_policy(self) -> None:
        checkpoint = self.store.load()
        terminal = self._resolve_terminal_profile_for_cleanup(checkpoint)

        self.product.autostart_disable()
        if terminal:
            checkpoint = self.store.load()
            acquisition = checkpoint.private.get("terminal_profile_acquisition")
            if isinstance(acquisition, dict):
                acquisition = dict(acquisition)
                acquisition["state"] = "releasing"
                checkpoint.private["terminal_profile_acquisition"] = acquisition
                self.store.replace(checkpoint)
            self.product.delete_profile(terminal)
            checkpoint = self.store.load()
            checkpoint.private.pop("terminal_profile", None)
            checkpoint.private.pop("terminal_profile_acquisition", None)
            self.store.replace(checkpoint)

        checkpoint = self.store.load()
        restore_boot_manifest(checkpoint.original_autostart or {"enabled": False})

    def _resolve_terminal_profile_for_cleanup(self, checkpoint: Checkpoint) -> str | None:
        acquisition = checkpoint.private.get("terminal_profile_acquisition")
        legacy_terminal = checkpoint.private.get("terminal_profile")
        if acquisition is None:
            if not legacy_terminal:
                return None
            terminal = str(legacy_terminal)
            current = self.product.profile_ids()
            if terminal not in current:
                checkpoint.private.pop("terminal_profile", None)
                self.store.replace(checkpoint)
                return None
            if not self.product.is_terminal_acceptance_profile(terminal):
                raise AmbiguousState(
                    "recorded terminal profile no longer matches acceptance fixture"
                )
            return terminal
        if not isinstance(acquisition, dict):
            raise AmbiguousState("terminal profile acquisition state is invalid")

        state = str(acquisition.get("state") or "")
        baseline_raw = acquisition.get("baseline_ids")
        if not isinstance(baseline_raw, list) or any(
            not isinstance(item, str) or not item for item in baseline_raw
        ):
            raise AmbiguousState("terminal profile acquisition baseline is invalid")
        baseline = set(baseline_raw)
        if len(baseline) != len(baseline_raw):
            raise AmbiguousState("terminal profile acquisition baseline contains duplicates")
        current = self.product.profile_ids()

        if state == "acquiring":
            additions = current - baseline
            if not additions:
                checkpoint.private.pop("terminal_profile_acquisition", None)
                checkpoint.private.pop("terminal_profile", None)
                self.store.replace(checkpoint)
                return None
            if len(additions) != 1:
                raise AmbiguousState(
                    "terminal profile acquisition is ambiguous after interruption"
                )
            terminal = next(iter(additions))
            if not self.product.is_terminal_acceptance_profile(terminal):
                raise AmbiguousState(
                    "new profile after terminal acquisition checkpoint is not the acceptance fixture"
                )
            checkpoint.private["terminal_profile_acquisition"] = {
                "state": "acquired",
                "baseline_ids": sorted(baseline),
                "profile_id": terminal,
            }
            checkpoint.private["terminal_profile"] = terminal
            self.store.replace(checkpoint)
            return terminal

        if state not in {"acquired", "releasing"}:
            raise AmbiguousState(
                f"unsupported terminal profile acquisition state {state!r}"
            )
        terminal = str(acquisition.get("profile_id") or "")
        if not terminal or terminal in baseline:
            raise AmbiguousState("terminal profile acquired identity is invalid")
        if legacy_terminal and str(legacy_terminal) != terminal:
            raise AmbiguousState("terminal profile identities disagree")
        if terminal not in current:
            checkpoint.private.pop("terminal_profile", None)
            checkpoint.private.pop("terminal_profile_acquisition", None)
            self.store.replace(checkpoint)
            return None
        if not self.product.is_terminal_acceptance_profile(terminal):
            raise AmbiguousState(
                "recorded terminal profile no longer matches acceptance fixture"
            )
        return terminal

    def _verify_candidate(self, checkpoint: Checkpoint) -> None:
        expected = checkpoint.candidate
        path = Path(expected["path"])
        if path.is_symlink() or not path.is_file():
            raise AmbiguousState("recorded candidate package is unavailable on resume")
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if digest != expected.get("sha256"):
            raise AmbiguousState("candidate digest changed across reboot")
        result = self.runner.run(
            ("dpkg-query", "-W", "-f=${Version}", "podlaz"), timeout=5
        ).require_success("resume installed candidate")
        if result.stdout.strip() != expected.get("version"):
            raise AmbiguousState("installed candidate version changed across reboot")

    def _require_verified_active(self) -> None:
        self._wait_status("active", "active", require_verified=True)

    def _require_inactive(self) -> None:
        self._wait_status("inactive", "disabled", require_verified=False)

    def _wait_status(
        self, connection: str, tun: str, *, require_verified: bool
    ) -> None:
        import time

        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            result = self.runner.run(
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
        raise AmbiguousState(
            f"boot phase did not converge to {connection}/{tun}"
        )

    @staticmethod
    def _attempt_fingerprint(value: dict) -> tuple[str, ...]:
        fields = (
            str(value.get("schema_version") or ""),
            str(value.get("boot_id") or ""),
            str(value.get("manifest_generation") or ""),
            str(value.get("state") or ""),
            str(value.get("terminal_reason") or ""),
        )
        if not all(fields[:4]):
            raise AmbiguousState("boot attempt control fingerprint is incomplete")
        return fields
