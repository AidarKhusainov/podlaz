"""Pure single-writer NM-owned TUN research contracts (disposable guest only)."""
import json
import unittest
from unittest.mock import patch

from nm_owned_tun_guest import (_qualified_peer_packets,
                                inspect_nm_tun_netlink_events,
                                verify_attached_tun, verify_nm_survives_xray_exit)


class NMOwnedTunContractTest(unittest.TestCase):
    def test_grouped_peer_counters_use_real_nft_rule_comments(self):
        payload = {"nftables": [
            {"rule": {"comment": "nm_owned_peer_a", "expr": [
                {"match": {"op": "=="}}, {"counter": {"packets": 4, "bytes": 240}}]}},
            {"rule": {"comment": "nm_owned_peer_b", "expr": [
                {"match": {"op": "=="}}, {"counter": {"packets": 2, "bytes": 120}}]}},
        ]}
        with patch("nm_owned_tun_guest.run", return_value=json.dumps(payload)):
            self.assertEqual(_qualified_peer_packets(), (True, True))
        payload["nftables"][1]["rule"]["expr"][1]["counter"]["packets"] = 0
        with patch("nm_owned_tun_guest.run", return_value=json.dumps(payload)):
            self.assertEqual(_qualified_peer_packets(), (True, False))

    def test_duplicate_or_missing_provider_counter_fails_closed(self):
        entry = {"rule": {"comment": "nm_owned_peer_a",
                           "expr": [{"counter": {"packets": 1}}]}}
        with patch("nm_owned_tun_guest.run",
                   return_value=json.dumps({"nftables": [entry, entry]})):
            with self.assertRaisesRegex(ValueError, "ambiguous"):
                _qualified_peer_packets()
        with patch("nm_owned_tun_guest.run",
                   return_value=json.dumps({"nftables": [entry]})):
            with self.assertRaisesRegex(ValueError, "missing"):
                _qualified_peer_packets()

    def test_same_existing_tun_identity_is_required(self):
        verify_attached_tun(12, 12, True)
        with self.assertRaisesRegex(ValueError, "changed"):
            verify_attached_tun(12, 13, True)
        with self.assertRaisesRegex(ValueError, "not running"):
            verify_attached_tun(12, 12, False)
        with self.assertRaisesRegex(ValueError, "invalid"):
            verify_attached_tun(0, 0, True)

    def test_link_operstate_may_drop_without_admin_down(self):
        carrier_event = (
            "9: podlaz0: <POINTOPOINT,NOARP,UP> mtu 1500 "
            "qdisc fq state DOWN mode DEFAULT group default\\n"
            "    link/none\\n"
        )
        self.assertEqual(inspect_nm_tun_netlink_events(carrier_event), 1)
        self.assertEqual(inspect_nm_tun_netlink_events(
            "9: podlaz0: <POINTOPOINT,NOARP,UP> mtu 1500 state UP\\n"), 0)

    def test_actual_admin_down_or_deletion_stays_fatal(self):
        with self.assertRaisesRegex(ValueError, "administrative"):
            inspect_nm_tun_netlink_events(
                "9: podlaz0: <POINTOPOINT,NOARP> mtu 1500 state DOWN\\n")
        with self.assertRaisesRegex(ValueError, "deleted"):
            inspect_nm_tun_netlink_events(
                "Deleted 9: podlaz0: <POINTOPOINT,UP> mtu 1500 state DOWN\\n")
        with self.assertRaisesRegex(ValueError, "ambiguous"):
            inspect_nm_tun_netlink_events("state DOWN\\n")
        with self.assertRaisesRegex(ValueError, "bounded"):
            inspect_nm_tun_netlink_events("a" * 65537)

    def test_nm_must_keep_its_tun_up_after_xray_exits(self):
        verify_nm_survives_xray_exit(True, True)
        with self.assertRaisesRegex(ValueError, "deactivated"):
            verify_nm_survives_xray_exit(False, True)
        with self.assertRaisesRegex(ValueError, "down"):
            verify_nm_survives_xray_exit(True, False)
        with self.assertRaisesRegex(ValueError, "deactivated"):
            verify_nm_survives_xray_exit(False, False)


if __name__ == "__main__":
    unittest.main()
