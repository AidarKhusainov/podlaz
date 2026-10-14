import unittest
from nm_vpn_l3_failure_trace import last_safe_stage

class SanitizedStageTest(unittest.TestCase):
    def test_bounded_reconcile_failure_stage(self):
        self.assertEqual(last_safe_stage([
            'nm-l3-progress=underlay-reconcile',
            'nm-l3-progress=underlay-link-route',
            'nm-l3-failure-origin=underlay-link-route',
            'nm-l3-progress=cleanup-intent']), 'origin-underlay-link-route')
        self.assertEqual(last_safe_stage([
            'nm-l3-failure-origin=underlay-secret-gateway']), 'not-reached')

    def test_fixed_underlay_failure_bucket(self):
        self.assertEqual(last_safe_stage(['nm-l3-progress=cleanup-underlay-one-route',
                                          'private-neighbor=2001:db8:460::1']),
                         'cleanup-underlay-one-route')


    def test_reactivation_errors_emit_no_profile_identity(self):
        self.assertEqual(last_safe_stage([
            'nm-l3-progress=postchurn-vpn-reactivate',
            'nm-l3-failure-origin=postchurn-vpn-reactivate']),
                         'origin-postchurn-vpn-reactivate')

    def test_closed_postchurn_vpn_state_does_not_echo_guest_identity(self):
        self.assertEqual(last_safe_stage([
            'nm-l3-progress=postchurn-nm-active',
            'nm-l3-progress=postchurn-vpn-inactive',
            'nm-l3-failure-origin=postchurn-vpn-inactive']),
                         'origin-postchurn-vpn-inactive')

    def test_data_plane_failure_is_closed_and_unambiguous(self):
        self.assertEqual(last_safe_stage(['nm-l3-progress=uplink-traffic-restored',
                                          'nm-l3-progress=data-route-selection',
                                          'nm-l3-failure-origin=data-route-selection',
                                          'nm-l3-progress=cleanup-intent']),
                         'origin-data-route-selection')

    def test_precleanup_failure_is_never_hidden_by_finally(self):
        self.assertEqual(last_safe_stage([
            'nm-l3-progress=uplink-traffic-restored',
            'nm-l3-failure-origin=uplink-traffic-restored',
            'nm-l3-progress=cleanup-entry',
            'nm-l3-progress=cleanup-intent',
            'nm-l3-result=fail']), 'origin-uplink-traffic-restored')
        self.assertEqual(last_safe_stage([
            'nm-l3-progress=cleanup-entry',
            'nm-l3-progress=cleanup-intent',
            'nm-l3-result=pass']), 'guest-success')
        self.assertEqual(last_safe_stage([
            'nm-l3-failure-origin=PRIVATE_PASSWORD',
            'nm-l3-progress=cleanup-underlay-verify']), 'cleanup-underlay-verify')

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
