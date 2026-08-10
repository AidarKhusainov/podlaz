#!/usr/bin/env python3
"""Exact procfs/cgroup-v2 attribution for the installed-package TUN soak."""

from __future__ import annotations

import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Mapping, Sequence

SCHEMA_VERSION = 1
KIB = 1024
MIB = 1024 * 1024

# A process with one of these names may own or mutate VPN-relevant host state.
# Discovery fails closed instead of publishing ambiguous attribution. The exact
# supervised Xray child and podlazd are excluded before this set is evaluated.
FOREIGN_CORE_NAMES = frozenset(
    {
        "xray",
        "v2ray",
        "sing-box",
        "mihomo",
        "clash",
        "clash-meta",
        "openvpn",
        "openvpn3",
        "tailscaled",
        "wireguard-go",
        "wg-quick",
    }
)

PROCESS_METRICS = (
    "rss_bytes",
    "pss_bytes",
    "threads",
    "tasks",
    "fds",
    "cpu_time_ticks",
)
CGROUP_METRICS = (
    "memory_current_bytes",
    "memory_peak_bytes",
    "pids_current",
    "cpu_usage_usec",
)
NON_GROWTH_METRICS = frozenset(
    {
        "cgroup.memory_peak_bytes",  # high-water mark is monotonic by definition
        "cgroup.cpu_usage_usec",
        "podlazd.cpu_time_ticks",
        "xray.cpu_time_ticks",
    }
)


class AttributionError(RuntimeError):
    """Raised when exact process/cgroup ownership cannot be proved."""


@dataclass(frozen=True)
class ProcessIdentity:
    pid: int
    start_time_ticks: int
    exe: str
    cgroup_path: str


@dataclass(frozen=True)
class ActiveIdentity:
    daemon: ProcessIdentity
    xray: ProcessIdentity
    transaction_file: str
    config_ref: str


def _read_text(path: Path) -> str:
    try:
        return path.read_text(encoding="utf-8")
    except OSError as exc:
        raise AttributionError(f"read structural process evidence failed: {path.name}: {exc.strerror or exc}") from exc


def _read_int(path: Path) -> int:
    value = _read_text(path).strip()
    if not value.isdigit():
        raise AttributionError(f"invalid numeric structural metric in {path.name}")
    return int(value)


def _read_exe(proc_root: Path, pid: int) -> str:
    try:
        return os.readlink(proc_root / str(pid) / "exe")
    except OSError as exc:
        raise AttributionError("process executable identity is unavailable") from exc


def _parse_proc_stat(text: str) -> tuple[int, int, int]:
    close = text.rfind(")")
    if close <= 0:
        raise AttributionError("malformed process stat record")
    fields = text[close + 1 :].strip().split()
    # fields starts with kernel field 3 (state). Indexes below correspond to
    # fields 14, 15 and 22 in proc_pid_stat(5).
    if len(fields) <= 19:
        raise AttributionError("incomplete process stat record")
    try:
        utime = int(fields[11])
        stime = int(fields[12])
        start_time = int(fields[19])
    except ValueError as exc:
        raise AttributionError("non-numeric process stat record") from exc
    if min(utime, stime, start_time) < 0:
        raise AttributionError("negative process stat value")
    return utime, stime, start_time


def _read_start_time(proc_root: Path, pid: int) -> int:
    _, _, start_time = _parse_proc_stat(_read_text(proc_root / str(pid) / "stat"))
    return start_time


def _read_unified_cgroup_path(proc_root: Path, pid: int) -> str:
    entries = []
    for raw in _read_text(proc_root / str(pid) / "cgroup").splitlines():
        parts = raw.split(":", 2)
        if len(parts) == 3 and parts[0] == "0" and parts[1] == "":
            entries.append(parts[2])
    if len(entries) != 1 or not entries[0].startswith("/"):
        raise AttributionError("process has no unique cgroup v2 identity")
    return entries[0]


def _read_cmdline(proc_root: Path, pid: int) -> tuple[str, ...]:
    try:
        data = (proc_root / str(pid) / "cmdline").read_bytes()
    except OSError as exc:
        raise AttributionError("supervised child command identity is unavailable") from exc
    values = tuple(part.decode("utf-8", errors="strict") for part in data.split(b"\0") if part)
    if not values:
        raise AttributionError("supervised child command identity is empty")
    return values


def _process_identity(proc_root: Path, pid: int, expected_exe: str) -> ProcessIdentity:
    if pid <= 1:
        raise AttributionError("unsafe process identity")
    actual_exe = _read_exe(proc_root, pid)
    if actual_exe != expected_exe:
        raise AttributionError("process executable identity does not match the installed package")
    return ProcessIdentity(
        pid=pid,
        start_time_ticks=_read_start_time(proc_root, pid),
        exe=actual_exe,
        cgroup_path=_read_unified_cgroup_path(proc_root, pid),
    )


def _transaction_candidates(transaction_dir: Path) -> list[tuple[Path, Mapping[str, Any]]]:
    candidates: list[tuple[Path, Mapping[str, Any]]] = []
    try:
        paths = sorted(transaction_dir.glob("*.json"))
    except OSError as exc:
        raise AttributionError("transaction inventory is unavailable") from exc
    for path in paths:
        try:
            value = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise AttributionError("transaction inventory contains unreadable state") from exc
        if not isinstance(value, Mapping):
            raise AttributionError("transaction inventory contains invalid state")
        if value.get("owner") == "podlaz" and value.get("mode") == "tun" and value.get("state") == "committed":
            candidates.append((path, value))
    return candidates


def _supervised_child(transaction: Mapping[str, Any]) -> tuple[int, str]:
    rollback = transaction.get("rollback")
    children = rollback.get("child_processes") if isinstance(rollback, Mapping) else None
    if not isinstance(children, list):
        raise AttributionError("committed TUN transaction has no supervised child evidence")
    matching = []
    for child in children:
        if not isinstance(child, Mapping):
            raise AttributionError("committed TUN transaction has malformed child evidence")
        if child.get("owner") == "podlaz" and child.get("label") == "xray":
            pid = child.get("pid")
            config_ref = child.get("config_ref")
            if not isinstance(pid, int) or pid <= 1 or not isinstance(config_ref, str) or not config_ref.startswith("/"):
                raise AttributionError("committed TUN transaction has incomplete child identity")
            matching.append((pid, config_ref))
    if len(matching) != 1:
        raise AttributionError("committed TUN transaction has ambiguous supervised child identity")
    return matching[0]


def _assert_config_reference(cmdline: Sequence[str], config_ref: str) -> None:
    values = [cmdline[index + 1] for index, value in enumerate(cmdline[:-1]) if value in {"-config", "-c"}]
    if values != [config_ref]:
        raise AttributionError("supervised child command does not reference exact durable runtime config")


def _foreign_core_pids(proc_root: Path, excluded: set[int]) -> list[int]:
    foreign: list[int] = []
    try:
        entries = list(proc_root.iterdir())
    except OSError as exc:
        raise AttributionError("process inventory is unavailable") from exc
    for entry in entries:
        if not entry.name.isdigit():
            continue
        pid = int(entry.name)
        if pid in excluded:
            continue
        try:
            comm = (entry / "comm").read_text(encoding="utf-8").strip().lower()
        except (OSError, UnicodeError):
            # A process that disappears during the scan is harmless. A process
            # that remains but cannot be inspected is ambiguous and therefore
            # blocks attribution.
            if entry.exists():
                raise AttributionError("foreign process inventory is incomplete")
            continue
        if comm in FOREIGN_CORE_NAMES:
            foreign.append(pid)
    return foreign


def discover_daemon_identity(
    *,
    daemon_pid: int,
    proc_root: Path = Path("/proc"),
    cgroup_root: Path = Path("/sys/fs/cgroup"),
    daemon_exe: str = "/usr/bin/podlazd",
    expected_cgroup_suffix: str = "/podlazd.service",
) -> ProcessIdentity:
    """Prove the installed daemon identity at an inactive lifecycle boundary."""

    daemon = _process_identity(proc_root, daemon_pid, daemon_exe)
    if not daemon.cgroup_path.endswith(expected_cgroup_suffix):
        raise AttributionError("podlazd is outside the expected service cgroup")
    cgroup_path = cgroup_root / daemon.cgroup_path.lstrip("/")
    try:
        cgroup_path.resolve(strict=True).relative_to(cgroup_root.resolve(strict=True))
    except (OSError, ValueError) as exc:
        raise AttributionError("service cgroup metrics are unavailable") from exc
    foreign = _foreign_core_pids(proc_root, {daemon_pid})
    if foreign:
        raise AttributionError("foreign VPN/core process may contaminate resource or network attribution")
    return daemon


def discover_active_identity(
    *,
    daemon_pid: int,
    transaction_dir: Path,
    proc_root: Path = Path("/proc"),
    cgroup_root: Path = Path("/sys/fs/cgroup"),
    daemon_exe: str = "/usr/bin/podlazd",
    xray_exe: str = "/usr/lib/podlaz/xray",
    expected_cgroup_suffix: str = "/podlazd.service",
) -> ActiveIdentity:
    """Prove exact daemon/Xray ownership before the warm-up baseline."""

    daemon = _process_identity(proc_root, daemon_pid, daemon_exe)
    if not daemon.cgroup_path.endswith(expected_cgroup_suffix):
        raise AttributionError("podlazd is outside the expected service cgroup")

    candidates = _transaction_candidates(transaction_dir)
    if len(candidates) != 1:
        raise AttributionError("active TUN transaction attribution is ambiguous")
    transaction_path, transaction = candidates[0]
    xray_pid, config_ref = _supervised_child(transaction)
    xray = _process_identity(proc_root, xray_pid, xray_exe)
    _assert_config_reference(_read_cmdline(proc_root, xray_pid), config_ref)

    if xray.cgroup_path != daemon.cgroup_path:
        raise AttributionError("supervised Xray child is outside the podlazd service cgroup")

    cgroup_path = cgroup_root / daemon.cgroup_path.lstrip("/")
    try:
        cgroup_path.resolve(strict=True).relative_to(cgroup_root.resolve(strict=True))
    except (OSError, ValueError) as exc:
        raise AttributionError("service cgroup metrics are unavailable") from exc

    foreign = _foreign_core_pids(proc_root, {daemon_pid, xray_pid})
    if foreign:
        raise AttributionError("foreign VPN/core process may contaminate resource or network attribution")

    return ActiveIdentity(
        daemon=daemon,
        xray=xray,
        transaction_file=str(transaction_path),
        config_ref=config_ref,
    )


def _parse_kib_mapping(path: Path) -> dict[str, int]:
    result: dict[str, int] = {}
    for raw in _read_text(path).splitlines():
        if ":" not in raw:
            continue
        key, value = raw.split(":", 1)
        fields = value.split()
        if not fields or not fields[0].isdigit():
            continue
        multiplier = KIB if len(fields) >= 2 and fields[1] == "kB" else 1
        result[key] = int(fields[0]) * multiplier
    return result


def _assert_identity_current(identity: ProcessIdentity, proc_root: Path) -> None:
    if _read_exe(proc_root, identity.pid) != identity.exe:
        raise AttributionError("process executable identity changed during soak")
    if _read_start_time(proc_root, identity.pid) != identity.start_time_ticks:
        raise AttributionError("process identity changed during soak")
    if _read_unified_cgroup_path(proc_root, identity.pid) != identity.cgroup_path:
        raise AttributionError("process cgroup identity changed during soak")


def _directory_count(path: Path, label: str) -> int:
    try:
        return sum(1 for _ in path.iterdir())
    except OSError as exc:
        raise AttributionError(f"{label} inventory is unavailable") from exc


def _process_metrics(identity: ProcessIdentity, proc_root: Path) -> dict[str, int | None]:
    _assert_identity_current(identity, proc_root)
    process = proc_root / str(identity.pid)
    status = _parse_kib_mapping(process / "status")
    smaps = _parse_kib_mapping(process / "smaps_rollup") if (process / "smaps_rollup").exists() else {}
    utime, stime, _ = _parse_proc_stat(_read_text(process / "stat"))
    rss = smaps.get("Rss", status.get("VmRSS"))
    if rss is None:
        raise AttributionError("process RSS is unavailable")
    threads = status.get("Threads")
    if threads is None:
        raise AttributionError("process thread count is unavailable")
    return {
        "rss_bytes": rss,
        "pss_bytes": smaps.get("Pss"),
        "threads": threads,
        "tasks": _directory_count(process / "task", "process task"),
        "fds": _directory_count(process / "fd", "process file descriptor"),
        "cpu_time_ticks": utime + stime,
    }


def _parse_cpu_stat(path: Path) -> int:
    values: dict[str, int] = {}
    for raw in _read_text(path).splitlines():
        fields = raw.split()
        if len(fields) == 2 and fields[1].isdigit():
            values[fields[0]] = int(fields[1])
    if "usage_usec" not in values:
        raise AttributionError("cgroup CPU usage is unavailable")
    return values["usage_usec"]


def _cgroup_metrics(cgroup_root: Path, cgroup_path: str) -> dict[str, int | None]:
    path = cgroup_root / cgroup_path.lstrip("/")
    current = _read_int(path / "memory.current")
    peak_path = path / "memory.peak"
    peak = _read_int(peak_path) if peak_path.exists() else None
    return {
        "memory_current_bytes": current,
        "memory_peak_bytes": peak,
        "pids_current": _read_int(path / "pids.current"),
        "cpu_usage_usec": _parse_cpu_stat(path / "cpu.stat"),
    }


def collect_sample(
    identity: ActiveIdentity,
    *,
    proc_root: Path = Path("/proc"),
    cgroup_root: Path = Path("/sys/fs/cgroup"),
    phase: str,
    session: int,
    sample_index: int,
    elapsed_seconds: float,
) -> dict[str, Any]:
    if phase not in {"inactive-baseline", "active", "post-cleanup", "reconnect"}:
        raise ValueError("unsupported sample phase")
    if session < 0 or sample_index < 0 or elapsed_seconds < 0:
        raise ValueError("negative sample coordinate")
    return {
        "schema_version": SCHEMA_VERSION,
        "phase": phase,
        "session": session,
        "sample_index": sample_index,
        "elapsed_seconds": elapsed_seconds,
        "cgroup": _cgroup_metrics(cgroup_root, identity.daemon.cgroup_path),
        "podlazd": _process_metrics(identity.daemon, proc_root),
        "xray": _process_metrics(identity.xray, proc_root),
    }


def collect_daemon_boundary_sample(
    daemon: ProcessIdentity,
    *,
    proc_root: Path = Path("/proc"),
    cgroup_root: Path = Path("/sys/fs/cgroup"),
    phase: str,
    sample_index: int,
    elapsed_seconds: float,
) -> dict[str, Any]:
    if phase not in {"inactive-baseline", "post-cleanup"}:
        raise ValueError("daemon boundary phase must be inactive")
    return {
        "schema_version": SCHEMA_VERSION,
        "phase": phase,
        "session": 0,
        "sample_index": sample_index,
        "elapsed_seconds": elapsed_seconds,
        "cgroup": _cgroup_metrics(cgroup_root, daemon.cgroup_path),
        "podlazd": _process_metrics(daemon, proc_root),
        "xray": None,
    }


def assert_replaced(previous: ActiveIdentity, current: ActiveIdentity) -> None:
    if previous.daemon != current.daemon:
        raise AttributionError("podlazd identity changed across reconnect")
    if previous.xray == current.xray:
        raise AttributionError("supervised Xray child was not replaced across reconnect")
    if previous.xray.cgroup_path != current.xray.cgroup_path:
        raise AttributionError("replacement Xray child changed service cgroup")


def assert_xray_gone(
    identity: ActiveIdentity,
    *,
    proc_root: Path = Path("/proc"),
    xray_exe: str = "/usr/lib/podlaz/xray",
) -> None:
    process_path = proc_root / str(identity.xray.pid)
    if process_path.exists():
        try:
            current_start = _read_start_time(proc_root, identity.xray.pid)
            current_exe = _read_exe(proc_root, identity.xray.pid)
        except AttributionError:
            raise AttributionError("exact supervised Xray disappearance is ambiguous")
        if current_start == identity.xray.start_time_ticks and current_exe == identity.xray.exe:
            raise AttributionError("exact supervised Xray child remains after disconnect")

    for entry in proc_root.iterdir():
        if not entry.name.isdigit():
            continue
        try:
            if os.readlink(entry / "exe") == xray_exe:
                raise AttributionError("orphan packaged Xray remains after disconnect")
        except FileNotFoundError:
            continue
        except OSError:
            if entry.exists():
                raise AttributionError("packaged Xray orphan inspection is incomplete")
