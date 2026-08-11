#!/usr/bin/env python3
"""Sanitized trend, lifecycle, and policy analysis for TUN soak samples."""

from __future__ import annotations

import statistics
from typing import Any, Mapping, Sequence

try:
    from .tun_soak_process import (
        CGROUP_METRICS,
        MIB,
        NON_GROWTH_METRICS,
        PROCESS_METRICS,
        SCHEMA_VERSION,
    )
except ImportError:
    from tun_soak_process import (
        CGROUP_METRICS,
        MIB,
        NON_GROWTH_METRICS,
        PROCESS_METRICS,
        SCHEMA_VERSION,
    )

def _flatten_samples(samples: Sequence[Mapping[str, Any]]) -> dict[str, list[tuple[float, float]]]:
    flattened: dict[str, list[tuple[float, float]]] = {}
    for sample in samples:
        if sample.get("phase") != "active" or sample.get("session") != 1:
            continue
        elapsed = sample.get("elapsed_seconds")
        if not isinstance(elapsed, (int, float)):
            raise ValueError("sample elapsed_seconds is invalid")
        for component, names in (("cgroup", CGROUP_METRICS), ("podlazd", PROCESS_METRICS), ("xray", PROCESS_METRICS)):
            values = sample.get(component)
            if not isinstance(values, Mapping):
                raise ValueError(f"sample {component} metrics are invalid")
            for name in names:
                value = values.get(name)
                if value is None:
                    continue
                if not isinstance(value, (int, float)):
                    raise ValueError(f"sample metric {component}.{name} is invalid")
                flattened.setdefault(f"{component}.{name}", []).append((float(elapsed), float(value)))
    return flattened


def _theil_sen_per_hour(points: Sequence[tuple[float, float]]) -> float:
    slopes: list[float] = []
    for left in range(len(points)):
        for right in range(left + 1, len(points)):
            dt = points[right][0] - points[left][0]
            if dt > 0:
                slopes.append((points[right][1] - points[left][1]) * 3600.0 / dt)
    return statistics.median(slopes) if slopes else 0.0


def _metric_noise_floor(metric: str, first: float) -> float:
    if metric.endswith("_bytes"):
        return max(8 * MIB, abs(first) * 0.05)
    if metric.endswith((".fds", ".tasks", ".threads", ".pids_current")):
        return 1.0
    return 0.0


def _metric_summary(metric: str, points: Sequence[tuple[float, float]]) -> dict[str, Any]:
    ordered = sorted(points)
    values = [value for _, value in ordered]
    first = values[0]
    last = values[-1]
    deltas = [values[index] - values[index - 1] for index in range(1, len(values))]
    positive_fraction = sum(delta > 0 for delta in deltas) / len(deltas) if deltas else 0.0
    window = max(1, len(values) // 4)
    early = statistics.median(values[:window])
    late = statistics.median(values[-window:])
    noise = _metric_noise_floor(metric, first)
    slope = _theil_sen_per_hour(ordered)
    sustained = (
        metric not in NON_GROWTH_METRICS
        and len(values) >= 6
        and slope > 0
        and last - first > noise
        and late - early > noise
        and positive_fraction >= 0.6
    )
    semantics = "current_observation"
    if metric == "cgroup.memory_peak_bytes":
        semantics = "historical_high_water_mark"
    elif metric.endswith("cpu_time_ticks") or metric == "cgroup.cpu_usage_usec":
        semantics = "cumulative_cpu_time"
    return {
        "samples": len(values),
        "first": int(first) if first.is_integer() else first,
        "last": int(last) if last.is_integer() else last,
        "minimum": int(min(values)) if min(values).is_integer() else min(values),
        "maximum": int(max(values)) if max(values).is_integer() else max(values),
        "net_growth": int(last - first) if (last - first).is_integer() else last - first,
        "theil_sen_per_hour": slope,
        "positive_delta_fraction": positive_fraction,
        "sustained_positive": sustained,
        "noise_floor": noise,
        "semantics": semantics,
    }


def _candidate_priority(metric: str) -> tuple[int, float]:
    component, name = metric.split(".", 1)
    name_priority = {"pss_bytes": 0, "rss_bytes": 1, "fds": 2, "tasks": 3, "threads": 4, "memory_current_bytes": 5, "pids_current": 6}
    component_priority = {"xray": 0, "podlazd": 1, "cgroup": 2}
    return (name_priority.get(name, 99) * 10 + component_priority.get(component, 9), 0.0)


def summarize_samples(samples: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    flattened = _flatten_samples(samples)
    if not flattened:
        raise ValueError("no first-session active samples")
    metrics = {name: _metric_summary(name, points) for name, points in sorted(flattened.items())}
    candidates = [
        name
        for name, summary in metrics.items()
        if summary["sustained_positive"] and name not in NON_GROWTH_METRICS
    ]
    candidates.sort(key=_candidate_priority)
    return {
        "schema_version": SCHEMA_VERSION,
        "active_samples": max(summary["samples"] for summary in metrics.values()),
        "metrics": metrics,
        "growth_candidates": candidates,
        "reproduced_growth_candidate": candidates[0] if candidates else None,
    }


def evaluate_policy(trend: Mapping[str, Any], policy: Mapping[str, Any]) -> dict[str, Any]:
    mode = policy.get("mode", "observe")
    if mode not in {"observe", "accept"}:
        raise ValueError("unsupported soak policy mode")
    if mode == "observe":
        return {"mode": mode, "ok": True, "evaluated": False, "violations": []}

    metrics = trend.get("metrics")
    candidates = trend.get("growth_candidates")
    target = policy.get("reproduced_growth_signal")
    limits = policy.get("metric_limits")
    if not isinstance(metrics, Mapping) or not isinstance(candidates, list):
        raise ValueError("trend summary is invalid")
    if not isinstance(target, str) or target not in metrics:
        raise ValueError("acceptance policy has no valid reproduced growth signal")
    if not isinstance(limits, Mapping) or target not in limits:
        raise ValueError("acceptance policy has no target metric rule")

    violations: dict[str, list[str]] = {}
    for metric, raw_limit in limits.items():
        if metric not in metrics or not isinstance(raw_limit, Mapping):
            raise ValueError(f"acceptance policy metric is invalid: {metric}")
        summary = metrics[metric]
        if not isinstance(summary, Mapping):
            raise ValueError(f"trend metric is invalid: {metric}")
        reasons: list[str] = []
        max_slope = raw_limit.get("max_theil_sen_per_hour")
        max_growth = raw_limit.get("max_net_growth")
        require_no_sustained = raw_limit.get("require_no_sustained_positive", metric == target)
        if not isinstance(max_slope, (int, float)) or max_slope < 0:
            raise ValueError(f"metric-specific slope limit is invalid: {metric}")
        if not isinstance(max_growth, (int, float)) or max_growth < 0:
            raise ValueError(f"metric-specific growth limit is invalid: {metric}")
        if summary.get("theil_sen_per_hour", 0) > max_slope:
            reasons.append("slope")
        if summary.get("net_growth", 0) > max_growth:
            reasons.append("net_growth")
        if require_no_sustained and summary.get("sustained_positive") is True:
            reasons.append("sustained_positive")
        if reasons:
            violations[metric] = reasons

    for candidate in candidates:
        if isinstance(candidate, str) and candidate not in limits:
            violations.setdefault(candidate, []).append("no_metric_specific_rule")

    return {
        "mode": mode,
        "ok": not violations,
        "evaluated": True,
        "reproduced_growth_signal": target,
        "violations": violations,
    }


def compare_reconnect_boundaries(
    *,
    initial: Mapping[str, Any],
    reconnect: Mapping[str, Any],
    memory_tolerance_bytes: int,
    count_tolerance: int,
) -> dict[str, Any]:
    if memory_tolerance_bytes < 0 or count_tolerance < 0:
        raise ValueError("negative reconnect tolerance")
    violations: list[str] = []
    count_metrics = (
        "cgroup.pids_current",
        "podlazd.threads",
        "podlazd.tasks",
        "podlazd.fds",
        "xray.threads",
        "xray.tasks",
        "xray.fds",
    )
    memory_metrics = (
        "cgroup.memory_current_bytes",
        "podlazd.rss_bytes",
        "podlazd.pss_bytes",
        "xray.rss_bytes",
        "xray.pss_bytes",
    )

    def lookup(root: Mapping[str, Any], path: str) -> int | float | None:
        component, metric = path.split(".", 1)
        values = root.get(component)
        return values.get(metric) if isinstance(values, Mapping) else None

    for metric in count_metrics:
        before = lookup(initial, metric)
        after = lookup(reconnect, metric)
        if not isinstance(before, (int, float)) or not isinstance(after, (int, float)) or after > before + count_tolerance:
            violations.append(metric)
    for metric in memory_metrics:
        before = lookup(initial, metric)
        after = lookup(reconnect, metric)
        if before is None and after is None:
            continue
        if not isinstance(before, (int, float)) or not isinstance(after, (int, float)) or after > before + memory_tolerance_bytes:
            violations.append(metric)
    return {
        "ok": not violations,
        "memory_tolerance_bytes": memory_tolerance_bytes,
        "count_tolerance": count_tolerance,
        "violations": sorted(violations),
    }


def compare_cleanup_boundaries(
    *,
    baseline: Mapping[str, Any],
    cleanup: Mapping[str, Any],
    memory_tolerance_bytes: int,
) -> dict[str, Any]:
    if memory_tolerance_bytes < 0:
        raise ValueError("negative cleanup memory tolerance")
    violations: list[str] = []
    strict = (
        "podlazd.threads",
        "podlazd.tasks",
        "podlazd.fds",
        "cgroup.pids_current",
    )
    memory = (
        "podlazd.rss_bytes",
        "podlazd.pss_bytes",
        "cgroup.memory_current_bytes",
    )

    def lookup(root: Mapping[str, Any], path: str) -> int | float | None:
        component, metric = path.split(".", 1)
        values = root.get(component)
        return values.get(metric) if isinstance(values, Mapping) else None

    for metric in strict:
        before = lookup(baseline, metric)
        after = lookup(cleanup, metric)
        if not isinstance(before, (int, float)) or not isinstance(after, (int, float)) or after > before:
            violations.append(metric)
    for metric in memory:
        before = lookup(baseline, metric)
        after = lookup(cleanup, metric)
        if before is None and after is None:
            continue
        if not isinstance(before, (int, float)) or not isinstance(after, (int, float)) or after > before + memory_tolerance_bytes:
            violations.append(metric)
    return {
        "ok": not violations,
        "memory_tolerance_bytes": memory_tolerance_bytes,
        "violations": sorted(violations),
    }


PUBLIC_PROVENANCE_FIELDS = frozenset(
    {
        "podlaz_version",
        "podlaz_commit",
        "xray_version",
        "xray_artifact_sha256",
        "xray_binary_sha256",
        "kernel_release",
        "systemd_version",
        "package_sha256",
        "package_architecture",
    }
)
PUBLIC_CONFIGURATION_FIELDS = frozenset(
    {
        "duration_seconds",
        "warmup_seconds",
        "sample_interval_seconds",
        "doctor_every_samples",
        "doctor_runs",
        "doctor_unhealthy_runs",
        "reconnect_samples",
        "tun_diagnostic_timeout_seconds",
        "tun_health_timeout_seconds",
    }
)


def _public_provenance(value: Mapping[str, Any]) -> dict[str, str]:
    unknown = sorted(set(value) - PUBLIC_PROVENANCE_FIELDS)
    if unknown:
        raise ValueError(f"unsupported provenance field: {unknown[0]}")
    required = PUBLIC_PROVENANCE_FIELDS
    missing = sorted(required - set(value))
    if missing:
        raise ValueError(f"missing provenance field: {missing[0]}")
    result: dict[str, str] = {}
    for name in sorted(required):
        item = value.get(name)
        if not isinstance(item, str) or not item or len(item) > 128:
            raise ValueError(f"invalid provenance field: {name}")
        if any(character not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._+~-() " for character in item):
            raise ValueError(f"unsafe provenance field: {name}")
        result[name] = item
    for digest_name in ("package_sha256", "xray_artifact_sha256", "xray_binary_sha256"):
        digest = result[digest_name]
        if len(digest) != 64 or any(character not in "0123456789abcdef" for character in digest.lower()):
            raise ValueError(f"invalid provenance field: {digest_name}")
    if result["package_architecture"] not in {"amd64", "arm64"}:
        raise ValueError("invalid provenance field: package_architecture")
    return result


def _public_configuration(value: Mapping[str, Any]) -> dict[str, int]:
    unknown = sorted(set(value) - PUBLIC_CONFIGURATION_FIELDS)
    if unknown:
        raise ValueError(f"unsupported configuration field: {unknown[0]}")
    result: dict[str, int] = {}
    for name in sorted(PUBLIC_CONFIGURATION_FIELDS):
        item = value.get(name)
        if not isinstance(item, int) or isinstance(item, bool):
            raise ValueError(f"invalid configuration field: {name}")
        if name == "doctor_unhealthy_runs":
            if item < 0:
                raise ValueError(f"invalid configuration field: {name}")
        elif item <= 0:
            raise ValueError(f"invalid configuration field: {name}")
        result[name] = item
    if result["doctor_unhealthy_runs"] > result["doctor_runs"]:
        raise ValueError("invalid configuration field: doctor_unhealthy_runs")
    return result


def _aggregate_boundary_samples(
    samples: Sequence[Mapping[str, Any]],
    *,
    phase: str,
    session: int,
) -> dict[str, Any]:
    selected = [sample for sample in samples if sample.get("phase") == phase and sample.get("session") == session]
    if not selected:
        raise ValueError(f"no {phase} samples for session {session}")
    result: dict[str, Any] = {}
    for component, names in (("cgroup", CGROUP_METRICS), ("podlazd", PROCESS_METRICS), ("xray", PROCESS_METRICS)):
        columns: dict[str, list[float]] = {name: [] for name in names}
        for sample in selected:
            values = sample.get(component)
            if not isinstance(values, Mapping):
                raise ValueError(f"sample {component} metrics are invalid")
            for name in names:
                value = values.get(name)
                if value is None:
                    continue
                if not isinstance(value, (int, float)) or isinstance(value, bool):
                    raise ValueError(f"sample metric {component}.{name} is invalid")
                columns[name].append(float(value))
        aggregated: dict[str, int | float | None] = {}
        for name, values in columns.items():
            if not values:
                aggregated[name] = None
                continue
            median = statistics.median(values)
            aggregated[name] = int(median) if median.is_integer() else median
        result[component] = aggregated
    return result


def build_report(
    *,
    active_samples: Sequence[Mapping[str, Any]],
    reconnect_samples: Sequence[Mapping[str, Any]],
    baseline_boundary: Mapping[str, Any],
    cleanup_boundary: Mapping[str, Any],
    provenance: Mapping[str, Any],
    configuration: Mapping[str, Any],
    policy: Mapping[str, Any],
    cleanup_memory_tolerance_bytes: int,
    reconnect_memory_tolerance_bytes: int,
    reconnect_count_tolerance: int,
) -> dict[str, Any]:
    """Build one compact public report from sanitized structural evidence."""

    if baseline_boundary.get("phase") != "inactive-baseline" or baseline_boundary.get("xray") is not None:
        raise ValueError("inactive baseline boundary is invalid")
    if cleanup_boundary.get("phase") != "post-cleanup" or cleanup_boundary.get("xray") is not None:
        raise ValueError("post-cleanup boundary is invalid")

    trend = summarize_samples(active_samples)
    policy_result = evaluate_policy(trend, policy)
    reconnect_count = len(
        [sample for sample in reconnect_samples if sample.get("phase") == "reconnect" and sample.get("session") == 2]
    )
    if reconnect_count <= 0:
        raise ValueError("no reconnect samples")
    initial_reference = _aggregate_boundary_samples(
        list(active_samples)[:reconnect_count],
        phase="active",
        session=1,
    )
    reconnect_reference = _aggregate_boundary_samples(
        reconnect_samples,
        phase="reconnect",
        session=2,
    )
    cleanup_result = compare_cleanup_boundaries(
        baseline=baseline_boundary,
        cleanup=cleanup_boundary,
        memory_tolerance_bytes=cleanup_memory_tolerance_bytes,
    )
    reconnect_result = compare_reconnect_boundaries(
        initial=initial_reference,
        reconnect=reconnect_reference,
        memory_tolerance_bytes=reconnect_memory_tolerance_bytes,
        count_tolerance=reconnect_count_tolerance,
    )
    lifecycle_ok = cleanup_result["ok"] and reconnect_result["ok"]
    mode = policy_result["mode"]
    if mode == "observe":
        ok = lifecycle_ok
        verdict = "observation_complete" if ok else "lifecycle_failed"
    else:
        ok = lifecycle_ok and policy_result["ok"]
        verdict = "acceptance_passed" if ok else "acceptance_failed"

    return {
        "schema_version": SCHEMA_VERSION,
        "ok": ok,
        "verdict": verdict,
        "provenance": _public_provenance(provenance),
        "configuration": _public_configuration(configuration),
        "trend": trend,
        "lifecycle": {
            "cleanup": cleanup_result,
            "reconnect": reconnect_result,
        },
        "policy": policy_result,
    }
