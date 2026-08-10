#!/usr/bin/env python3
"""Sanitized installed-package TUN resource attribution and report CLI.

Private process identity is kept in a caller-owned temporary file. Public samples
contain only structural counters and never contain PIDs, transaction IDs,
command lines, generated-config paths, host names, or network identifiers.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from dataclasses import asdict
from pathlib import Path
from typing import Any, Mapping, Sequence

try:
    from .tun_soak_analysis import (
        build_report,
        compare_cleanup_boundaries,
        compare_reconnect_boundaries,
        evaluate_policy,
        summarize_samples,
    )
    from .tun_soak_process import (
        SCHEMA_VERSION,
        ActiveIdentity,
        AttributionError,
        ProcessIdentity,
        assert_replaced,
        assert_xray_gone,
        collect_daemon_boundary_sample,
        collect_sample,
        discover_active_identity,
        discover_daemon_identity,
    )
except ImportError:
    from tun_soak_analysis import (
        build_report,
        compare_cleanup_boundaries,
        compare_reconnect_boundaries,
        evaluate_policy,
        summarize_samples,
    )
    from tun_soak_process import (
        SCHEMA_VERSION,
        ActiveIdentity,
        AttributionError,
        ProcessIdentity,
        assert_replaced,
        assert_xray_gone,
        collect_daemon_boundary_sample,
        collect_sample,
        discover_active_identity,
        discover_daemon_identity,
    )

def _process_identity_to_json(identity: ProcessIdentity) -> dict[str, Any]:
    return {"schema_version": SCHEMA_VERSION, "daemon": asdict(identity)}


def _process_identity_from_json(value: Mapping[str, Any]) -> ProcessIdentity:
    try:
        return ProcessIdentity(**value["daemon"])
    except (KeyError, TypeError) as exc:
        raise AttributionError("private daemon identity file is invalid") from exc


def _identity_to_json(identity: ActiveIdentity) -> dict[str, Any]:
    return {
        "schema_version": SCHEMA_VERSION,
        "daemon": asdict(identity.daemon),
        "xray": asdict(identity.xray),
        "transaction_file": identity.transaction_file,
        "config_ref": identity.config_ref,
    }


def _identity_from_json(value: Mapping[str, Any]) -> ActiveIdentity:
    try:
        daemon = ProcessIdentity(**value["daemon"])
        xray = ProcessIdentity(**value["xray"])
        transaction_file = value["transaction_file"]
        config_ref = value["config_ref"]
    except (KeyError, TypeError) as exc:
        raise AttributionError("private identity file is invalid") from exc
    if not isinstance(transaction_file, str) or not isinstance(config_ref, str):
        raise AttributionError("private identity file is invalid")
    return ActiveIdentity(daemon=daemon, xray=xray, transaction_file=transaction_file, config_ref=config_ref)


def _load_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise AttributionError(f"read JSON evidence failed: {path.name}") from exc


def _write_json(path: Path, value: Any, *, mode: int = 0o644) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.chmod(temporary, mode)
    os.replace(temporary, path)


def _append_ndjson(path: Path, value: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(value, separators=(",", ":"), sort_keys=True) + "\n")


def _read_ndjson(path: Path) -> list[Mapping[str, Any]]:
    values: list[Mapping[str, Any]] = []
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise AttributionError(f"read sample evidence failed: {path.name}") from exc
    for line in lines:
        if not line.strip():
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError as exc:
            raise AttributionError("sample evidence contains malformed JSON") from exc
        if not isinstance(value, Mapping):
            raise AttributionError("sample evidence contains invalid record")
        values.append(value)
    return values


def _command_discover_daemon(args: argparse.Namespace) -> int:
    identity = discover_daemon_identity(
        daemon_pid=args.daemon_pid,
        proc_root=args.proc_root,
        cgroup_root=args.cgroup_root,
        daemon_exe=args.daemon_exe,
        expected_cgroup_suffix=args.cgroup_suffix,
    )
    _write_json(args.output, _process_identity_to_json(identity), mode=0o600)
    return 0


def _command_boundary_sample(args: argparse.Namespace) -> int:
    value = _load_json(args.identity)
    if not isinstance(value, Mapping):
        raise AttributionError("private daemon identity file is invalid")
    sample = collect_daemon_boundary_sample(
        _process_identity_from_json(value),
        proc_root=args.proc_root,
        cgroup_root=args.cgroup_root,
        phase=args.phase,
        sample_index=args.sample_index,
        elapsed_seconds=args.elapsed_seconds,
    )
    _write_json(args.output, sample)
    return 0


def _command_discover(args: argparse.Namespace) -> int:
    identity = discover_active_identity(
        daemon_pid=args.daemon_pid,
        transaction_dir=args.transaction_dir,
        proc_root=args.proc_root,
        cgroup_root=args.cgroup_root,
        daemon_exe=args.daemon_exe,
        xray_exe=args.xray_exe,
        expected_cgroup_suffix=args.cgroup_suffix,
    )
    _write_json(args.output, _identity_to_json(identity), mode=0o600)
    return 0


def _command_sample(args: argparse.Namespace) -> int:
    value = _load_json(args.identity)
    if not isinstance(value, Mapping):
        raise AttributionError("private identity file is invalid")
    identity = _identity_from_json(value)
    sample = collect_sample(
        identity,
        proc_root=args.proc_root,
        cgroup_root=args.cgroup_root,
        phase=args.phase,
        session=args.session,
        sample_index=args.sample_index,
        elapsed_seconds=args.elapsed_seconds,
    )
    _append_ndjson(args.output, sample)
    return 0


def _command_assert_replaced(args: argparse.Namespace) -> int:
    before = _load_json(args.before)
    after = _load_json(args.after)
    if not isinstance(before, Mapping) or not isinstance(after, Mapping):
        raise AttributionError("private active identity file is invalid")
    assert_replaced(_identity_from_json(before), _identity_from_json(after))
    return 0


def _command_assert_gone(args: argparse.Namespace) -> int:
    value = _load_json(args.identity)
    if not isinstance(value, Mapping):
        raise AttributionError("private identity file is invalid")
    assert_xray_gone(
        _identity_from_json(value),
        proc_root=args.proc_root,
        xray_exe=args.xray_exe,
    )
    return 0


def _command_summarize(args: argparse.Namespace) -> int:
    summary = summarize_samples(_read_ndjson(args.samples))
    _write_json(args.output, summary)
    return 0



def _command_report(args: argparse.Namespace) -> int:
    baseline = _load_json(args.baseline_boundary)
    cleanup = _load_json(args.cleanup_boundary)
    provenance = _load_json(args.provenance)
    configuration = _load_json(args.configuration)
    policy = _load_json(args.policy)
    if not all(isinstance(value, Mapping) for value in (baseline, cleanup, provenance, configuration, policy)):
        raise AttributionError("report input contains invalid JSON objects")
    report = build_report(
        active_samples=_read_ndjson(args.samples),
        reconnect_samples=_read_ndjson(args.reconnect_samples),
        baseline_boundary=baseline,
        cleanup_boundary=cleanup,
        provenance=provenance,
        configuration=configuration,
        policy=policy,
        cleanup_memory_tolerance_bytes=args.cleanup_memory_tolerance_bytes,
        reconnect_memory_tolerance_bytes=args.reconnect_memory_tolerance_bytes,
        reconnect_count_tolerance=args.reconnect_count_tolerance,
    )
    _write_json(args.output, report)
    return 0 if report["ok"] else 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    discover_daemon = subparsers.add_parser("discover-daemon", help="prove exact inactive daemon attribution")
    discover_daemon.add_argument("--daemon-pid", type=int, required=True)
    discover_daemon.add_argument("--output", type=Path, required=True)
    discover_daemon.add_argument("--proc-root", type=Path, default=Path("/proc"))
    discover_daemon.add_argument("--cgroup-root", type=Path, default=Path("/sys/fs/cgroup"))
    discover_daemon.add_argument("--daemon-exe", default="/usr/bin/podlazd")
    discover_daemon.add_argument("--cgroup-suffix", default="/podlazd.service")
    discover_daemon.set_defaults(handler=_command_discover_daemon)

    boundary = subparsers.add_parser("boundary-sample", help="write one inactive daemon boundary sample")
    boundary.add_argument("--identity", type=Path, required=True)
    boundary.add_argument("--output", type=Path, required=True)
    boundary.add_argument("--phase", choices=("inactive-baseline", "post-cleanup"), required=True)
    boundary.add_argument("--sample-index", type=int, required=True)
    boundary.add_argument("--elapsed-seconds", type=float, required=True)
    boundary.add_argument("--proc-root", type=Path, default=Path("/proc"))
    boundary.add_argument("--cgroup-root", type=Path, default=Path("/sys/fs/cgroup"))
    boundary.set_defaults(handler=_command_boundary_sample)

    discover = subparsers.add_parser("discover", help="prove exact active daemon/Xray attribution")
    discover.add_argument("--daemon-pid", type=int, required=True)
    discover.add_argument("--transaction-dir", type=Path, required=True)
    discover.add_argument("--output", type=Path, required=True)
    discover.add_argument("--proc-root", type=Path, default=Path("/proc"))
    discover.add_argument("--cgroup-root", type=Path, default=Path("/sys/fs/cgroup"))
    discover.add_argument("--daemon-exe", default="/usr/bin/podlazd")
    discover.add_argument("--xray-exe", default="/usr/lib/podlaz/xray")
    discover.add_argument("--cgroup-suffix", default="/podlazd.service")
    discover.set_defaults(handler=_command_discover)

    sample = subparsers.add_parser("sample", help="append one sanitized structural sample")
    sample.add_argument("--identity", type=Path, required=True)
    sample.add_argument("--output", type=Path, required=True)
    sample.add_argument("--phase", choices=("active", "reconnect"), required=True)
    sample.add_argument("--session", type=int, required=True)
    sample.add_argument("--sample-index", type=int, required=True)
    sample.add_argument("--elapsed-seconds", type=float, required=True)
    sample.add_argument("--proc-root", type=Path, default=Path("/proc"))
    sample.add_argument("--cgroup-root", type=Path, default=Path("/sys/fs/cgroup"))
    sample.set_defaults(handler=_command_sample)

    replaced = subparsers.add_parser("assert-replaced", help="prove reconnect created a new exact Xray child")
    replaced.add_argument("--before", type=Path, required=True)
    replaced.add_argument("--after", type=Path, required=True)
    replaced.set_defaults(handler=_command_assert_replaced)

    gone = subparsers.add_parser("assert-gone", help="prove exact child and packaged Xray absence")
    gone.add_argument("--identity", type=Path, required=True)
    gone.add_argument("--xray-exe", default="/usr/lib/podlaz/xray")
    gone.add_argument("--proc-root", type=Path, default=Path("/proc"))
    gone.set_defaults(handler=_command_assert_gone)

    summarize = subparsers.add_parser("summarize", help="write sanitized component-specific trend summary")
    summarize.add_argument("--samples", type=Path, required=True)
    summarize.add_argument("--output", type=Path, required=True)
    summarize.set_defaults(handler=_command_summarize)

    report = subparsers.add_parser("report", help="write one sanitized soak trend and lifecycle verdict")
    report.add_argument("--samples", type=Path, required=True)
    report.add_argument("--reconnect-samples", type=Path, required=True)
    report.add_argument("--baseline-boundary", type=Path, required=True)
    report.add_argument("--cleanup-boundary", type=Path, required=True)
    report.add_argument("--provenance", type=Path, required=True)
    report.add_argument("--configuration", type=Path, required=True)
    report.add_argument("--policy", type=Path, required=True)
    report.add_argument("--cleanup-memory-tolerance-bytes", type=int, required=True)
    report.add_argument("--reconnect-memory-tolerance-bytes", type=int, required=True)
    report.add_argument("--reconnect-count-tolerance", type=int, required=True)
    report.add_argument("--output", type=Path, required=True)
    report.set_defaults(handler=_command_report)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return int(args.handler(args))
    except (AttributionError, ValueError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
