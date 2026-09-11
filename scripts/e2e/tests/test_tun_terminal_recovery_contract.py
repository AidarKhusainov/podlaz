from __future__ import annotations

import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "tun-terminal-recovery.sh"
PACKAGE_RESTART_SCRIPT = Path(__file__).resolve().parents[1] / "tun-package-restart-recovery.sh"
PACKAGE_RUNTIME_PROVENANCE = Path(__file__).resolve().parents[1] / "lib" / "package_runtime_provenance.sh"


class TunTerminalRecoveryContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.text = SCRIPT.read_text(encoding="utf-8")
        cls.package_restart_text = PACKAGE_RESTART_SCRIPT.read_text(encoding="utf-8")
        cls.package_runtime_provenance_text = PACKAGE_RUNTIME_PROVENANCE.read_text(encoding="utf-8")

    def function_body(self, name: str, next_marker: str, text: str | None = None) -> str:
        source = self.text if text is None else text
        start = source.index(f"{name}() {{")
        end = source.index(next_marker, start)
        return source[start:end]

    def test_connectivity_probe_is_bounded_ipv4_dns_and_https(self) -> None:
        require_block = self.text[self.text.index("require_cmd ") : self.text.index("\n\n: \"${PODLAZ_E2E_PROFILE_URI", self.text.index("require_cmd "))]
        self.assertIn("timeout", require_block.split())

        body = self.function_body("check_https_and_dns", "\n}\n\ncapture_host_state")
        self.assertIn("getent ahostsv4", body)
        self.assertIn("timeout", body)
        self.assertIn("curl -4", body)
        self.assertIn("--connect-timeout", body)
        self.assertIn("--max-time", body)
        self.assertIn('[[ "${PODLAZ_E2E_HTTPS_CHECK_URL}" == https://* ]]', self.text)

    def test_candidate_active_boundaries_probe_real_traffic_before_teardown(self) -> None:
        flow = self.text[self.text.index('log "connect exact candidate TUN"') :]

        initial_active = flow.index("assert_active_authority_present candidate-active")
        initial_probe = flow.index("check_https_and_dns candidate-active-vpn", initial_active)
        inject = flow.index('log "inject terminal firewall rollback blocker"', initial_active)
        self.assertLess(initial_active, initial_probe)
        self.assertLess(initial_probe, inject)

        reconnect = flow.index("assert_active_authority_present candidate-reconnect")
        reconnect_probe = flow.index("check_https_and_dns candidate-reconnect-vpn", reconnect)
        reconnect_disconnect = flow.index('run_client disconnect', reconnect)
        self.assertLess(reconnect, reconnect_probe)
        self.assertLess(reconnect_probe, reconnect_disconnect)

        upgrade_reconnect = flow.index("assert_active_authority_present upgrade-reconnect")
        upgrade_probe = flow.index("check_https_and_dns upgrade-reconnect-vpn", upgrade_reconnect)
        upgrade_disconnect = flow.index('run_client disconnect', upgrade_reconnect)
        self.assertLess(upgrade_reconnect, upgrade_probe)
        self.assertLess(upgrade_probe, upgrade_disconnect)

    def test_post_convergence_diagnostics_are_daemon_backed_and_forget_terminal_failure(self) -> None:
        body = self.function_body("assert_post_convergence_diagnostics", "\n}\n\nassert_v0240_stranded_shape")
        self.assertIn('run_client doctor', body)
        self.assertIn('Source: daemon', body)
        self.assertIn('run_client status', body)
        self.assertIn('Status: Disconnected', body)
        self.assertIn('assert_clean_recovery_view', body)
        self.assertIn('assert_not_contains', body)

        flow = self.text[self.text.index('log "recover terminal state in same daemon without reboot/service restart"') :]
        same_daemon = flow.index('assert_post_convergence_diagnostics after-recover "terminal firewall rollback blocked before nftables mutation"')
        second_recover = flow.index('log "prove recovery is idempotent"')
        self.assertLess(same_daemon, second_recover)

        upgrade = flow.index('assert_post_convergence_diagnostics after-v0240-upgrade-recover "missing nftables chains"')
        upgrade_lifecycle = flow.index('log "prove post-upgrade normal lifecycle and route/rule cleanup"')
        self.assertLess(upgrade, upgrade_lifecycle)

    def test_networkmanager_postcondition_is_conditional_bounded_private_evidence(self) -> None:
        body = self.function_body("assert_networkmanager_tun_absent", "\n}\n\nassert_post_convergence_diagnostics")
        self.assertIn("command -v nmcli", body)
        self.assertIn("systemctl is-active --quiet NetworkManager.service", body)
        self.assertIn("capture_secret_command", body)
        self.assertIn("timeout", body)
        self.assertIn("nmcli", body)
        self.assertIn("connection show --active", body)
        self.assertIn('grep -Fx -- "${TUN_IFACE}"', body)

    def test_v0240_package_restart_candidate_install_has_no_service_repair(self) -> None:
        text = self.package_restart_text
        body = self.function_body(
            "install_candidate_package_replacement",
            "\n}\n\nassert_v0240_package_restart_failure",
            text,
        )
        self.assertIn("apt install", body)
        self.assertIn("wait_for_daemon_socket", body)
        self.assertNotIn("systemctl start", body)
        self.assertNotIn("systemctl restart", body)
        self.assertEqual(body.count("apt install"), 1)

    def test_v0240_package_restart_requires_historical_failure_and_resume_intent(self) -> None:
        text = self.package_restart_text
        body = self.function_body(
            "assert_v0240_package_restart_failure",
            "\n}\n\nclassify_package_restart_candidate",
            text,
        )
        for required in (
            "missing nftables chains",
            "intent",
            "resume",
            "journalctl",
            "V0240_PRE_CHILD_PID",
            "V0240_PRE_CHILD_START",
            "assert_original_process_absent",
        ):
            self.assertIn(required, body)

    def test_v0240_package_restart_pins_public_release_bytes(self) -> None:
        text = self.package_restart_text
        for required in (
            "c9d8f76838292d39355506123e2f03ca1f0a96227fb2c22af8324ac6baf3b278",
            "8a86c439cc86fb075b66f58ae16eb99baaf35118d9f9b6ddc05a9235b1b57250",
            "V0240_ACTUAL_SHA256",
            "assert_exact_package_runtime_provenance",
        ):
            self.assertIn(required, text)
        self.assertIn(
            "ab71c876d558a7653d44d5b98c76f3899e569a90",
            self.package_runtime_provenance_text,
        )

    def test_v0240_package_restart_terminal_outcome_requires_typed_replay_evidence(self) -> None:
        text = self.package_restart_text
        for required in (
            "network-session-resume.json",
            "terminal-data-plane-clean.ready",
            "terminal-data-plane-clean.continue",
            "PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE=true",
            "PACKAGE_RESTART_TYPED_TERMINAL",
        ):
            self.assertIn(required, text)

        evidence = self.function_body(
            "capture_typed_terminal_replay_evidence",
            "\n}\n\nwait_for_candidate_start_boundary",
            text,
        )
        for required in (
            "replay_disposition",
            "terminal",
            "session_id",
            "recovery_epoch",
            "candidate_mutation",
            "network_apply_subphase",
        ):
            self.assertIn(required, evidence)

        classify = self.function_body(
            "classify_package_restart_candidate",
            "\n}\n\nassert_clean_recovery_view",
            text,
        )
        self.assertIn("PACKAGE_RESTART_TYPED_TERMINAL", classify)
        self.assertIn("untyped terminal", classify)
        self.assertNotIn(
            "elif status.get('connection')=='inactive' and not cleanup and not committed and not session:\n    print('terminal')",
            classify,
        )

        flow = text[text.index("capture_v0240_package_restart_authority") :]
        arm = flow.index("install_candidate_terminal_evidence_pause")
        install = flow.index('install_candidate_package_replacement "${CANDIDATE}"', arm)
        self.assertLess(arm, install)

    def test_v0240_package_restart_flow_proves_traffic_and_terminal_idempotence(self) -> None:
        text = self.package_restart_text
        flow = text[text.index('log "reproduce exact v0.2.40 package-restart resume boundary"') :]
        pre_active = flow.index("wait_for_verified_active v0.2.40-package-restart-active")
        pre_traffic = flow.index("check_https_and_dns v0.2.40-package-restart-vpn", pre_active)
        install = flow.index('install_candidate_package_replacement "${CANDIDATE}"', pre_traffic)
        historical = flow.index("assert_v0240_package_restart_failure", install)
        classify = flow.index("classify_package_restart_candidate", historical)
        self.assertLess(pre_active, pre_traffic)
        self.assertLess(pre_traffic, install)
        self.assertLess(install, historical)
        self.assertLess(historical, classify)
        self.assertIn("check_https_and_dns package-restart-resumed-vpn", flow)
        self.assertIn("check_https_and_dns package-restart-terminal-ordinary", flow)
        self.assertIn('run_client recover --execute --yes', flow)
        self.assertIn("assert_terminal_clean package-restart-first-recovery", flow)
        self.assertIn("assert_terminal_clean package-restart-second-recovery", flow)
        clean = self.function_body("assert_terminal_clean", "\n}\n\ncleanup", text)
        self.assertIn("assert_clean_recovery_view", clean)
        self.assertIn("package_restart_second_recovery_clean", flow)
        self.assertIn("assert_tun_foreign_state package-restart-second-recovery", flow)

    def test_v0240_package_restart_terminal_cleanup_proves_exact_owned_resources_absent(self) -> None:
        text = self.package_restart_text
        self.assertIn("lib/tun_package_assertions.sh", text)
        capture = self.function_body(
            "capture_v0240_package_restart_authority",
            "\n}\n\nassert_original_process_absent",
            text,
        )
        for required in (
            "FALLBACK_NETWORK_HELPER",
            "PRIVATE_SOURCE_MANIFEST",
            "snapshot",
            "V0240_PROTECTION_FAMILY",
            "V0240_PROTECTION_TABLE",
        ):
            self.assertIn(required, capture)

        clean = self.function_body("assert_terminal_clean", "\n}\n\ncleanup", text)
        for required in (
            "verify_tun_package_resources_absent",
            "FALLBACK_NETWORK_HELPER",
            "PRIVATE_SOURCE_MANIFEST",
            "inspect_nft_table_state",
            "V0240_PROTECTION_FAMILY",
            "V0240_PROTECTION_TABLE",
        ):
            self.assertIn(required, clean)


if __name__ == "__main__":
    unittest.main()
