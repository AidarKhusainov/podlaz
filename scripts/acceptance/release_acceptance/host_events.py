from __future__ import annotations

from dataclasses import dataclass
import os

from .checkpoint import MutationLedger
from .command import CommandRunner
from .model import AmbiguousState, ScenarioOutcome


@dataclass(frozen=True)
class HostEventResult:
    outcome: ScenarioOutcome
    reason: str


class WifiLease:
    def __init__(self, runner: CommandRunner, ledger: MutationLedger):
        self.runner = runner
        self.ledger = ledger
        self.connection: str | None = None

    def reconnect(self) -> HostEventResult:
        if os.environ.get("SSH_CONNECTION") or os.environ.get("SSH_CLIENT"):
            return HostEventResult(ScenarioOutcome.SKIP_REMOTE_SESSION, "remote_session")
        uplink = self._uplink()
        device = self.runner.run(("nmcli", "-g", "GENERAL.TYPE,GENERAL.CONNECTION", "device", "show", uplink), timeout=10)
        if device.returncode != 0:
            return HostEventResult(ScenarioOutcome.SKIP_HOST_CAPABILITY, "networkmanager_unavailable")
        rows = [line for line in device.stdout.splitlines() if line.strip()]
        connection = rows[-1].split(":", 1)[-1].strip() if rows else ""
        if not connection or connection == "--":
            return HostEventResult(ScenarioOutcome.SKIP_HOST_CAPABILITY, "no_networkmanager_connection")
        self.connection = connection
        self.ledger.begin_acquire("wifi_reconnect", "networkmanager_connection", {"connection": connection, "uplink": uplink})
        self.runner.run(("nmcli", "connection", "down", connection), timeout=30).require_success("controlled Wi-Fi/NM disconnect")
        self.ledger.mark_acquired("wifi_reconnect")
        self.ledger.begin_release("wifi_reconnect")
        self.runner.run(("nmcli", "connection", "up", connection), timeout=60).require_success("restore NetworkManager connection")
        self.ledger.mark_released("wifi_reconnect")
        return HostEventResult(ScenarioOutcome.PASS, "controlled_networkmanager_reconnect")

    def _uplink(self) -> str:
        result = self.runner.run(("ip", "-4", "route", "show", "table", "main", "default"), timeout=5).require_success("default uplink")
        fields = result.stdout.split()
        if "dev" not in fields:
            raise AmbiguousState("default uplink has no device")
        uplink = fields[fields.index("dev") + 1]
        if uplink == "podlaz0":
            raise AmbiguousState("default ordinary uplink resolved to podlaz0")
        return uplink


class SuspendEvent:
    def __init__(self, runner: CommandRunner):
        self.runner = runner

    def run(self) -> HostEventResult:
        if os.environ.get("SSH_CONNECTION") or os.environ.get("SSH_CLIENT"):
            return HostEventResult(ScenarioOutcome.SKIP_REMOTE_SESSION, "remote_session")
        if not os.path.exists("/sys/power/state") or "mem" not in open("/sys/power/state", encoding="utf-8").read():
            return HostEventResult(ScenarioOutcome.SKIP_HOST_CAPABILITY, "suspend_mem_unsupported")
        result = self.runner.run(("rtcwake", "-m", "mem", "-s", "8"), timeout=30)
        if result.returncode != 0:
            return HostEventResult(ScenarioOutcome.SKIP_HOST_CAPABILITY, "rtcwake_failed")
        return HostEventResult(ScenarioOutcome.PASS, "suspend_resume_completed")
