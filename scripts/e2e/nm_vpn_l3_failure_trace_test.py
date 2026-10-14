import unittest
from nm_vpn_l3_failure_trace import last_safe_stage

class SanitizedStageTest(unittest.TestCase):
    def test_only_closed_cleanup_stage_is_public(self):
        self.assertEqual(last_safe_stage(['nm-l3-progress=cleanup-underlay-verify',
                                          'private gateway data']), 'cleanup-underlay-verify')
        self.assertEqual(last_safe_stage(['nm-l3-progress=cleanup-internal-addr-172',
                                          'nm-l3-progress=cleanup-nft']), 'cleanup-nft')

    def test_closed_enum_prevents_private_guest_output(self):
        self.assertEqual(last_safe_stage(['private-password:abc',
                                          'nm-l3-progress=ipv6-route',
                                          'nm-l3-progress=private.gateway']),
                         'ipv6-route')
        self.assertEqual(last_safe_stage(['nm-l3-invalid-ip4-rejected=pass']),
                         'nm-l3-invalid-ip4-rejected')
        self.assertEqual(last_safe_stage(['url=https://secret.example',
                                          'nm-l3-progress=arbitrary',
                                          'nm-l3-failure-protection=fail']),
                         'not-reached')
