"""Guest qualification must activate only after the legacy L3 owner is gone."""
from pathlib import Path
import subprocess
import unittest

SCRIPT = Path(__file__).with_name('nm_vpn_l3_guest_harness.sh')


class GateAHostIsolation(unittest.TestCase):
    def test_isolated_guest_pipeline_stops_legacy_before_nm(self):
        source = SCRIPT.read_text()
        self.assertIn('source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"', source)
        self.assertIn('PODLAZ_E2E_SYNTHETIC_PROTOCOL=vmess', source)
        self.assertIn('start_system_guest', source)
        self.assertIn('qualify_explicit_proxy_only', source)
        self.assertIn('assert_verified_active_authority', source)
        self.assertIn('nm-l3-generated-xray.json', source)
        self.assertLess(source.index('run_guest_user /usr/bin/podlaz disconnect'),
                        source.index('nm_vpn_l3_scenario_guest.py'))
        self.assertIn('assert_terminal_authority_clean', source)
        self.assertIn('tun.nm_l3_gate_a', source)
        self.assertIn('nm-l3-failure-protection', source)
        self.assertIn('nm-l3-invalid-ip4-rejected', source)
        self.assertIn('nm-l3-uplink-churn-protected', source)
        self.assertIn('nm-l3-ipv6-physical-block', source)
        self.assertIn('PODLAZ_E2E_NM_L3_GUEST=true', source)
        self.assertIn('nm-l3-foreign-collision-rejected', source)
        self.assertNotIn('ip route flush', source)
        self.assertEqual(subprocess.run(['bash', '-n', str(SCRIPT)], check=False).returncode, 0)

    def test_hosted_guest_suite_is_actual_selected_release(self):
        shared = SCRIPT.with_name("hosted-synthetic-tun.sh").read_text()
        harness = SCRIPT.read_text()
        self.assertIn('GUEST_RELEASE="${PODLAZ_E2E_GUEST_RELEASE:-noble}"', shared)
        self.assertIn('debootstrap --variant=minbase "${GUEST_RELEASE}"', shared)
        self.assertIn('case "${GUEST_RELEASE}" in', shared)
        self.assertIn('guest_exec cat /etc/os-release', harness)
        self.assertIn('VERSION_CODENAME=${GUEST_RELEASE}', harness)

    def test_plugin_only_in_guest_and_separate_service(self):
        source = SCRIPT.read_text()
        self.assertIn('python3-dbus python3-gi', source)
        self.assertIn('/usr/lib/NetworkManager/VPN/podlaz-l3-research.name', source)
        self.assertIn('nm_vpn_l3_guest_plugin.py', source)
        self.assertIn('guest_prepare_adapter', source)
        self.assertIn('PODLAZ_E2E_GUEST_RELEASE', source)
        self.assertNotIn('systemctl restart NetworkManager', source)


if __name__ == '__main__':
    unittest.main()
