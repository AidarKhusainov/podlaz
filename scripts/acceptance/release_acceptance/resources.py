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


class ResourceSampler:
    def __init__(self, runner: CommandRunner):
        self.runner = runner

    def sample(self) -> ResourceSample:
        pid = self._main_pid()
        exe = os.readlink(f"/proc/{pid}/exe")
        if exe != "/usr/bin/podlazd":
            raise AmbiguousState("podlazd MainPID executable identity mismatch")
        cgroup = self._service_cgroup(pid)
        xray_pid = self._xray_pid(cgroup)
        xray_exe = os.readlink(f"/proc/{xray_pid}/exe")
        if xray_exe != "/usr/lib/podlaz/xray":
            raise AmbiguousState("supervised Xray executable identity mismatch")
        cgroup_root = Path("/sys/fs/cgroup") / cgroup.lstrip("/")
        return ResourceSample(
            memory_current=self._read_int(cgroup_root / "memory.current"),
            memory_peak=self._read_int(cgroup_root / "memory.peak"),
            tasks=self._read_int(cgroup_root / "pids.current"),
            fds=self._fd_count(pid) + self._fd_count(xray_pid),
            threads=self._thread_count(pid) + self._thread_count(xray_pid),
        )

    @staticmethod
    def summarize(samples: list[ResourceSample]) -> ResourceSummary:
        if not samples:
            raise AmbiguousState("resource summary requires at least one sample")
        return ResourceSummary(
            sample_count=len(samples),
            soak_memory_current_sampled_peak_bytes=max(item.memory_current for item in samples),
            service_cgroup_lifetime_memory_peak_bytes=max(item.memory_peak for item in samples),
            tasks_peak=max(item.tasks for item in samples),
            fds_peak=max(item.fds for item in samples),
            threads_peak=max(item.threads for item in samples),
        )

    def _main_pid(self) -> int:
        result = self.runner.run(("systemctl", "show", "-p", "MainPID", "--value", "podlazd.service"), timeout=5).require_success("resource MainPID")
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
    def _xray_pid(cgroup: str) -> int:
        candidates = []
        for entry in Path("/proc").iterdir():
            if not entry.name.isdigit():
                continue
            pid = int(entry.name)
            try:
                exe = os.readlink(entry / "exe")
                groups = (entry / "cgroup").read_text(encoding="utf-8")
            except (FileNotFoundError, PermissionError, ProcessLookupError):
                continue
            if exe == "/usr/lib/podlaz/xray" and f"0::{cgroup}" in groups:
                candidates.append(pid)
        if len(candidates) != 1:
            raise AmbiguousState(f"expected one Podlaz Xray in service cgroup, found {len(candidates)}")
        return candidates[0]

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
