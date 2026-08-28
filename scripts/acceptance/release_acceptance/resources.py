from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import json
import os

from .command import CommandRunner
from .model import AmbiguousState
from .product import RuntimeState


@dataclass(frozen=True)
class ResourceSample:
    memory_current: int
    memory_peak: int
    tasks: int
    fds: int
    threads: int


@dataclass(frozen=True)
class ResourceSummary:
    sample_count: int
    soak_memory_current_sampled_peak_bytes: int
    service_cgroup_lifetime_memory_peak_bytes: int
    tasks_peak: int
    fds_peak: int
    threads_peak: int


@dataclass(frozen=True)
class XrayAuthority:
    pid: int
    config_ref: str


class ResourceSampler:
    def __init__(self, runner: CommandRunner):
        self.runner = runner

    def sample(self) -> ResourceSample:
        daemon_pid = self._main_pid()
        if os.readlink(f"/proc/{daemon_pid}/exe") != "/usr/bin/podlazd":
            raise AmbiguousState("podlazd MainPID executable identity mismatch")
        cgroup = self._service_cgroup(daemon_pid)
        authority = self._committed_xray_authority()
        self._verify_xray_identity(authority, daemon_pid, cgroup)
        cgroup_root = Path("/sys/fs/cgroup") / cgroup.lstrip("/")
        return ResourceSample(
            memory_current=self._read_int(cgroup_root / "memory.current"),
            memory_peak=self._read_int(cgroup_root / "memory.peak"),
            tasks=self._read_int(cgroup_root / "pids.current"),
            fds=self._fd_count(daemon_pid) + self._fd_count(authority.pid),
            threads=self._thread_count(daemon_pid) + self._thread_count(authority.pid),
        )

    @staticmethod
    def summarize(samples: list[ResourceSample]) -> ResourceSummary:
        if not samples:
            raise AmbiguousState("resource summary requires at least one sample")
        return ResourceSummary(
            sample_count=len(samples),
            soak_memory_current_sampled_peak_bytes=max(
                item.memory_current for item in samples
            ),
            service_cgroup_lifetime_memory_peak_bytes=max(
                item.memory_peak for item in samples
            ),
            tasks_peak=max(item.tasks for item in samples),
            fds_peak=max(item.fds for item in samples),
            threads_peak=max(item.threads for item in samples),
        )

    def _main_pid(self) -> int:
        result = self.runner.run(
            ("systemctl", "show", "-p", "MainPID", "--value", "podlazd.service"),
            timeout=5,
        ).require_success("resource MainPID")
        try:
            pid = int(result.stdout.strip())
        except ValueError as error:
            raise AmbiguousState("invalid resource MainPID") from error
        if pid <= 1:
            raise AmbiguousState("podlazd has no active MainPID")
        return pid

    @staticmethod
    def _service_cgroup(pid: int) -> str:
        for line in Path(f"/proc/{pid}/cgroup").read_text(encoding="utf-8").splitlines():
            fields = line.split(":", 2)
            if len(fields) == 3 and fields[0] == "0":
                return fields[2]
        raise AmbiguousState("podlazd is not attributable to unified cgroup v2")

    @staticmethod
    def _committed_xray_authority() -> XrayAuthority:
        authorities: list[XrayAuthority] = []
        for path in RuntimeState.TRANSACTIONS.glob("*.json"):
            if path.is_symlink() or not path.is_file():
                raise AmbiguousState("transaction directory contains non-regular state")
            try:
                tx = json.loads(path.read_text(encoding="utf-8"))
            except (OSError, json.JSONDecodeError) as error:
                raise AmbiguousState("could not decode transaction for resource attribution") from error
            if tx.get("owner") != "podlaz" or tx.get("state") != "committed":
                continue
            children = (tx.get("rollback") or {}).get("child_processes") or []
            if not isinstance(children, list):
                raise AmbiguousState("committed transaction has invalid child-process authority")
            for child in children:
                if not isinstance(child, dict):
                    raise AmbiguousState("committed transaction child authority is malformed")
                if child.get("owner") != "podlaz" or child.get("label") != "xray":
                    continue
                try:
                    pid = int(child.get("pid") or 0)
                except (TypeError, ValueError) as error:
                    raise AmbiguousState("transaction Xray PID is invalid") from error
                config_ref = str(child.get("config_ref") or "").strip()
                if pid <= 1 or not config_ref.startswith("/"):
                    raise AmbiguousState("transaction Xray authority is incomplete")
                authorities.append(XrayAuthority(pid, config_ref))
        if len(authorities) != 1:
            raise AmbiguousState(
                f"expected one committed transaction Xray authority, found {len(authorities)}"
            )
        return authorities[0]

    @classmethod
    def _verify_xray_identity(
        cls, authority: XrayAuthority, daemon_pid: int, service_cgroup: str
    ) -> None:
        pid = authority.pid
        try:
            executable = os.readlink(f"/proc/{pid}/exe")
            cgroup = cls._service_cgroup(pid)
            cmdline = Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0")
            stat_fields = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8").split()
        except (FileNotFoundError, PermissionError, ProcessLookupError, OSError) as error:
            raise AmbiguousState("transaction-owned Xray process is unavailable") from error
        if executable != "/usr/lib/podlaz/xray":
            raise AmbiguousState("transaction-owned Xray executable identity mismatch")
        if cgroup != service_cgroup:
            raise AmbiguousState("transaction-owned Xray is outside podlazd service cgroup")
        if len(stat_fields) < 4:
            raise AmbiguousState("transaction-owned Xray proc stat is incomplete")
        try:
            parent_pid = int(stat_fields[3])
        except ValueError as error:
            raise AmbiguousState("transaction-owned Xray parent PID is invalid") from error
        if parent_pid != daemon_pid:
            raise AmbiguousState("transaction-owned Xray is not a direct podlazd child")
        decoded = [value.decode("utf-8", errors="strict") for value in cmdline if value]
        if authority.config_ref not in decoded:
            raise AmbiguousState("transaction config_ref is absent from Xray command line")

        # Foreign Xray processes elsewhere on the laptop are irrelevant. Ambiguity
        # matters only inside the exact Podlaz service cgroup.
        same_cgroup_xray = 0
        for entry in Path("/proc").iterdir():
            if not entry.name.isdigit():
                continue
            other_pid = int(entry.name)
            try:
                if os.readlink(entry / "exe") != "/usr/lib/podlaz/xray":
                    continue
                if cls._service_cgroup(other_pid) == service_cgroup:
                    same_cgroup_xray += 1
            except (FileNotFoundError, PermissionError, ProcessLookupError, OSError):
                continue
        if same_cgroup_xray != 1:
            raise AmbiguousState(
                f"expected one Xray inside Podlaz service cgroup, found {same_cgroup_xray}"
            )

    @staticmethod
    def _read_int(path: Path) -> int:
        value = path.read_text(encoding="utf-8").strip()
        try:
            return int(value)
        except ValueError as error:
            raise AmbiguousState(f"invalid integer metric: {path}") from error

    @staticmethod
    def _fd_count(pid: int) -> int:
        return sum(1 for _ in Path(f"/proc/{pid}/fd").iterdir())

    @staticmethod
    def _thread_count(pid: int) -> int:
        return sum(1 for _ in Path(f"/proc/{pid}/task").iterdir())
