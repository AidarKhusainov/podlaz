from __future__ import annotations

import json
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "tun-resource-soak.sh"
WORKFLOW = Path(__file__).resolve().parents[3] / ".github" / "workflows" / "e2e-tun-resource-soak.yml"
POLICY = Path(__file__).resolve().parents[1] / "tun-resource-soak-policy.json"
ARCHITECTURE = Path(__file__).resolve().parents[3] / "ARCHITECTURE.md"


class TunResourceSoakContractTests(unittest.TestCase):
    def script_text(self) -> str:
        return SCRIPT.read_text(encoding="utf-8")

    def function_body(self, name: str, next_marker: str) -> str:
        text = self.script_text()
        start = text.index(f"{name}() {{")
        end = text.index(next_marker, start)
        return text[start:end]

    def test_checked_in_policy_remains_observation_until_repeated_baselines_exist(self) -> None:
        policy = json.loads(POLICY.read_text(encoding="utf-8"))

        self.assertEqual(2, policy["schema_version"])
        self.assertEqual("observe", policy["mode"])
        self.assertIsNone(policy["reproduced_growth_signal"])
        self.assertEqual({}, policy["metric_limits"])

    def test_warmed_inactive_baseline_precedes_both_measured_sessions(self) -> None:
        text = self.script_text()
        precondition_call = text.rindex("\nprecondition_warmed_inactive_baseline\n")
        measured_connect = text.rindex('SOAK_PHASE="session-one-connect"')
        reconnect_call = text.rindex("\nrun_reconnect_probe\n")
        baseline_sample = text.index('--output "${BASELINE_BOUNDARY}"', text.index("precondition_warmed_inactive_baseline() {"))
        self.assertLess(baseline_sample, precondition_call)
        self.assertLess(precondition_call, measured_connect)
        self.assertLess(measured_connect, reconnect_call)
        self.assertIn('BASELINE_BOUNDARY="${E2E_ARTIFACT_DIR}/tun-resource-warmed-inactive-baseline.json"', text)

    def test_structural_network_isolation_is_captured_and_revalidated_throughout_soak(self) -> None:
        text = self.script_text()
        self.assertIn('ISOLATION_TOOL="${SCRIPT_DIR}/lib/tun_soak_isolation.py"', text)
        self.assertIn('NETWORK_ISOLATION_BASELINE="${SOAK_PRIVATE_DIR}/network-isolation-baseline.json"', text)
        self.assertIn('capture_network_isolation_baseline', text)
        active = self.function_body("run_active_soak", "\n}\n\nrun_reconnect_probe")
        reconnect = self.function_body("run_reconnect_probe", "\n}\n\nwrite_public_report")
        cleanup = self.function_body("disconnect_and_sample_cleanup", "\n}\n\nrun_active_soak")
        self.assertIn('assert_network_isolation active "${SESSION_ONE_NETWORK_MANIFEST}"', active)
        self.assertIn('assert_network_isolation reconnect "${SESSION_TWO_NETWORK_MANIFEST}"', reconnect)
        self.assertIn('assert_network_isolation post-cleanup', cleanup)
        isolation = (Path(__file__).resolve().parents[1] / "lib" / "tun_soak_isolation.py").read_text(encoding="utf-8")
        self.assertIn("validate_clean_baseline", isolation)
        self.assertIn("strip_exact_podlaz_state", isolation)
        self.assertIn("DEFAULT_RULE_LAYOUT", isolation)
        self.assertIn("_validate_canonical_default_rules", isolation)
        self.assertIn("DEDICATED_UPLINK_LINK_TYPE", isolation)
        self.assertIn("_is_positive_physical_link", isolation)
        self.assertIn("RULE_RAW_FIELDS", isolation)
        self.assertIn("ROUTE_RAW_FIELDS", isolation)
        self.assertIn("_reject_unknown_raw_fields", isolation)
        self.assertIn("_validate_non_main_routes", isolation)
        self.assertIn("return dict(actual) == dict(expected)", isolation)
        self.assertIn('"addresses": _json_command(("ip", "-j", "address", "show"))', isolation)
        self.assertIn('os.stat("/proc/self/ns/net")', isolation)

    def test_preconditioning_and_measured_sessions_keep_one_daemon_and_replace_each_child(self) -> None:
        text = self.script_text()
        self.assertIn('WARMED_DAEMON_PID="${after_pid}"', text)
        self.assertIn('--before "${PRECONDITION_IDENTITY}"', text)
        self.assertIn('--after "${SESSION_ONE_IDENTITY}"', text)
        self.assertIn('podlazd restarted during the first measured lifecycle', text)
        self.assertIn('podlazd restarted before reconnect attribution', text)
        self.assertIn('podlazd restarted during reconnect lifecycle', text)

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
        connect = text.rindex('SOAK_PHASE="session-one-connect"')
        discover = text.index('--output "${SESSION_ONE_IDENTITY}"', connect)
        warmup = text.index('sleep "${PODLAZ_E2E_SOAK_WARMUP_SECONDS}"', discover)
        sample = text.index('--output "${ACTIVE_SAMPLES}"', warmup)
        self.assertLess(connect, discover)
        self.assertLess(discover, warmup)
        self.assertLess(warmup, sample)

    def test_samples_exact_cgroup_daemon_and_supervised_xray_without_ps_parsing(self) -> None:
        text = self.script_text()
        self.assertIn('${METRICS_TOOL}" discover', text)
        self.assertIn('${METRICS_TOOL}" sample', text)
        self.assertNotIn("ps -", text)
        self.assertNotIn("/proc/${", text)
        process_metrics = (Path(__file__).resolve().parents[1] / "lib" / "tun_soak_process.py").read_text(encoding="utf-8")
        self.assertIn("memory.current", process_metrics)
        self.assertIn("smaps_rollup", process_metrics)

    def test_active_loop_generates_bounded_traffic_and_read_only_health(self) -> None:
        body = self.function_body("run_active_soak", "\n}\n\nrun_reconnect_probe")
        probe = self.function_body("run_bounded_data_plane_probe", "\n}\n\nprecondition_warmed_inactive_baseline")
        self.assertIn("run_bounded_data_plane_probe active", body)
        self.assertIn("resolvectl --cache=no --interface=podlaz0", probe)
        self.assertIn("curl -4 -fsS --max-time", probe)
        self.assertIn('wait_for_verified_tun_status "${label}"', probe)
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
        self.assertIn("run_tun_status_command", health)
        status_command = health[health.index("run_tun_status_command() {"):health.index("\n}", health.index("run_tun_status_command() {"))]
        self.assertIn("run_installed_podlaz_bounded", status_command)
        self.assertNotIn("run_installed_podlaz status", health)
        self.assertIn("PODLAZ_E2E_TUN_STATUS_TIMEOUT_SECONDS", health)
        self.assertIn('3)', health)
        self.assertIn('wait_for_verified_tun_status post-connect', text)
        self.assertIn('run_bounded_data_plane_probe active', text)
        self.assertIn('run_bounded_data_plane_probe reconnect', text)
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
        append = self.function_body("append_sensitive_value", "\n}\n\nenforce_acceptance_inputs")
        self.assertNotIn("::", append)
        inventory = self.function_body("collect_host_sensitive_values", "\n}\n\nfirst_profile_uri")
        self.assertIn("nmcli", inventory)

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

    def test_acceptance_policy_requires_canonical_duration_warmup_and_actual_cadence(self) -> None:
        analysis = (Path(__file__).resolve().parents[1] / "lib" / "tun_soak_analysis.py").read_text(encoding="utf-8")
        self.assertIn("CANONICAL_ACCEPTANCE_MIN_POST_WARMUP_SECONDS = 3 * 60 * 60", analysis)
        self.assertIn("CANONICAL_ACCEPTANCE_MIN_WARMUP_SECONDS = 120", analysis)
        self.assertIn("CANONICAL_ACCEPTANCE_MAX_SAMPLE_INTERVAL_SECONDS = 60", analysis)
        self.assertIn("CANONICAL_ACCEPTANCE_MAX_OBSERVED_SAMPLE_GAP_SECONDS = 10 * 60", analysis)
        self.assertIn('"observed_duration_seconds"', analysis)
        self.assertIn('"maximum_observed_sample_gap_seconds"', analysis)
        self.assertIn("acceptance_gate is weaker than the canonical three-hour gate", analysis)

    def test_workflow_is_manual_self_hosted_and_does_not_block_ordinary_ci(self) -> None:
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("workflow_dispatch:", text)
        self.assertIn("self-hosted", text)
        self.assertIn("vpn-e2e", text)
        self.assertIn("ubuntu-24.04", text)
        self.assertNotIn("pull_request:", text)
        self.assertIn("tun-resource-soak.sh", text)
        self.assertIn("PODLAZ_E2E_SOAK_DURATION_SECONDS", text)

    def test_canonical_architecture_routes_soak_details_to_executable_contracts(self) -> None:
        architecture = ARCHITECTURE.read_text(encoding="utf-8")
        self.assertIn("scripts/**`, `.github/workflows/**`, and `packaging/**`: executable package/release/E2E contract", architecture)
        self.assertIn("Hosted tests validate pure/unit/contract behavior without privileged host mutation", architecture)
        self.assertIn("Dedicated package/E2E scenarios validate installed-package", architecture)
        self.assertIn("Shared E2E infrastructure belongs in `scripts/e2e/lib/**`", architecture)
        self.assertIn("Scenario-specific predicates and resource cleanup remain local when their semantics differ", architecture)
        self.assertIn("Destructive host-network E2E must remain explicitly gated to the dedicated runner", architecture)

    def test_root_private_trusted_host_preflight_uses_sudo_stat_boundary(self) -> None:
        text = self.script_text()
        self.assertIn('sudo -n test -f "${PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE}"', text)
        self.assertNotIn('[[ -f "${PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE}" ]]', text)

    def test_trusted_host_and_runtime_os_are_verified_before_baseline_capture(self) -> None:
        text = self.script_text()
        self.assertIn('PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE', text)
        self.assertIn('ENVIRONMENT_TOOL="${SCRIPT_DIR}/lib/tun_soak_environment.py"', text)
        self.assertIn('verify-os --output "${RUNTIME_OS_JSON}"', text)
        capture = self.function_body("capture_network_isolation_baseline", "\n}\n\nassert_network_isolation")
        self.assertIn('--trusted-host "${PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE}"', capture)
        verify = self.function_body("assert_network_isolation", "\n}\n\nrun_bounded_data_plane_probe")
        self.assertIn('--trusted-host "${PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE}"', verify)
        self.assertIn('require_cmd', text)
        self.assertIn('nmcli', text)

    def test_accept_mode_hash_pins_private_policy_snapshot_to_checked_in_head(self) -> None:
        text = self.script_text()
        self.assertIn('CANONICAL_SOAK_POLICY_REPOSITORY_PATH="scripts/e2e/tun-resource-soak-policy.json"', text)
        self.assertIn('SOAK_POLICY_SNAPSHOT="${SOAK_PRIVATE_DIR}/soak-policy.json"', text)
        self.assertIn('install -m 0600 "${PODLAZ_E2E_SOAK_POLICY_FILE}" "${SOAK_POLICY_SNAPSHOT}"', text)
        self.assertIn('SOAK_POLICY_SHA256="$(sha256sum "${SOAK_POLICY_SNAPSHOT}"', text)
        self.assertIn('git show "HEAD:${CANONICAL_SOAK_POLICY_REPOSITORY_PATH}"', text)
        self.assertIn('accept mode requires the exact checked-in HEAD policy', text)
        report = self.function_body("write_public_report", "\n}\n\nwrite_failure_evidence")
        self.assertIn('--policy "${SOAK_POLICY_SNAPSHOT}"', report)
        self.assertNotIn('--policy "${PODLAZ_E2E_SOAK_POLICY_FILE}"', report)

    def test_accept_mode_pins_checked_in_policy_and_removes_global_lifecycle_tolerances(self) -> None:
        text = self.script_text()
        self.assertIn('enforce_acceptance_inputs', text)
        self.assertIn('tun-resource-soak-policy.json', text)
        self.assertNotIn('PODLAZ_E2E_SOAK_CLEANUP_MEMORY_TOLERANCE_BYTES', text)
        self.assertNotIn('PODLAZ_E2E_SOAK_RECONNECT_MEMORY_TOLERANCE_BYTES', text)
        self.assertNotIn('PODLAZ_E2E_SOAK_RECONNECT_COUNT_TOLERANCE', text)
        report = self.function_body("write_public_report", "\n}\n\nwrite_failure_evidence")
        self.assertNotIn('--cleanup-memory-tolerance-bytes', report)
        self.assertNotIn('--reconnect-memory-tolerance-bytes', report)
        self.assertNotIn('--reconnect-count-tolerance', report)

    def test_workflow_passes_canonical_private_trusted_host_path(self) -> None:
        workflow = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn(
            'PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE: /etc/podlaz-e2e/tun-resource-soak-trusted-host.json',
            workflow,
        )


if __name__ == "__main__":
    unittest.main()
