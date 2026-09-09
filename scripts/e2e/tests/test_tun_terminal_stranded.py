import copy
import importlib.util
import pathlib
import unittest


MODULE_PATH = pathlib.Path(__file__).resolve().parents[1] / "lib" / "tun_terminal_stranded.py"
SPEC = importlib.util.spec_from_file_location("tun_terminal_stranded", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC is not None and SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class TunTerminalStrandedTests(unittest.TestCase):
    def setUp(self):
        self.tx = {
            "state": "failed",
            "failure_reason": "rollback active TUN host networking before stopping Xray: missing nftables chains",
            "rollback": {
                "tun_addresses": [
                    {"interface_name": "podlaz0", "cidr": "198.18.0.1/32"}
                ],
                "routes": [
                    {"table": "51820", "cidr": "0.0.0.0/1", "dev": "podlaz0", "via": ""},
                    {"table": "51820", "cidr": "128.0.0.0/1", "dev": "podlaz0", "via": ""},
                ],
                "policy_rules": [
                    {"priority": 10000, "from": "all", "to": "", "table": "51820", "mark": ""},
                    {"priority": 9999, "from": "", "to": "203.0.113.10/32", "table": "main", "mark": ""},
                ],
                "nftables": [{"family": "inet", "table": "podlaz"}],
            },
        }
        self.session = {
            "intent": "disconnect",
            "protection": {
                "state": "armed",
                "family": "inet",
                "table": "podlaz_pe_001122334455",
            },
        }
        self.addrs = [{"ifname": "podlaz0", "addr_info": []}]
        self.routes = []
        self.rules = []
        self.nft = {
            "nftables": [
                {"table": {"family": "inet", "name": "podlaz"}},
                {"table": {"family": "inet", "name": "podlaz_pe_001122334455"}},
                {"table": {"family": "inet", "name": "foreign_table"}},
            ]
        }

    def active_state(self):
        tx = copy.deepcopy(self.tx)
        tx["state"] = "committed"
        tx["failure_reason"] = ""
        session = copy.deepcopy(self.session)
        session["intent"] = "resume"
        addrs = [{"ifname": "podlaz0", "addr_info": [{"family": "inet", "local": "198.18.0.1", "prefixlen": 32}]}]
        routes = [
            {"table": 51820, "dst": "0.0.0.0/1", "dev": "podlaz0"},
            {"table": 51820, "dst": "128.0.0.0/1", "dev": "podlaz0"},
        ]
        rules = [
            {"priority": 10000, "from": "all", "table": 51820},
            {"priority": 9999, "to": "203.0.113.10/32", "table": "main"},
        ]
        return tx, session, addrs, routes, rules, copy.deepcopy(self.nft)

    def assert_invalid(self, mutator):
        tx = copy.deepcopy(self.tx)
        session = copy.deepcopy(self.session)
        addrs = copy.deepcopy(self.addrs)
        routes = copy.deepcopy(self.routes)
        rules = copy.deepcopy(self.rules)
        nft = copy.deepcopy(self.nft)
        mutator(tx, session, addrs, routes, rules, nft)
        with self.assertRaises(ValueError):
            MODULE.validate_v0240_stranded_state(tx, session, addrs, routes, rules, nft)

    def test_exact_stranded_shape_is_accepted(self):
        MODULE.validate_v0240_stranded_state(self.tx, self.session, self.addrs, self.routes, self.rules, self.nft)

    def test_remaining_tun_address_is_rejected(self):
        self.assert_invalid(lambda tx, session, addrs, routes, rules, nft: addrs[0]["addr_info"].append(
            {"family": "inet", "local": "198.18.0.1", "prefixlen": 32}
        ))

    def test_remaining_exact_route_is_rejected(self):
        self.assert_invalid(lambda tx, session, addrs, routes, rules, nft: routes.append(
            {"table": 51820, "dst": "0.0.0.0/1", "dev": "podlaz0"}
        ))

    def test_remaining_exact_policy_rule_is_rejected(self):
        self.assert_invalid(lambda tx, session, addrs, routes, rules, nft: rules.append(
            {"priority": 10000, "from": "all", "table": 51820}
        ))

    def test_missing_transaction_firewall_is_rejected(self):
        self.assert_invalid(lambda tx, session, addrs, routes, rules, nft: nft.__setitem__(
            "nftables",
            [item for item in nft["nftables"] if item.get("table", {}).get("name") != "podlaz"],
        ))

    def test_missing_privacy_envelope_is_rejected(self):
        self.assert_invalid(lambda tx, session, addrs, routes, rules, nft: nft.__setitem__(
            "nftables",
            [item for item in nft["nftables"] if item.get("table", {}).get("name") != "podlaz_pe_001122334455"],
        ))

    def test_non_v0240_firewall_identity_is_rejected(self):
        self.assert_invalid(lambda tx, session, addrs, routes, rules, nft: tx["rollback"]["nftables"][0].update(
            {"table": "other"}
        ))

    def test_non_terminal_intent_is_rejected(self):
        self.assert_invalid(lambda tx, session, addrs, routes, rules, nft: session.update({"intent": "resume"}))

    def test_active_requires_every_exact_route_and_rule(self):
        tx, session, addrs, routes, rules, nft = self.active_state()
        MODULE.validate_exact_live_state(
            tx, session, addrs, routes, rules, nft,
            data_plane_present=True, barriers_present=True,
        )
        routes.pop()
        with self.assertRaises(ValueError):
            MODULE.validate_exact_live_state(
                tx, session, addrs, routes, rules, nft,
                data_plane_present=True, barriers_present=True,
            )

    def test_absent_rejects_any_single_exact_route_residue(self):
        tx = copy.deepcopy(self.tx)
        session = copy.deepcopy(self.session)
        routes = [{"table": 51820, "dst": "128.0.0.0/1", "dev": "podlaz0"}]
        clean_nft = {"nftables": [{"table": {"family": "inet", "name": "foreign_table"}}]}
        with self.assertRaises(ValueError):
            MODULE.validate_exact_live_state(
                tx, session, copy.deepcopy(self.addrs), routes, [], clean_nft,
                data_plane_present=False, barriers_present=False,
            )

    def test_absent_rejects_any_single_exact_rule_residue(self):
        tx = copy.deepcopy(self.tx)
        session = copy.deepcopy(self.session)
        rules = [{"priority": 9999, "to": "203.0.113.10/32", "table": "main"}]
        clean_nft = {"nftables": [{"table": {"family": "inet", "name": "foreign_table"}}]}
        with self.assertRaises(ValueError):
            MODULE.validate_exact_live_state(
                tx, session, copy.deepcopy(self.addrs), [], rules, clean_nft,
                data_plane_present=False, barriers_present=False,
            )


if __name__ == "__main__":
    unittest.main()
