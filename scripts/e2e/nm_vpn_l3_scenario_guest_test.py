"""Fail-closed dataplane observations for a disposable NM L3 guest."""
import unittest
from nm_vpn_l3_scenario_guest import verify_route_selection


class RouteSelectionTest(unittest.TestCase):
    def test_only_genuine_main_tun_default_and_distinct_marked_underlay(self):
        verify_route_selection(
            main_defaults=[{"dst": "default", "dev": "host0", "metric":100},
                           {"dst": "default", "dev": "podlaz0", "metric":10}],
            ordinary=[{"dst": "1.1.1.1", "dev": "podlaz0"}],
            marked=[{"dst": "1.1.1.1", "gateway": "172.31.254.1", "dev": "host0", "mark": 20570}],
            uplink="host0", mark=20570)

    def test_rejects_metadata_route_that_cannot_carry_traffic(self):
        bad = [
            [{"dst":"default", "dev":"host0", "metric":100}],
            [{"dst":"default", "dev":"host0", "metric":5}, {"dst":"default", "dev":"podlaz0", "metric":10}],
            [{"dst":"default", "dev":"host0", "metric":100}, {"dst":"default", "dev":"podlaz0", "metric":10, "table":53000}],
        ]
        for defaults in bad:
            with self.subTest(defaults=defaults), self.assertRaises(ValueError):
                verify_route_selection(defaults, [{"dev":"podlaz0"}], [{"dev":"host0", "mark":20570}], "host0", 20570)

    def test_rejects_looping_marked_socket_or_leaking_ordinary_route(self):
        defaults=[{"dst":"default", "dev":"podlaz0", "metric":10}, {"dst":"default", "dev":"host0", "metric":100}]
        for ordinary, marked in (
            ([{"dev":"host0"}], [{"dev":"host0", "mark":20570}]),
            ([{"dev":"podlaz0"}], [{"dev":"podlaz0", "mark":20570}]),
            ([{"dev":"podlaz0"}], [{"dev":"host0", "mark":33}]),
        ):
            with self.subTest(ordinary=ordinary, marked=marked), self.assertRaises(ValueError):
                verify_route_selection(defaults, ordinary, marked, "host0", 20570)


class NMProfileOwnershipTest(unittest.TestCase):
    def test_exact_uuid_and_name_are_required_for_mutation(self):
        from nm_vpn_l3_scenario_guest import exact_owned_nm_profile_present
        owned='12345678-1234-4234-8234-1234567890ab'
        other='12345678-1234-4234-8234-1234567890ac'
        self.assertTrue(exact_owned_nm_profile_present([(owned,'podlaz-guest-nm-l3')],owned))
        self.assertFalse(exact_owned_nm_profile_present([(other,'synthetic-uplink')],owned))
        for inventory in ([(other,'podlaz-guest-nm-l3')],
                          [(owned,'foreign')],
                          [(owned,'podlaz-guest-nm-l3'),(other,'podlaz-guest-nm-l3')],
                          [(owned,'podlaz-guest-nm-l3'),(owned,'podlaz-guest-nm-l3')],
                          [('', 'podlaz-guest-nm-l3')]):
            with self.subTest(inventory=inventory), self.assertRaises(ValueError):
                exact_owned_nm_profile_present(inventory,owned)


class NaturalConnectivityLivenessTest(unittest.TestCase):
    def test_does_not_accept_cached_full_after_xray_has_exited(self):
        from unittest.mock import patch
        from nm_vpn_l3_scenario_guest import _natural_full

        class StoppedXray:
            def poll(self):
                return 1

        with patch('nm_vpn_l3_scenario_guest.run', return_value='full') as command:
            with self.assertRaises(ValueError):
                _natural_full(StoppedXray())
            command.assert_not_called()


if __name__=='__main__':
    unittest.main()


class ExactUnderlayCleanup(unittest.TestCase):
    def test_refuses_foreign_routes_and_rule_replacements(self):
        from nm_vpn_l3_scenario_guest import verify_owned_underlay
        from nm_vpn_underlay_guest import select_underlay, UnderlayEvidence
        evidence = UnderlayEvidence('host0', '172.31.254.1', '172.31.254.0/30', 100,
                                    ((0,255,0,0),(32766,254,0,0),(32767,253,0,0)),
                                    frozenset((253,254,255)))
        plan = select_underlay(evidence,10)
        routes = [
            {'dst':'172.31.254.0/30','dev':'host0','scope':'link','table':plan.table},
            {'dst':'default','dev':'host0','gateway':'172.31.254.1','table':plan.table},
        ]
        rules = [{'priority':10000,'fwmark':'0x505a','fwmask':'0xffffffff','table':plan.table}]
        verify_owned_underlay(plan,routes,rules)
        for mutated in (
            [routes[0], {**routes[1],'gateway':'172.31.254.2'}],
            routes+[{'dst':'198.51.100.0/24','dev':'host0','table':plan.table}],
        ):
            with self.assertRaises(ValueError):
                verify_owned_underlay(plan,mutated,rules)
        with self.assertRaises(ValueError):
            verify_owned_underlay(plan,routes,[{**rules[0],'fwmark':'0x505b'}])
