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

CANONICAL_ACCEPTANCE_MIN_POST_WARMUP_SECONDS = 3 * 60 * 60
CANONICAL_ACCEPTANCE_MIN_WARMUP_SECONDS = 120
CANONICAL_ACCEPTANCE_MAX_SAMPLE_INTERVAL_SECONDS = 60
CANONICAL_ACCEPTANCE_MAX_OBSERVED_SAMPLE_GAP_SECONDS = 10 * 60
ACCEPTANCE_GATE_FIELDS = frozenset(
    {
        "minimum_post_warmup_duration_seconds",
        "minimum_warmup_seconds",
        "maximum_sample_interval_seconds",
        "maximum_observed_sample_gap_seconds",
    }
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
    metric_name = metric.rsplit(".", 1)[-1]
    if metric_name == "fds" or metric_name.endswith("_fds") or metric_name in {"tasks", "threads", "pids_current"}:
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
    name_priority = {
        "pss_bytes": 0,
        "rss_bytes": 1,
        "tcp_established_socket_fds": 2,
        "tcp_other_socket_fds": 3,
        "udp_connected_socket_fds": 4,
        "udp_unconnected_socket_fds": 5,
        "udp_other_socket_fds": 6,
        "unix_socket_fds": 7,
        "unclassified_socket_fds": 8,
        "tcp_socket_fds": 9,
        "udp_socket_fds": 10,
        "socket_fds": 11,
        "fds": 12,
        "pipe_fds": 13,
        "anon_inode_fds": 14,
        "regular_fds": 15,
        "other_fds": 16,
        "tasks": 17,
        "threads": 18,
        "memory_current_bytes": 19,
        "pids_current": 20,
    }
    component_priority = {"xray": 0, "podlazd": 1, "cgroup": 2}
    return (name_priority.get(name, 99) * 10 + component_priority.get(component, 9), 0.0)


def _active_elapsed_coordinates(samples: Sequence[Mapping[str, Any]]) -> list[float]:
    coordinates: list[float] = []
    for sample in samples:
        if sample.get("phase") != "active" or sample.get("session") != 1:
            continue
        elapsed = sample.get("elapsed_seconds")
        if not isinstance(elapsed, (int, float)) or isinstance(elapsed, bool) or elapsed < 0:
            raise ValueError("sample elapsed_seconds is invalid")
        coordinates.append(float(elapsed))
    coordinates.sort()
    if not coordinates:
        raise ValueError("no first-session active samples")
    if len(set(coordinates)) != len(coordinates):
        raise ValueError("active sample elapsed_seconds is duplicated")
    return coordinates


def summarize_samples(samples: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    flattened = _flatten_samples(samples)
    coordinates = _active_elapsed_coordinates(samples)
    if not flattened:
        raise ValueError("no first-session active samples")
    metrics = {name: _metric_summary(name, points) for name, points in sorted(flattened.items())}
    candidates = [
        name
        for name, summary in metrics.items()
        if summary["sustained_positive"] and name not in NON_GROWTH_METRICS
    ]
    candidates.sort(key=_candidate_priority)
    first_elapsed = coordinates[0]
    last_elapsed = coordinates[-1]
    observed_duration = last_elapsed - first_elapsed
    observed_gaps = [
        coordinates[index] - coordinates[index - 1]
        for index in range(1, len(coordinates))
    ]
    maximum_observed_gap = max(observed_gaps, default=0.0)
    return {
        "schema_version": SCHEMA_VERSION,
        "active_samples": len(coordinates),
        "first_elapsed_seconds": int(first_elapsed) if first_elapsed.is_integer() else first_elapsed,
        "last_elapsed_seconds": int(last_elapsed) if last_elapsed.is_integer() else last_elapsed,
        "observed_duration_seconds": int(observed_duration) if observed_duration.is_integer() else observed_duration,
        "maximum_observed_sample_gap_seconds": (
            int(maximum_observed_gap)
            if maximum_observed_gap.is_integer()
            else maximum_observed_gap
        ),
        "metrics": metrics,
        "growth_candidates": candidates,
        "reproduced_growth_candidate": candidates[0] if candidates else None,
    }


def _acceptance_gate_result(
    *,
    trend: Mapping[str, Any],
    policy: Mapping[str, Any],
    configuration: Mapping[str, Any],
) -> dict[str, Any]:
    raw_gate = policy.get("acceptance_gate")
    if not isinstance(raw_gate, Mapping):
        raise ValueError("acceptance policy has no acceptance_gate contract")
    unknown = sorted(set(raw_gate) - ACCEPTANCE_GATE_FIELDS)
    missing = sorted(ACCEPTANCE_GATE_FIELDS - set(raw_gate))
    if unknown:
        raise ValueError(f"unsupported acceptance_gate field: {unknown[0]}")
    if missing:
        raise ValueError(f"missing acceptance_gate field: {missing[0]}")

    minimum_duration = raw_gate.get("minimum_post_warmup_duration_seconds")
    minimum_warmup = raw_gate.get("minimum_warmup_seconds")
    maximum_interval = raw_gate.get("maximum_sample_interval_seconds")
    maximum_observed_gap = raw_gate.get("maximum_observed_sample_gap_seconds")
    values = (minimum_duration, minimum_warmup, maximum_interval, maximum_observed_gap)
    if any(not isinstance(value, int) or isinstance(value, bool) or value <= 0 for value in values):
        raise ValueError("acceptance_gate values must be positive integers")
    if (
        minimum_duration < CANONICAL_ACCEPTANCE_MIN_POST_WARMUP_SECONDS
        or minimum_warmup < CANONICAL_ACCEPTANCE_MIN_WARMUP_SECONDS
        or maximum_interval > CANONICAL_ACCEPTANCE_MAX_SAMPLE_INTERVAL_SECONDS
        or maximum_observed_gap > CANONICAL_ACCEPTANCE_MAX_OBSERVED_SAMPLE_GAP_SECONDS
    ):
        raise ValueError("acceptance_gate is weaker than the canonical three-hour gate")

    configured_duration = configuration.get("duration_seconds")
    configured_warmup = configuration.get("warmup_seconds")
    configured_interval = configuration.get("sample_interval_seconds")
    observed_duration = trend.get("observed_duration_seconds")
    observed_sample_gap = trend.get("maximum_observed_sample_gap_seconds")
    if any(
        not isinstance(value, (int, float)) or isinstance(value, bool)
        for value in (
            configured_duration,
            configured_warmup,
            configured_interval,
            observed_duration,
            observed_sample_gap,
        )
    ):
        raise ValueError("acceptance gate evidence is invalid")

    violations: list[str] = []
    if configured_duration < minimum_duration:
        violations.append("configured_post_warmup_duration")
    if observed_duration < minimum_duration:
        violations.append("observed_post_warmup_duration")
    if configured_warmup < minimum_warmup:
        violations.append("warmup_duration")
    if configured_interval <= 0 or configured_interval > maximum_interval:
        violations.append("sample_interval")
    if observed_sample_gap < 0 or observed_sample_gap > maximum_observed_gap:
        violations.append("observed_sample_gap")
    return {
        "ok": not violations,
        "requirements": {
            "minimum_post_warmup_duration_seconds": minimum_duration,
            "minimum_warmup_seconds": minimum_warmup,
            "maximum_sample_interval_seconds": maximum_interval,
            "maximum_observed_sample_gap_seconds": maximum_observed_gap,
        },
        "evidence": {
            "configured_post_warmup_duration_seconds": configured_duration,
            "observed_post_warmup_duration_seconds": observed_duration,
            "configured_warmup_seconds": configured_warmup,
            "configured_sample_interval_seconds": configured_interval,
            "maximum_observed_sample_gap_seconds": observed_sample_gap,
        },
        "violations": violations,
    }


def evaluate_policy(
    trend: Mapping[str, Any],
    policy: Mapping[str, Any],
    *,
    configuration: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    mode = policy.get("mode", "observe")
    if mode not in {"observe", "accept"}:
        raise ValueError("unsupported soak policy mode")
    if mode == "observe":
        return {"mode": mode, "ok": True, "evaluated": False, "violations": []}
    if not isinstance(configuration, Mapping):
        raise ValueError("acceptance policy evaluation requires run configuration")

    gate_result = _acceptance_gate_result(
        trend=trend,
        policy=policy,
        configuration=configuration,
    )
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
        "ok": gate_result["ok"] and not violations,
        "evaluated": True,
        "reproduced_growth_signal": target,
        "acceptance_gate": gate_result,
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
        "precondition_warmup_seconds",
        "warmup_seconds",
        "sample_interval_seconds",
        "doctor_every_samples",
        "doctor_runs",
        "doctor_unhealthy_runs",
        "reconnect_samples",
        "tun_diagnostic_timeout_seconds",
        "tun_health_timeout_seconds",
        "tun_status_timeout_seconds",
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
    reconnect_cleanup_boundary: Mapping[str, Any],
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
    if reconnect_cleanup_boundary.get("phase") != "post-cleanup" or reconnect_cleanup_boundary.get("xray") is not None:
        raise ValueError("reconnect post-cleanup boundary is invalid")

    public_provenance = _public_provenance(provenance)
    public_configuration = _public_configuration(configuration)
    trend = summarize_samples(active_samples)
    policy_result = evaluate_policy(
        trend,
        policy,
        configuration=public_configuration,
    )
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
    first_cleanup_result = compare_cleanup_boundaries(
        baseline=baseline_boundary,
        cleanup=cleanup_boundary,
        memory_tolerance_bytes=cleanup_memory_tolerance_bytes,
    )
    second_cleanup_result = compare_cleanup_boundaries(
        baseline=baseline_boundary,
        cleanup=reconnect_cleanup_boundary,
        memory_tolerance_bytes=cleanup_memory_tolerance_bytes,
    )
    cleanup_result = {
        "ok": first_cleanup_result["ok"] and second_cleanup_result["ok"],
        "comparison": "warmed-inactive-baseline-to-each-measured-cleanup",
        "warmed_baseline": "preconditioned",
        "measured_session_one": first_cleanup_result,
        "measured_session_two": second_cleanup_result,
    }
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
        "provenance": public_provenance,
        "configuration": public_configuration,
        "trend": trend,
        "lifecycle": {
            "cleanup": cleanup_result,
            "reconnect": reconnect_result,
        },
        "policy": policy_result,
    }
