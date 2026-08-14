from __future__ import annotations

import json
import unittest
from pathlib import Path

from scripts.e2e.lib import tun_soak_analysis

POLICY = Path(__file__).resolve().parents[1] / "tun-resource-soak-policy.json"


class TunSoakPolicyV2Tests(unittest.TestCase):
    @staticmethod
    def configuration() -> dict[str, int | str]:
        return {
            "duration_seconds": 10800,
            "precondition_warmup_seconds": 30,
            "warmup_seconds": 120,
            "sample_interval_seconds": 60,
            "doctor_every_samples": 10,
            "doctor_runs": 18,
            "doctor_unhealthy_runs": 0,
            "reconnect_warmup_seconds": 120,
            "reconnect_samples": 3,
            "cleanup_settle_seconds": 10,
            "tun_diagnostic_timeout_seconds": 90,
            "tun_health_timeout_seconds": 75,
            "tun_health_poll_seconds": 1,
            "tun_status_timeout_seconds": 10,
            "cleanup_attempts": 2,
            "cleanup_retry_seconds": 2,
            "policy_sha256": "a" * 64,
        }

    @staticmethod
    def metric_summary(
        *,
        samples: int = 181,
        duration: int = 10800,
        maximum_gap: int = 60,
        slope: float = 0,
        net_growth: float = 0,
        positive_fraction: float = 0,
        sustained: bool = False,
        semantics: str = "current_observation",
    ) -> dict[str, object]:
        return {
            "samples": samples,
            "first": 10,
            "last": 10 + net_growth,
            "minimum": 10,
            "maximum": 10 + max(0, net_growth),
            "net_growth": net_growth,
            "theil_sen_per_hour": slope,
            "positive_delta_fraction": positive_fraction,
            "sustained_positive": sustained,
            "noise_floor": 0,
            "semantics": semantics,
            "first_elapsed_seconds": 0,
            "last_elapsed_seconds": duration,
            "observed_duration_seconds": duration,
            "maximum_observed_sample_gap_seconds": maximum_gap,
        }

    @classmethod
    def trend(cls, metrics: dict[str, dict[str, object]]) -> dict[str, object]:
        growth_metrics = sorted(
            metric for metric in metrics if metric not in tun_soak_analysis.NON_GROWTH_METRICS
        )
        return {
            "schema_version": 1,
            "active_samples": 181,
            "first_elapsed_seconds": 0,
            "last_elapsed_seconds": 10800,
            "observed_duration_seconds": 10800,
            "maximum_observed_sample_gap_seconds": 60,
            "metrics": metrics,
            "growth_metrics": growth_metrics,
            "growth_candidates": [],
            "reproduced_growth_candidate": None,
        }

    @classmethod
    def accept_policy(
        cls,
        metrics: dict[str, dict[str, object]],
        target: str,
    ) -> dict[str, object]:
        limits = {
            metric: {
                "max_theil_sen_per_hour": 0,
                "max_net_growth": 0,
                "max_positive_delta_fraction": 0,
                "require_no_sustained_positive": True,
            }
            for metric in metrics
            if metric not in tun_soak_analysis.NON_GROWTH_METRICS
        }
        expected_configuration = {
            key: value
            for key, value in cls.configuration().items()
            if key in tun_soak_analysis.ACCEPTANCE_CONFIGURATION_FIELDS
        }
        return {
            "schema_version": 2,
            "mode": "accept",
            "reproduced_growth_signal": target,
            "acceptance_gate": {
                "minimum_post_warmup_duration_seconds": 10800,
                "minimum_warmup_seconds": 120,
                "maximum_sample_interval_seconds": 60,
                "maximum_observed_sample_gap_seconds": 600,
                "minimum_metric_samples": 19,
            },
            "acceptance_configuration": expected_configuration,
            "metric_limits": limits,
            "lifecycle_limits": {"cleanup": {}, "reconnect": {}},
        }

    def test_memory_peak_cannot_be_reproduced_growth_signal(self) -> None:
        metrics = {
            "cgroup.memory_peak_bytes": self.metric_summary(
                semantics="historical_high_water_mark"
            ),
            "xray.fds": self.metric_summary(),
        }
        policy = self.accept_policy(metrics, "cgroup.memory_peak_bytes")

        with self.assertRaisesRegex(ValueError, "non-growth metric"):
            tun_soak_analysis.evaluate_policy(
                self.trend(metrics),
                policy,
                configuration=self.configuration(),
            )

    def test_cumulative_cpu_cannot_be_reproduced_growth_signal(self) -> None:
        metrics = {
            "xray.cpu_time_ticks": self.metric_summary(semantics="cumulative_cpu_time"),
            "xray.fds": self.metric_summary(),
        }
        policy = self.accept_policy(metrics, "xray.cpu_time_ticks")

        with self.assertRaisesRegex(ValueError, "non-growth metric"):
            tun_soak_analysis.evaluate_policy(
                self.trend(metrics),
                policy,
                configuration=self.configuration(),
            )

    def test_accept_rule_cannot_disable_sustained_growth_requirement(self) -> None:
        metrics = {"xray.fds": self.metric_summary()}
        policy = self.accept_policy(metrics, "xray.fds")
        policy["metric_limits"]["xray.fds"]["require_no_sustained_positive"] = False

        with self.assertRaisesRegex(ValueError, "require_no_sustained_positive"):
            tun_soak_analysis.evaluate_policy(
                self.trend(metrics),
                policy,
                configuration=self.configuration(),
            )

    def test_accept_requires_rule_for_every_observed_growth_metric(self) -> None:
        metrics = {
            "podlazd.fds": self.metric_summary(),
            "xray.fds": self.metric_summary(),
        }
        policy = self.accept_policy(metrics, "podlazd.fds")
        del policy["metric_limits"]["xray.fds"]

        with self.assertRaisesRegex(ValueError, "complete growth-metric rule set"):
            tun_soak_analysis.evaluate_policy(
                self.trend(metrics),
                policy,
                configuration=self.configuration(),
            )

    def test_slow_fd_growth_fails_even_when_generic_candidate_is_absent(self) -> None:
        metrics = {
            "podlazd.fds": self.metric_summary(),
            "xray.fds": self.metric_summary(
                slope=1,
                net_growth=3,
                positive_fraction=0.02,
                sustained=False,
            ),
        }
        trend = self.trend(metrics)
        self.assertEqual([], trend["growth_candidates"])
        policy = self.accept_policy(metrics, "podlazd.fds")

        result = tun_soak_analysis.evaluate_policy(
            trend,
            policy,
            configuration=self.configuration(),
        )

        self.assertFalse(result["ok"])
        self.assertEqual(
            ["net_growth", "positive_delta_fraction", "slope"],
            result["violations"]["xray.fds"],
        )

    def test_target_requires_three_hours_of_its_own_evidence(self) -> None:
        metrics = {
            "xray.pss_bytes": self.metric_summary(samples=2, duration=60, maximum_gap=60),
        }
        policy = self.accept_policy(metrics, "xray.pss_bytes")

        result = tun_soak_analysis.evaluate_policy(
            self.trend(metrics),
            policy,
            configuration=self.configuration(),
        )

        self.assertFalse(result["ok"])
        self.assertIn("metric_sample_count", result["violations"]["xray.pss_bytes"])
        self.assertIn("metric_observed_duration", result["violations"]["xray.pss_bytes"])

    def test_policy_limited_metric_rejects_full_duration_with_sparse_gap(self) -> None:
        metrics = {
            "xray.fds": self.metric_summary(samples=2, duration=10800, maximum_gap=10800),
        }
        policy = self.accept_policy(metrics, "xray.fds")

        result = tun_soak_analysis.evaluate_policy(
            self.trend(metrics),
            policy,
            configuration=self.configuration(),
        )

        self.assertFalse(result["ok"])
        self.assertIn("metric_sample_count", result["violations"]["xray.fds"])
        self.assertIn("metric_observed_sample_gap", result["violations"]["xray.fds"])

    def test_accept_rejects_unreviewed_reconnect_configuration(self) -> None:
        metrics = {"xray.fds": self.metric_summary()}
        policy = self.accept_policy(metrics, "xray.fds")
        actual = self.configuration()
        actual["reconnect_samples"] = 2

        result = tun_soak_analysis.evaluate_policy(
            self.trend(metrics),
            policy,
            configuration=actual,
        )

        self.assertFalse(result["ok"])
        self.assertEqual(
            ["reconnect_samples"],
            result["configuration_contract"]["violations"],
        )

    def test_checked_in_observe_policy_has_complete_metric_specific_lifecycle_rules(self) -> None:
        policy = json.loads(POLICY.read_text(encoding="utf-8"))

        self.assertEqual(2, policy["schema_version"])
        self.assertEqual("observe", policy["mode"])
        self.assertIsNone(policy["reproduced_growth_signal"])
        self.assertEqual({}, policy["metric_limits"])
        cleanup = tun_soak_analysis._validated_lifecycle_limits(policy, "cleanup")
        reconnect = tun_soak_analysis._validated_lifecycle_limits(policy, "reconnect")
        self.assertEqual(set(tun_soak_analysis.CLEANUP_LIFECYCLE_METRICS), set(cleanup))
        self.assertEqual(set(tun_soak_analysis.RECONNECT_LIFECYCLE_METRICS), set(reconnect))
        self.assertFalse(cleanup["podlazd.pss_bytes"]["required"])
        self.assertTrue(cleanup["podlazd.fds"]["required"])

    def test_missing_required_lifecycle_metric_fails_closed(self) -> None:
        rules = {
            "podlazd.fds": {"max_increase": 0, "required": True},
            "podlazd.pss_bytes": {"max_increase": 1024, "required": False},
        }
        result = tun_soak_analysis.compare_cleanup_boundaries(
            baseline={"podlazd": {"fds": 10, "pss_bytes": None}},
            cleanup={"podlazd": {"fds": None, "pss_bytes": None}},
            metric_limits=rules,
        )

        self.assertFalse(result["ok"])
        self.assertEqual(
            "missing_or_invalid_evidence",
            result["violations"]["podlazd.fds"],
        )
        self.assertNotIn("podlazd.pss_bytes", result["violations"])


if __name__ == "__main__":
    unittest.main()
