from __future__ import annotations

import json
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "tun-resource-soak.sh"
WORKFLOW = Path(__file__).resolve().parents[3] / ".github" / "workflows" / "e2e-tun-resource-soak.yml"
POLICY = Path(__file__).resolve().parents[1] / "tun-resource-soak-policy.json"


class TunResourceSoakContractTests(unittest.TestCase):
    def script_text(self) -> str:
        return SCRIPT.read_text(encoding="utf-8")

    def function_body(self, name: str, next_marker: str) -> str:
        text = self.script_text()
        start = text.index(f"{name}() {{")
        end = text.index(next_marker, start)
        return text[start:end]

    def test_checked_in_policy_is_calibrated_acceptance(self) -> None:
        policy = json.loads(POLICY.read_text(encoding="utf-8"))

        self.assertEqual(1, policy["schema_version"])
        self.assertEqual("accept", policy["mode"])
        self.assertEqual(
            "xray.tcp_established_socket_fds",
            policy["reproduced_growth_signal"],
        )
        target = policy["metric_limits"]["xray.tcp_established_socket_fds"]
        self.assertEqual(64, target["max_theil_sen_per_hour"])
        self.assertEqual(128, target["max_net_growth"])
        self.assertIs(True, target["require_no_sustained_positive"])
        self.assertIn("xray.rss_bytes", policy["metric_limits"])
        self.assertIn("podlazd.fds", policy["metric_limits"])
        self.assertIn("cgroup.memory_current_bytes", policy["metric_limits"])

    def test_reconnect_records_an_equivalent_post_cleanup_boundary(self) -> None:
        text = self.script_text()
        self.assertIn('RECONNECT_CLEANUP_BOUNDARY="${E2E_ARTIFACT_DIR}/tun-resource-post-reconnect-cleanup.json"', text)
        self.assertIn('--reconnect-cleanup-boundary "${RECONNECT_CLEANUP_BOUNDARY}"', text)
        self.assertGreater(
            text.index('SOAK_PHASE="reconnect-cleanup"'),
            text.index('SOAK_PHASE="post-cleanup"'),
        )
        self.assertGreater(
            text.index('--output "${RECONNECT_CLEANUP_BOUNDARY}"'),
            text.index('SOAK_PHASE="reconnect-cleanup"'),
        )

    def test_package_provenance_uses_the_exact_checked_out_head(self) -> None:
        text = self.script_text()
        self.assertIn('BUILD_COMMIT="$(git rev-parse HEAD)"', text)
        self.assertNotIn('BUILD_COMMIT="${GITHUB_SHA:-$(git rev-parse HEAD)}"', text)

    def test_raw_package_build_and_install_logs_stay_private(self) -> None:
        text = self.script_text()
        self.assertIn('PACKAGE_BUILD_LOG="${SOAK_PRIVATE_DIR}/package-build.log"', text)
        self.assertIn('PACKAGE_INSTALL_LOG="${SOAK_PRIVATE_DIR}/package-install.log"', text)
        self.assertIn('PACKAGE_REINSTALL_LOG="${SOAK_PRIVATE_DIR}/package-reinstall.log"', text)
        self.assertIn('bash scripts/build-deb.sh >"${PACKAGE_BUILD_LOG}" 2>&1', text)
        self.assertIn('apt install -y "./${DEV_DEB}" >"${PACKAGE_INSTALL_LOG}" 2>&1', text)
        self.assertIn('apt install --reinstall -y "./${DEV_DEB}" >"${PACKAGE_REINSTALL_LOG}" 2>&1', text)
        self.assertNotIn('tun-resource-build-deb.log', text)
        self.assertNotIn('tun-resource-apt-install.log', text)
        self.assertNotIn('tun-resource-apt-reinstall.log', text)

    def test_attribution_precedes_warmup_and_first_active_sample(self) -> None:
        text = self.script_text()
        connect = text.index('run_installed_podlaz connect --mode tun "${PROFILE_ID}"')
        discover = text.index('tun_soak_metrics.py" discover', connect)
        warmup = text.index('sleep "${PODLAZ_E2E_SOAK_WARMUP_SECONDS}"', discover)
        sample = text.index('tun_soak_metrics.py" sample', warmup)
        self.assertLess(connect, discover)
        self.assertLess(discover, warmup)
        self.assertLess(warmup, sample)

    def test_samples_exact_cgroup_daemon_and_supervised_xray_without_ps_parsing(self) -> None:
        text = self.script_text()
        self.assertIn('tun_soak_metrics.py" discover', text)
        self.assertIn('tun_soak_metrics.py" sample', text)
        self.assertNotIn("ps -", text)
        self.assertNotIn("/proc/${", text)
        process_metrics = (Path(__file__).resolve().parents[1] / "lib" / "tun_soak_process.py").read_text(encoding="utf-8")
        self.assertIn("memory.current", process_metrics)
        self.assertIn("smaps_rollup", process_metrics)

    def test_active_loop_generates_bounded_traffic_and_read_only_health(self) -> None:
        body = self.function_body("run_active_soak", "\n}\n\nrun_reconnect_probe")
        self.assertIn("resolvectl --cache=no --interface=podlaz0", body)
        self.assertIn("curl -4 -fsS --max-time", body)
        self.assertIn("wait_for_verified_tun_status active", body)
        self.assertIn("run_bounded_tun_diagnostic active", body)
        self.assertIn("PODLAZ_E2E_SOAK_DOCTOR_EVERY_SAMPLES", body)
        self.assertNotIn("&", body)


    def test_status_checks_use_bounded_verified_health_convergence(self) -> None:
        text = self.script_text()
        self.assertIn('source "${SCRIPT_DIR}/lib/tun_soak_health.sh"', text)
        self.assertIn('PODLAZ_E2E_TUN_HEALTH_TIMEOUT_SECONDS', text)
        self.assertIn('PODLAZ_E2E_TUN_DIAGNOSTIC_TIMEOUT_SECONDS', text)
        health = (Path(__file__).resolve().parents[1] / "lib" / "tun_soak_health.sh").read_text(encoding="utf-8")
        self.assertIn("run_bounded_tun_diagnostic", health)
        self.assertIn("run_installed_podlaz_bounded", health)
        self.assertIn('3)', health)
        self.assertIn('wait_for_verified_tun_status post-connect', text)
        self.assertIn('wait_for_verified_tun_status active', text)
        self.assertIn('wait_for_verified_tun_status reconnect', text)
        self.assertNotIn('run_installed_podlaz status', text)
        writer = self.function_body("write_failure_evidence", "\n}\n\ncleanup")
        self.assertIn('"status_verdict"', writer)

    def test_disconnect_proves_exact_child_gone_before_cleanup_sample(self) -> None:
        body = self.function_body("disconnect_and_sample_cleanup", "\n}\n\nrun_active_soak")
        disconnect = body.index("run_installed_podlaz disconnect")
        gone = body.index('tun_soak_metrics.py" assert-gone')
        sample = body.index('tun_soak_metrics.py" boundary-sample')
        self.assertLess(disconnect, gone)
        self.assertLess(gone, sample)

    def test_reconnect_proves_new_child_and_non_cumulative_resources(self) -> None:
        body = self.function_body("run_reconnect_probe", "\n}\n\nwrite_public_report")
        self.assertIn('tun_soak_metrics.py" assert-replaced', body)
        self.assertIn('--phase reconnect', body)
        self.assertIn('tun_soak_metrics.py" assert-gone', body)
        report = self.function_body("write_public_report", "\n}\n\ncleanup")
        self.assertIn('tun_soak_metrics.py" report', report)
        self.assertIn("--reconnect-samples", report)
        self.assertIn("--cleanup-boundary", report)

    def test_host_sensitive_inventory_is_never_emitted_to_workflow_commands(self) -> None:
        text = self.script_text()
        self.assertNotIn("mask_multiline_sensitive", text)
        self.assertNotIn("mask_value", text)
        append = self.function_body("append_sensitive_value", "\n}\n\ncollect_host_sensitive_values")
        self.assertNotIn("::", append)

    def test_failure_evidence_is_structural_and_written_before_private_cleanup(self) -> None:
        text = self.script_text()
        self.assertIn("tun-resource-failure.json", text)
        writer = self.function_body("write_failure_evidence", "\n}\n\ncleanup")
        self.assertIn('"phase"', writer)
        self.assertIn('"harness_exit_code"', writer)
        self.assertIn('"command_exit_code"', writer)
        self.assertNotIn('stdout', writer)
        self.assertNotIn('stderr', writer)
        self.assertNotIn('cmdline', writer)
        cleanup = self.function_body("cleanup", "\n}\n\ntrap cleanup EXIT")
        failure = cleanup.index("write_failure_evidence")
        private_cleanup = cleanup.index('rm -rf -- "${SOAK_PRIVATE_DIR}"')
        self.assertLess(failure, private_cleanup)

    def test_cleanup_retries_privately_before_removing_identity_state(self) -> None:
        text = self.script_text()
        self.assertIn('source "${SCRIPT_DIR}/lib/tun_soak_cleanup.sh"', text)
        cleanup = self.function_body("cleanup", "\n}\n\ntrap cleanup EXIT")
        bounded_cleanup = cleanup.index("run_tun_soak_cleanup final")
        private_cleanup = cleanup.index('rm -rf -- "${SOAK_PRIVATE_DIR}"')
        self.assertLess(bounded_cleanup, private_cleanup)
        helper = (Path(__file__).resolve().parents[1] / "lib" / "tun_soak_cleanup.sh").read_text(encoding="utf-8")
        self.assertIn("PODLAZ_E2E_SOAK_CLEANUP_ATTEMPTS", helper)
        self.assertIn("cleanup-attempt-${attempt}.log", helper)
        self.assertNotIn("cat ", helper)

    def test_failure_report_contains_only_allowlisted_cli_classification(self) -> None:
        text = self.script_text()
        self.assertIn("classify-cli-error", text)
        writer = self.function_body("write_failure_evidence", "\n}\n\ncleanup")
        self.assertIn('"command_classification"', writer)
        self.assertNotIn('command_message', writer)
        self.assertNotIn('raw_error', writer)

    def test_private_identity_and_profile_material_are_removed_before_artifact_scan(self) -> None:
        cleanup = self.function_body("cleanup", "\n}\n\ntrap cleanup EXIT")
        self.assertIn('rm -rf -- "${SOAK_PRIVATE_DIR}"', cleanup)
        text = self.script_text()
        scan = text.index("assert_artifacts_do_not_contain_sensitive_values")
        report = text.index("write_public_report")
        self.assertLess(report, scan)
        self.assertNotIn("cmdline", text)
        self.assertNotIn("transaction_id", text)

    def test_workflow_is_manual_self_hosted_and_does_not_block_ordinary_ci(self) -> None:
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("workflow_dispatch:", text)
        self.assertIn("self-hosted", text)
        self.assertIn("vpn-e2e", text)
        self.assertIn("ubuntu-24.04", text)
        self.assertNotIn("pull_request:", text)
        self.assertIn("tun-resource-soak.sh", text)
        self.assertIn("PODLAZ_E2E_SOAK_DURATION_SECONDS", text)


    def test_canonical_docs_define_release_gate_and_metric_semantics(self) -> None:
        docs_root = Path(__file__).resolve().parents[3] / "docs"
        e2e = (docs_root / "e2e.md").read_text(encoding="utf-8")
        development = (docs_root / "development.md").read_text(encoding="utf-8")
        self.assertIn("TUN Resource Soak E2E", e2e)
        self.assertIn("three-hour post-warm-up", e2e)
        self.assertIn("current memory", e2e)
        self.assertIn("peak memory", e2e)
        self.assertIn("cgroup total", e2e)
        self.assertIn("exact supervised Xray", e2e)
        self.assertIn("direct child", e2e)
        self.assertIn("bounded current-health convergence", e2e)
        self.assertIn("Diagnostic exit `0` and diagnostic exit `3`", e2e)
        self.assertIn("attempted at most twice", e2e)
        self.assertIn("metric-specific", e2e)
        self.assertIn("python3 -m unittest scripts.e2e.tests.test_tun_soak_metrics", development)
        self.assertIn("python3 -m unittest scripts.e2e.tests.test_tun_soak_status", development)
        self.assertIn("python3 -m unittest scripts.e2e.tests.test_tun_soak_health", development)
        self.assertIn("python3 -m unittest scripts.e2e.tests.test_tun_soak_cleanup", development)


if __name__ == "__main__":
    unittest.main()
