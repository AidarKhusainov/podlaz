from __future__ import annotations

import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "tun-package-convergence.sh"


class TunPackageConvergenceContractTests(unittest.TestCase):
    def function_body(self, name: str, next_marker: str) -> str:
        text = SCRIPT.read_text(encoding="utf-8")
        start = text.index(f"{name}() {{")
        end = text.index(next_marker, start)
        return text[start:end]

    def test_missing_link_snapshots_before_release_and_verifies_after_rollback(self) -> None:
        body = self.function_body("run_missing_link_probe", "\n}\n\nsetup_isolated_xdg")
        snapshot = body.index("snapshot_tun_network_manifest")
        release = body.index('touch "${HOOK_CONTINUE}"')
        wait = body.index("wait_connect_bounded")
        verify = body.index("verify_tun_network_manifest_absent")
        resources = body.index("assert_podlaz_resources_absent")
        self.assertLess(snapshot, release)
        self.assertLess(release, wait)
        self.assertLess(wait, verify)
        self.assertLess(verify, resources)

    def test_missing_link_validates_the_production_rollback_capture(self) -> None:
        body = self.function_body("run_missing_link_probe", "\n}\n\nsetup_isolated_xdg")
        self.assertNotIn("sudo -n resolvectl revert podlaz0", body)

        release = body.index('touch "${HOOK_CONTINUE}"')
        wait = body.index("wait_connect_bounded")
        exit_code = body.index('grep -Fx "1" "${DNS_ROLLBACK_EXIT_CODE}"')
        stdout = body.index('test ! -s "${DNS_ROLLBACK_STDOUT}"')
        stderr = body.index('verify_resolvectl_missing_link.py" "${DNS_ROLLBACK_STDERR}"')
        captured_event = body.index("dns-rollback-result-captured")

        self.assertLess(release, wait)
        self.assertLess(wait, exit_code)
        self.assertLess(wait, stdout)
        self.assertLess(wait, stderr)
        self.assertLess(wait, captured_event)

    def test_successful_connects_verify_their_exact_network_manifest_after_disconnect(self) -> None:
        inactive = self.function_body("run_inactive_scope_probe", "\n}\n\nrun_missing_link_probe")
        self.assertLess(
            inactive.index("snapshot_tun_network_manifest"),
            inactive.index("run_installed_podlaz disconnect"),
        )
        self.assertLess(
            inactive.index("run_installed_podlaz disconnect"),
            inactive.index("verify_tun_network_manifest_absent"),
        )

        missing = self.function_body("run_missing_link_probe", "\n}\n\nsetup_isolated_xdg")
        retry_connect = missing.rindex('run_installed_podlaz connect --mode tun "${PROFILE_ID}"')
        retry_snapshot = missing.index("snapshot_tun_network_manifest", retry_connect)
        retry_disconnect = missing.index("run_installed_podlaz disconnect", retry_snapshot)
        retry_verify = missing.index("verify_tun_network_manifest_absent", retry_disconnect)
        self.assertLess(retry_connect, retry_snapshot)
        self.assertLess(retry_snapshot, retry_disconnect)
        self.assertLess(retry_disconnect, retry_verify)

    def test_successful_sessions_require_exact_address_and_scoped_dns(self) -> None:
        inactive = self.function_body("run_inactive_scope_probe", "\n}\n\nrun_missing_link_probe")
        self.assertIn("assert_tun_package_address_present", inactive)
        self.assertIn("verify_tun_scoped_dns_query", inactive)
        self.assertLess(
            inactive.index("assert_tun_package_address_present"),
            inactive.index("verify_tun_scoped_dns_query"),
        )

        missing = self.function_body("run_missing_link_probe", "\n}\n\nsetup_isolated_xdg")
        retry_connect = missing.rindex('run_installed_podlaz connect --mode tun "${PROFILE_ID}"')
        retry_address = missing.index("assert_tun_package_address_present", retry_connect)
        retry_disconnect = missing.index("run_installed_podlaz disconnect", retry_address)
        self.assertLess(retry_connect, retry_address)
        self.assertLess(retry_address, retry_disconnect)

    def test_foreign_address_conflict_blocks_before_mutation(self) -> None:
        body = self.function_body("run_foreign_address_conflict_probe", "\n}\n\nrun_inactive_scope_probe")
        self.assertIn('ip address add "${TUN_PACKAGE_ADDRESS_CIDR}"', body)
        self.assertIn("tun_address_conflict", body)
        self.assertIn("assert_podlaz_resources_absent", body)
        self.assertLess(body.index("ip address add"), body.index("run_installed_podlaz connect"))
        self.assertLess(body.index("run_installed_podlaz connect"), body.index("assert_podlaz_resources_absent"))


if __name__ == "__main__":
    unittest.main()
