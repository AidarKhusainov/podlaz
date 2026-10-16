"""Hosted candidate test cannot skip the kernel/privacy gate."""
from pathlib import Path
import unittest

WORKFLOW=Path(__file__).parents[2]/'.github/workflows/hosted-networkmanager-l3-guest.yml'

class GuestGateWorkflow(unittest.TestCase):
    def test_always_requires_normalized_report_and_private_cleanup(self):
        source=WORKFLOW.read_text()
        self.assertIn('fail-fast: false',source)
        self.assertIn('release: [noble, resolute]', source)
        self.assertIn("'scripts/e2e/hosted-synthetic-tun.sh'", source)
        self.assertIn('PODLAZ_E2E_GUEST_RELEASE:', source)
        self.assertIn('ubuntu-24.04',source)
        self.assertIn('bash scripts/e2e/nm_vpn_l3_guest_harness.sh',source)
        self.assertIn('Validate normalized guest report',source)
        self.assertIn('if: always()',source)
        self.assertIn('Remove private guest root',source)
        self.assertIn('retention-days: 7',source)
        self.assertNotIn('continue-on-error',source)
        self.assertNotIn('nft flush ruleset',source)

if __name__=='__main__':
    unittest.main()
