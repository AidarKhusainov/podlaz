#!/usr/bin/env python3
"""Sanitized trend, lifecycle, and policy analysis for TUN soak samples."""

from __future__ import annotations

import math
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

POLICY_SCHEMA_VERSION = 2
CANONICAL_ACCEPTANCE_MIN_POST_WARMUP_SECONDS = 3 * 60 * 60
CANONICAL_ACCEPTANCE_MIN_WARMUP_SECONDS = 120
CANONICAL_ACCEPTANCE_MAX_SAMPLE_INTERVAL_SECONDS = 60
CANONICAL_ACCEPTANCE_MAX_OBSERVED_SAMPLE_GAP_SECONDS = 10 * 60
CANONICAL_ACCEPTANCE_MIN_METRIC_SAMPLES = (
    math.ceil(
        CANONICAL_ACCEPTANCE_MIN_POST_WARMUP_SECONDS
        / CANONICAL_ACCEPTANCE_MAX_OBSERVED_SAMPLE_GAP_SECONDS
    )
    + 1
)
ACCEPTANCE_GATE_FIELDS = frozenset(
    {
        "minimum_post_warmup_duration_seconds",
        "minimum_warmup_seconds",
        "maximum_sample_interval_seconds",
        "maximum_observed_sample_gap_seconds",
        "minimum_metric_samples",
    }
)
ACCEPTANCE_CONFIGURATION_FIELDS = frozenset(
    {
        "duration_seconds",
        "precondition_warmup_seconds",
        "warmup_seconds",
        "sample_interval_seconds",
        "doctor_every_samples",
        "reconnect_warmup_seconds",
        "reconnect_samples",
        "cleanup_settle_seconds",
        "tun_diagnostic_timeout_seconds",
        "tun_health_timeout_seconds",
        "tun_health_poll_seconds",
        "tun_status_timeout_seconds",
        "cleanup_attempts",
        "cleanup_retry_seconds",
    }
)
METRIC_LIMIT_FIELDS = frozenset(
    {
        "max_theil_sen_per_hour",
        "max_net_growth",
        "max_positive_delta_fraction",
        "require_no_sustained_positive",
    }
)
LIFECYCLE_RULE_FIELDS = frozenset({"max_increase", "required"})
OBSERVE_POLICY_FIELDS = frozenset(
    {
        "schema_version",
        "mode",
        "reproduced_growth_signal",
        "metric_limits",
        "lifecycle_limits",
    }
)
ACCEPT_POLICY_FIELDS = OBSERVE_POLICY_FIELDS | frozenset(
    {"acceptance_gate", "acceptance_configuration"}
)

GROWTH_CGROUP_METRICS = tuple(
    metric for metric in CGROUP_METRICS if f"cgroup.{metric}" not in NON_GROWTH_METRICS
)
GROWTH_PROCESS_METRICS = tuple(
    metric for metric in PROCESS_METRICS if f"podlazd.{metric}" not in NON_GROWTH_METRICS
)
CLEANUP_LIFECYCLE_METRICS = tuple(
    sorted(
        [f"cgroup.{metric}" for metric in GROWTH_CGROUP_METRICS]
        + [f"podlazd.{metric}" for metric in GROWTH_PROCESS_METRICS]
    )
)
RECONNECT_LIFECYCLE_METRICS = tuple(
    sorted(
        list(CLEANUP_LIFECYCLE_METRICS)
        + [f"xray.{metric}" for metric in GROWTH_PROCESS_METRICS]
    )
)
LIFECYCLE_METRICS = {
    "cleanup": CLEANUP_LIFECYCLE_METRICS,
    "reconnect": RECONNECT_LIFECYCLE_METRICS,
}


def _is_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def _flatten_samples(samples: Sequence[Mapping[str, Any]]) -> dict[str, list[tuple[float, float]]]:
    flattened: dict[str, list[tuple[float, float]]] = {}
    for sample in samples:
        if sample.get("phase") != "active" or sample.get("session") != 1:
            continue
        elapsed = sample.get("elapsed_seconds")
        if not _is_number(elapsed) or elapsed < 0:
            raise ValueError("sample elapsed_seconds is invalid")
        for component, names in (
            ("cgroup", CGROUP_METRICS),
            ("podlazd", PROCESS_METRICS),
            ("xray", PROCESS_METRICS),
        ):
            values = sample.get(component)
            if not isinstance(values, Mapping):
                raise ValueError(f"sample {component} metrics are invalid")
            for name in names:
                value = values.get(name)
                if value is None:
                    continue
                if not _is_number(value):
                    raise ValueError(f"sample metric {component}.{name} is invalid")
                flattened.setdefault(f"{component}.{name}", []).append(
                    (float(elapsed), float(value))
                )
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
    if (
        metric_name == "fds"
        or metric_name.endswith("_fds")
        or metric_name in {"tasks", "threads", "pids_current"}
    ):
        return 1.0
    return 0.0


def _as_public_number(value: float) -> int | float:
    return int(value) if value.is_integer() else value


def _metric_summary(metric: str, points: Sequence[tuple[float, float]]) -> dict[str, Any]:
    ordered = sorted(points)
    if not ordered:
        raise ValueError(f"metric has no samples: {metric}")
    coordinates = [elapsed for elapsed, _ in ordered]
    if len(coordinates) != len(set(coordinates)):
        raise ValueError(f"metric has duplicate sample timestamps: {metric}")
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
    gaps = [coordinates[index] - coordinates[index - 1] for index in range(1, len(coordinates))]
    observed_duration = coordinates[-1] - coordinates[0]
    maximum_gap = max(gaps, default=0.0)
    return {
        "samples": len(values),
        "first": _as_public_number(first),
        "last": _as_public_number(last),
        "minimum": _as_public_number(min(values)),
        "maximum": _as_public_number(max(values)),
        "early_median": _as_public_number(float(early)),
        "late_median": _as_public_number(float(late)),
        "net_growth": _as_public_number(last - first),
        "theil_sen_per_hour": slope,
        "positive_delta_fraction": positive_fraction,
        "sustained_positive": sustained,
        "noise_floor": noise,
        "semantics": semantics,
        "first_elapsed_seconds": _as_public_number(coordinates[0]),
        "last_elapsed_seconds": _as_public_number(coordinates[-1]),
        "observed_duration_seconds": _as_public_number(observed_duration),
        "maximum_observed_sample_gap_seconds": _as_public_number(maximum_gap),
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
        if not _is_number(elapsed) or elapsed < 0:
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
    growth_metrics = sorted(name for name in metrics if name not in NON_GROWTH_METRICS)
    candidates = [
        name
        for name in growth_metrics
        if metrics[name]["sustained_positive"]
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
        "first_elapsed_seconds": _as_public_number(first_elapsed),
        "last_elapsed_seconds": _as_public_number(last_elapsed),
        "observed_duration_seconds": _as_public_number(observed_duration),
        "maximum_observed_sample_gap_seconds": _as_public_number(maximum_observed_gap),
        "metrics": metrics,
        "growth_metrics": growth_metrics,
        "growth_candidates": candidates,
        "reproduced_growth_candidate": candidates[0] if candidates else None,
    }


def _positive_int(value: Any, label: str) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value <= 0:
        raise ValueError(f"{label} must be a positive integer")
    return value


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

    minimum_duration = _positive_int(
        raw_gate.get("minimum_post_warmup_duration_seconds"),
        "minimum_post_warmup_duration_seconds",
    )
    minimum_warmup = _positive_int(raw_gate.get("minimum_warmup_seconds"), "minimum_warmup_seconds")
    maximum_interval = _positive_int(
        raw_gate.get("maximum_sample_interval_seconds"),
        "maximum_sample_interval_seconds",
    )
    maximum_observed_gap = _positive_int(
        raw_gate.get("maximum_observed_sample_gap_seconds"),
        "maximum_observed_sample_gap_seconds",
    )
    minimum_metric_samples = _positive_int(
        raw_gate.get("minimum_metric_samples"),
        "minimum_metric_samples",
    )
    implied_minimum_samples = math.ceil(minimum_duration / maximum_observed_gap) + 1
    if (
        minimum_duration < CANONICAL_ACCEPTANCE_MIN_POST_WARMUP_SECONDS
        or minimum_warmup < CANONICAL_ACCEPTANCE_MIN_WARMUP_SECONDS
        or maximum_interval > CANONICAL_ACCEPTANCE_MAX_SAMPLE_INTERVAL_SECONDS
        or maximum_observed_gap > CANONICAL_ACCEPTANCE_MAX_OBSERVED_SAMPLE_GAP_SECONDS
        or minimum_metric_samples < CANONICAL_ACCEPTANCE_MIN_METRIC_SAMPLES
        or minimum_metric_samples < implied_minimum_samples
    ):
        raise ValueError("acceptance_gate is weaker than the canonical three-hour gate")

    configured_duration = configuration.get("duration_seconds")
    configured_warmup = configuration.get("warmup_seconds")
    configured_interval = configuration.get("sample_interval_seconds")
    observed_duration = trend.get("observed_duration_seconds")
    observed_sample_gap = trend.get("maximum_observed_sample_gap_seconds")
    values = (
        configured_duration,
        configured_warmup,
        configured_interval,
        observed_duration,
        observed_sample_gap,
    )
    if any(not _is_number(value) for value in values):
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
            "minimum_metric_samples": minimum_metric_samples,
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


def _acceptance_configuration_result(
    *,
    policy: Mapping[str, Any],
    configuration: Mapping[str, Any],
) -> dict[str, Any]:
    expected = policy.get("acceptance_configuration")
    if not isinstance(expected, Mapping):
        raise ValueError("acceptance policy has no reviewed configuration contract")
    unknown = sorted(set(expected) - ACCEPTANCE_CONFIGURATION_FIELDS)
    missing = sorted(ACCEPTANCE_CONFIGURATION_FIELDS - set(expected))
    if unknown:
        raise ValueError(f"unsupported acceptance configuration field: {unknown[0]}")
    if missing:
        raise ValueError(f"missing acceptance configuration field: {missing[0]}")
    normalized: dict[str, int] = {}
    for name in sorted(ACCEPTANCE_CONFIGURATION_FIELDS):
        normalized[name] = _positive_int(expected.get(name), f"acceptance configuration {name}")
    violations: list[str] = []
    for name, expected_value in normalized.items():
        actual = configuration.get(name)
        if not isinstance(actual, int) or isinstance(actual, bool) or actual != expected_value:
            violations.append(name)
    return {
        "ok": not violations,
        "expected": normalized,
        "violations": violations,
    }


def _metric_evidence_reasons(summary: Mapping[str, Any], gate: Mapping[str, Any]) -> list[str]:
    requirements = gate.get("requirements")
    if not isinstance(requirements, Mapping):
        raise ValueError("acceptance gate requirements are invalid")
    samples = summary.get("samples")
    duration = summary.get("observed_duration_seconds")
    maximum_gap = summary.get("maximum_observed_sample_gap_seconds")
    if not isinstance(samples, int) or isinstance(samples, bool) or samples < 0:
        raise ValueError("metric sample count is invalid")
    if not _is_number(duration) or not _is_number(maximum_gap):
        raise ValueError("metric timing evidence is invalid")
    reasons: list[str] = []
    if samples < requirements["minimum_metric_samples"]:
        reasons.append("metric_sample_count")
    if duration < requirements["minimum_post_warmup_duration_seconds"]:
        reasons.append("metric_observed_duration")
    if maximum_gap < 0 or maximum_gap > requirements["maximum_observed_sample_gap_seconds"]:
        reasons.append("metric_observed_sample_gap")
    return reasons


def evaluate_policy(
    trend: Mapping[str, Any],
    policy: Mapping[str, Any],
    *,
    configuration: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    if policy.get("schema_version") != POLICY_SCHEMA_VERSION:
        raise ValueError("unsupported soak policy schema")
    mode = policy.get("mode", "observe")
    if mode not in {"observe", "accept"}:
        raise ValueError("unsupported soak policy mode")
    allowed_fields = OBSERVE_POLICY_FIELDS if mode == "observe" else ACCEPT_POLICY_FIELDS
    unknown = sorted(set(policy) - allowed_fields)
    missing = sorted(allowed_fields - set(policy))
    if unknown:
        raise ValueError(f"unsupported soak policy field: {unknown[0]}")
    if missing:
        raise ValueError(f"missing soak policy field: {missing[0]}")

    if mode == "observe":
        if policy.get("reproduced_growth_signal") is not None or policy.get("metric_limits") != {}:
            raise ValueError("observe policy must remain uncalibrated")
        return {"mode": mode, "ok": True, "evaluated": False, "violations": {}}
    if not isinstance(configuration, Mapping):
        raise ValueError("acceptance policy evaluation requires run configuration")

    metrics = trend.get("metrics")
    if not isinstance(metrics, Mapping) or not metrics:
        raise ValueError("trend summary is invalid")
    derived_growth_metrics = sorted(metric for metric in metrics if metric not in NON_GROWTH_METRICS)
    declared_growth_metrics = trend.get("growth_metrics", derived_growth_metrics)
    if declared_growth_metrics != derived_growth_metrics:
        raise ValueError("trend growth-metric inventory is inconsistent")

    target = policy.get("reproduced_growth_signal")
    if not isinstance(target, str) or not target:
        raise ValueError("acceptance policy has no valid reproduced growth signal")
    if target in NON_GROWTH_METRICS:
        raise ValueError("reproduced growth signal cannot be a non-growth metric")
    if target not in metrics:
        raise ValueError("acceptance policy reproduced growth signal is not observed")

    limits = policy.get("metric_limits")
    if not isinstance(limits, Mapping):
        raise ValueError("acceptance policy metric limits are invalid")
    if set(limits) != set(derived_growth_metrics):
        raise ValueError("acceptance policy must define a complete growth-metric rule set")

    gate_result = _acceptance_gate_result(
        trend=trend,
        policy=policy,
        configuration=configuration,
    )
    configuration_result = _acceptance_configuration_result(
        policy=policy,
        configuration=configuration,
    )

    violations: dict[str, list[str]] = {}
    for metric in derived_growth_metrics:
        raw_limit = limits.get(metric)
        summary = metrics.get(metric)
        if not isinstance(raw_limit, Mapping) or not isinstance(summary, Mapping):
            raise ValueError(f"acceptance policy metric is invalid: {metric}")
        unknown_rule = sorted(set(raw_limit) - METRIC_LIMIT_FIELDS)
        missing_rule = sorted(METRIC_LIMIT_FIELDS - set(raw_limit))
        if unknown_rule:
            raise ValueError(f"unsupported metric-specific field for {metric}: {unknown_rule[0]}")
        if missing_rule:
            raise ValueError(f"missing metric-specific field for {metric}: {missing_rule[0]}")
        max_slope = raw_limit.get("max_theil_sen_per_hour")
        max_growth = raw_limit.get("max_net_growth")
        max_positive_fraction = raw_limit.get("max_positive_delta_fraction")
        require_no_sustained = raw_limit.get("require_no_sustained_positive")
        if not _is_number(max_slope) or max_slope < 0:
            raise ValueError(f"metric-specific slope limit is invalid: {metric}")
        if not _is_number(max_growth) or max_growth < 0:
            raise ValueError(f"metric-specific growth limit is invalid: {metric}")
        if not _is_number(max_positive_fraction) or not 0 <= max_positive_fraction <= 1:
            raise ValueError(f"metric-specific positive-delta limit is invalid: {metric}")
        if require_no_sustained is not True:
            raise ValueError(f"metric-specific require_no_sustained_positive must be true: {metric}")
        if summary.get("semantics") != "current_observation":
            raise ValueError(f"acceptance policy metric is not a current-growth metric: {metric}")

        reasons = _metric_evidence_reasons(summary, gate_result)
        slope = summary.get("theil_sen_per_hour")
        net_growth = summary.get("net_growth")
        positive_fraction = summary.get("positive_delta_fraction")
        if not _is_number(slope) or not _is_number(net_growth) or not _is_number(positive_fraction):
            raise ValueError(f"trend metric is invalid: {metric}")
        if slope > max_slope:
            reasons.append("slope")
        if net_growth > max_growth:
            reasons.append("net_growth")
        if positive_fraction > max_positive_fraction:
            reasons.append("positive_delta_fraction")
        if summary.get("sustained_positive") is True:
            reasons.append("sustained_positive")
        if reasons:
            violations[metric] = sorted(set(reasons))

    return {
        "mode": mode,
        "ok": gate_result["ok"] and configuration_result["ok"] and not violations,
        "evaluated": True,
        "reproduced_growth_signal": target,
        "acceptance_gate": gate_result,
        "configuration_contract": configuration_result,
        "violations": violations,
    }


def _validated_lifecycle_limits(policy: Mapping[str, Any], phase: str) -> dict[str, dict[str, Any]]:
    lifecycle = policy.get("lifecycle_limits")
    if not isinstance(lifecycle, Mapping) or set(lifecycle) != set(LIFECYCLE_METRICS):
        raise ValueError("lifecycle policy must define exact cleanup and reconnect contracts")
    raw_rules = lifecycle.get(phase)
    if not isinstance(raw_rules, Mapping):
        raise ValueError(f"lifecycle policy {phase} rules are invalid")
    expected_metrics = set(LIFECYCLE_METRICS[phase])
    if set(raw_rules) != expected_metrics:
        raise ValueError(f"lifecycle policy {phase} must define a complete metric rule set")
    rules: dict[str, dict[str, Any]] = {}
    for metric in sorted(expected_metrics):
        raw = raw_rules.get(metric)
        if not isinstance(raw, Mapping) or set(raw) != LIFECYCLE_RULE_FIELDS:
            raise ValueError(f"lifecycle metric rule is invalid: {phase}.{metric}")
        max_increase = raw.get("max_increase")
        required = raw.get("required")
        if not _is_number(max_increase) or max_increase < 0:
            raise ValueError(f"lifecycle max increase is invalid: {phase}.{metric}")
        if not isinstance(required, bool):
            raise ValueError(f"lifecycle required flag is invalid: {phase}.{metric}")
        if not required and not metric.endswith(".pss_bytes"):
            raise ValueError(f"only optional PSS evidence may be non-required: {phase}.{metric}")
        rules[metric] = {"max_increase": max_increase, "required": required}
    return rules


def _lookup_metric(root: Mapping[str, Any], path: str) -> int | float | None:
    component, metric = path.split(".", 1)
    values = root.get(component)
    return values.get(metric) if isinstance(values, Mapping) else None


def _compare_boundaries(
    *,
    before: Mapping[str, Any],
    after: Mapping[str, Any],
    rules: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    violations: dict[str, str] = {}
    for metric, rule in sorted(rules.items()):
        before_value = _lookup_metric(before, metric)
        after_value = _lookup_metric(after, metric)
        required = rule["required"]
        if before_value is None and after_value is None and not required:
            continue
        if not _is_number(before_value) or not _is_number(after_value):
            violations[metric] = "missing_or_invalid_evidence"
            continue
        if after_value > before_value + rule["max_increase"]:
            violations[metric] = "increase_exceeds_metric_limit"
    return {
        "ok": not violations,
        "rule_source": "reviewed_policy",
        "metric_rule_count": len(rules),
        "violations": violations,
    }


def compare_reconnect_boundaries(
    *,
    initial: Mapping[str, Any],
    reconnect: Mapping[str, Any],
    metric_limits: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    return _compare_boundaries(before=initial, after=reconnect, rules=metric_limits)


def compare_cleanup_boundaries(
    *,
    baseline: Mapping[str, Any],
    cleanup: Mapping[str, Any],
    metric_limits: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    return _compare_boundaries(before=baseline, after=cleanup, rules=metric_limits)


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
        "runtime_os",
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
        "reconnect_warmup_seconds",
        "reconnect_samples",
        "cleanup_settle_seconds",
        "tun_diagnostic_timeout_seconds",
        "tun_health_timeout_seconds",
        "tun_health_poll_seconds",
        "tun_status_timeout_seconds",
        "cleanup_attempts",
        "cleanup_retry_seconds",
    }
)


def _safe_provenance_string(name: str, value: Any) -> str:
    if not isinstance(value, str) or not value or len(value) > 128:
        raise ValueError(f"invalid provenance field: {name}")
    allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._+~-() "
    if any(character not in allowed for character in value):
        raise ValueError(f"unsafe provenance field: {name}")
    return value


def _public_provenance(value: Mapping[str, Any]) -> dict[str, Any]:
    unknown = sorted(set(value) - PUBLIC_PROVENANCE_FIELDS)
    missing = sorted(PUBLIC_PROVENANCE_FIELDS - set(value))
    if unknown:
        raise ValueError(f"unsupported provenance field: {unknown[0]}")
    if missing:
        raise ValueError(f"missing provenance field: {missing[0]}")
    result: dict[str, Any] = {}
    for name in sorted(PUBLIC_PROVENANCE_FIELDS - {"runtime_os"}):
        result[name] = _safe_provenance_string(name, value.get(name))
    runtime_os = value.get("runtime_os")
    if not isinstance(runtime_os, Mapping) or set(runtime_os) != {"id", "version_id"}:
        raise ValueError("invalid provenance field: runtime_os")
    normalized_os = {
        "id": _safe_provenance_string("runtime_os.id", runtime_os.get("id")),
        "version_id": _safe_provenance_string("runtime_os.version_id", runtime_os.get("version_id")),
    }
    if normalized_os != {"id": "ubuntu", "version_id": "24.04"}:
        raise ValueError("invalid provenance field: runtime_os")
    result["runtime_os"] = normalized_os
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
    missing = sorted(PUBLIC_CONFIGURATION_FIELDS - set(value))
    if missing:
        raise ValueError(f"missing configuration field: {missing[0]}")
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
    for component, names in (
        ("cgroup", CGROUP_METRICS),
        ("podlazd", PROCESS_METRICS),
        ("xray", PROCESS_METRICS),
    ):
        columns: dict[str, list[float]] = {name: [] for name in names}
        for sample in selected:
            values = sample.get(component)
            if not isinstance(values, Mapping):
                raise ValueError(f"sample {component} metrics are invalid")
            for name in names:
                value = values.get(name)
                if value is None:
                    continue
                if not _is_number(value):
                    raise ValueError(f"sample metric {component}.{name} is invalid")
                columns[name].append(float(value))
        aggregated: dict[str, int | float | None] = {}
        for name, values in columns.items():
            if not values:
                aggregated[name] = None
                continue
            median = statistics.median(values)
            aggregated[name] = _as_public_number(median)
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
    policy_sha256: str,
) -> dict[str, Any]:
    """Build one compact public report from sanitized structural evidence."""

    if baseline_boundary.get("phase") != "inactive-baseline" or baseline_boundary.get("xray") is not None:
        raise ValueError("inactive baseline boundary is invalid")
    if cleanup_boundary.get("phase") != "post-cleanup" or cleanup_boundary.get("xray") is not None:
        raise ValueError("post-cleanup boundary is invalid")
    if reconnect_cleanup_boundary.get("phase") != "post-cleanup" or reconnect_cleanup_boundary.get("xray") is not None:
        raise ValueError("reconnect post-cleanup boundary is invalid")

    if (
        not isinstance(policy_sha256, str)
        or len(policy_sha256) != 64
        or any(character not in "0123456789abcdef" for character in policy_sha256)
    ):
        raise ValueError("policy digest is not a lowercase SHA-256 value")

    public_provenance = _public_provenance(provenance)
    public_configuration = _public_configuration(configuration)
    trend = summarize_samples(active_samples)
    policy_result = {
        **evaluate_policy(trend, policy, configuration=public_configuration),
        "sha256": policy_sha256,
    }
    cleanup_limits = _validated_lifecycle_limits(policy, "cleanup")
    reconnect_limits = _validated_lifecycle_limits(policy, "reconnect")

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
        metric_limits=cleanup_limits,
    )
    second_cleanup_result = compare_cleanup_boundaries(
        baseline=baseline_boundary,
        cleanup=reconnect_cleanup_boundary,
        metric_limits=cleanup_limits,
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
        metric_limits=reconnect_limits,
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
