"""Fail-closed, pure-data contract for the disposable NM L3 guest adapter."""
import ipaddress
import sys
import unittest

from nm_vpn_l3_contract import GuestVPNConfig, config_dicts, vpn_profile_settings


class GuestL3Contract(unittest.TestCase):
    def test_genuine_vpn_ipv4_config_and_no_ipv6_delegation(self):
        config = GuestVPNConfig(
            interface="podlaz0", gateway="198.51.100.7",
            address="198.18.0.17/32", dns=("198.51.100.53",),
        )
        generic, ip4 = config_dicts(config)
        self.assertEqual(generic["gateway"], int.from_bytes(ipaddress.IPv4Address("198.51.100.7").packed, sys.byteorder))
        self.assertEqual(generic["tundev"], "podlaz0")
        self.assertEqual(generic["has-ip4"], True)
        self.assertEqual(generic["has-ip6"], False)
        self.assertEqual(generic["can-persist"], False)
        self.assertEqual(ip4["address"], int.from_bytes(ipaddress.IPv4Address("198.18.0.17").packed, sys.byteorder))
        self.assertEqual(ip4["prefix"], 32)
        self.assertEqual(ip4["ptp"], ip4["address"])
        self.assertEqual(ip4["dns"], [int.from_bytes(ipaddress.IPv4Address("198.51.100.53").packed, sys.byteorder)])
        self.assertIs(ip4["never-default"], False)

    def test_explicit_nm_main_default_and_gateway_route_suppression(self):
        settings = vpn_profile_settings("org.freedesktop.NetworkManager.podlazl3research", "podlaz-guest-nm-l3", "b870759a-8643-4e10-9496-823603871b8d", 10)
        self.assertEqual(settings["ipv4"]["route-table"], 254)
        self.assertFalse(settings["ipv4"]["never-default"])
        self.assertFalse(settings["ipv4"]["auto-route-ext-gw"])
        self.assertEqual(settings["ipv4"]["route-metric"], 10)
        self.assertEqual(settings["ipv4"]["dns-priority"], -30000)
        self.assertEqual(settings["ipv4"]["dns-search"], ["~."])
        self.assertEqual(settings["ipv6"]["method"], "ignore")
        self.assertFalse(settings["ipv6"]["auto-route-ext-gw"])
        self.assertFalse(settings["connection"]["autoconnect"])
        self.assertEqual(settings["connection"]["uuid"], "b870759a-8643-4e10-9496-823603871b8d")

    def test_refuse_any_untruthful_gateway_and_bad_link(self):
        for invalid in ("", "0.0.0.0", "127.0.0.1", "224.0.0.1", "::1", "not-an-ip"):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                config_dicts(GuestVPNConfig("podlaz0", invalid, "198.18.0.17/32", ("198.51.100.53",)))
        for interface in ("", "eth0", "../../lo", "podlaz00"):
            with self.subTest(interface=interface), self.assertRaises(ValueError):
                config_dicts(GuestVPNConfig(interface, "198.51.100.7", "198.18.0.17/32", ("198.51.100.53",)))

    def test_refuse_invalid_addresses_and_resolver_inputs(self):
        for addr in ("198.18.0.1/24", "0.0.0.0/32", "198.51.100.3/32", "::1/128"):
            with self.subTest(addr=addr), self.assertRaises(ValueError):
                config_dicts(GuestVPNConfig("podlaz0", "198.51.100.7", addr, ("198.51.100.53",)))
        for dns in ((), ("127.0.0.1",), ("198.51.100.53", "bad")):
            with self.subTest(dns=dns), self.assertRaises(ValueError):
                config_dicts(GuestVPNConfig("podlaz0", "198.51.100.7", "198.18.0.17/32", dns))

    def test_reject_non_unique_or_unsafe_nm_profile(self):
        for metric in (0, -1, 101, 4294967295):
            with self.subTest(metric=metric), self.assertRaises(ValueError):
                vpn_profile_settings("org.freedesktop.NetworkManager.podlazl3research", "podlaz-guest-nm-l3", "b870759a-8643-4e10-9496-823603871b8d", metric)
        with self.assertRaises(ValueError):
            vpn_profile_settings("org.freedesktop.NetworkManager.podlazl3research", "podlaz-guest-nm-l3", "not-a-uuid", 10)


if __name__ == "__main__":
    unittest.main()

class GuestDBusEmission(unittest.TestCase):
    def test_config_precedes_real_ip4_config_with_exact_dbus_types(self):
        from nm_vpn_l3_contract import emit_nm_ipv4_config
        events = []
        class DBus:
            @staticmethod
            def String(v): return ("s", v)
            @staticmethod
            def UInt32(v): return ("u", v)
            @staticmethod
            def Boolean(v): return ("b", v)
            @staticmethod
            def Array(v, signature): return ("a" + signature, v)
            @staticmethod
            def Dictionary(v, signature): return ("a{" + signature + "}", v)
        config = GuestVPNConfig("podlaz0", "172.31.253.1", "198.18.0.17/32", ("1.1.1.1",))
        emit_nm_ipv4_config(config, DBus, lambda name, value: events.append((name, value)))
        self.assertEqual([name for name, _ in events], ["Config", "Ip4Config"])
        self.assertEqual(events[0][1][0], "a{sv}")
        self.assertEqual(events[1][1][0], "a{sv}")
        self.assertEqual(events[0][1][1]["has-ip4"], ("b", True))
        self.assertEqual(events[1][1][1]["prefix"], ("u", 32))
        self.assertEqual(events[1][1][1]["dns"][0], "au")

    def test_invalid_config_emits_nothing(self):
        from nm_vpn_l3_contract import emit_nm_ipv4_config
        events = []
        with self.assertRaises(ValueError):
            emit_nm_ipv4_config(GuestVPNConfig("eth0", "172.31.253.1", "198.18.0.17/32", ("1.1.1.1",)),
                object(), lambda name, value: events.append(name))
        self.assertEqual(events, [])
