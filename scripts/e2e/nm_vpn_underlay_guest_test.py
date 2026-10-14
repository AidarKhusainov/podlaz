"""Guest-only policy-routing safety requirements; no privileged mutations."""
import unittest
from nm_vpn_underlay_guest import UnderlayEvidence, select_underlay


GOOD = UnderlayEvidence(
    interface="host0", gateway="172.31.254.1", uplink_network="172.31.254.0/30",
    uplink_default_metric=100, ipv4_rules=((0, 255, 0, 0), (32766, 254, 0, 0),
                                            (32767, 253, 0, 0)),
    occupied_tables=frozenset((253, 254, 255)),
)


class UnderlayGuestContract(unittest.TestCase):
    def test_separate_underlay_no_main_loop(self):
        p = select_underlay(GOOD, vpn_default_metric=10)
        self.assertNotEqual(p.table, 254)
        self.assertLess(p.rule_priority, 32766)
        self.assertEqual(p.tunnel_mark, 20570)
        self.assertEqual(p.default_route, ("default", "172.31.254.1", "host0", p.table))
        self.assertEqual(p.connected_route, ("172.31.254.0/30", "host0", p.table))
        self.assertEqual(p.fwmark_rule, (p.tunnel_mark, 0xffffffff, p.table, p.rule_priority))

    def test_refuses_policy_rules_that_can_override_our_mark(self):
        foreign = UnderlayEvidence(**{**GOOD.__dict__,
            "ipv4_rules": ((0, 255, 0, 0), (10, 51820, 20570, 0xffffffff),
                           (32766, 254, 0, 0))})
        with self.assertRaises(ValueError):
            select_underlay(foreign, 10)

    def test_refuses_untrusted_or_unreachable_gateway(self):
        for dev, gw, network in (
            ("podlaz0", "172.31.254.1", "172.31.254.0/30"),
            ("host0", "198.51.100.254", "172.31.254.0/30"),
            ("host0", "172.31.254.1", "0.0.0.0/0"),
            ("host0", "127.0.0.1", "172.31.254.0/30"),
        ):
            with self.subTest(dev=dev, gw=gw, network=network), self.assertRaises(ValueError):
                select_underlay(UnderlayEvidence(**{**GOOD.__dict__,
                                  "interface": dev, "gateway": gw, "uplink_network": network}), 10)

    def test_refuses_metric_and_table_collision(self):
        with self.assertRaises(ValueError):
            select_underlay(GOOD, 100)
        with self.assertRaises(ValueError):
            select_underlay(UnderlayEvidence(**{**GOOD.__dict__,
                                  "occupied_tables": frozenset(range(53000, 53100))}), 10)
        with self.assertRaises(ValueError):
            select_underlay(UnderlayEvidence(**{**GOOD.__dict__,
                                  "uplink_default_metric": 0}), 10)

    def test_refuses_ambiguous_foreign_rule_and_reuses_no_foreign_state(self):
        foreign = UnderlayEvidence(**{**GOOD.__dict__,
            "ipv4_rules": ((0,255,0,0), (100,51820,0,0), (32766,254,0,0))})
        with self.assertRaises(ValueError):
            select_underlay(foreign, 10)
        # Unknown underlay table already taken, but a different free one is not
        # authority to delete an occupant. Allocation must skip it.
        occupied = UnderlayEvidence(**{**GOOD.__dict__,
                         "occupied_tables": frozenset((253,254,255,53000))})
        p = select_underlay(occupied, 10)
        self.assertEqual(p.table, 53001)


if __name__ == "__main__":
    unittest.main()
