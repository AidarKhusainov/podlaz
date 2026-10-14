"""Proof that guest adapter refuses unknown or previously configured TUN."""
import unittest
from nm_vpn_l3_runtime_guard import validate_xray_tun

LINK = [{"ifname": "podlaz0", "ifindex": 72, "linkinfo": {"info_kind": "tun"}}]
ADDR = [{"ifname": "podlaz0", "ifindex": 72, "addr_info": []}]


class RuntimeGuardTest(unittest.TestCase):
    def test_accepts_tracked_unconfigured_xray_tun(self):
        validate_xray_tun(72, LINK, ADDR)

    def test_refuses_foreign_link_and_stale_ifindex(self):
        for link in ([{"ifname": "podlaz0", "ifindex": 73, "linkinfo": {"info_kind": "tun"}}],
                     [{"ifname": "host0", "ifindex": 72, "linkinfo": {"info_kind": "tun"}}],
                     [{"ifname": "podlaz0", "ifindex": 72, "linkinfo": {"info_kind": "dummy"}}], []):
            with self.subTest(link=link), self.assertRaises(ValueError):
                validate_xray_tun(72, link, ADDR)

    def test_refuses_second_writer_and_unqualified_ipv6(self):
        for addrs in (
            [{"family":"inet", "local":"198.18.0.17", "prefixlen":32}],
            [{"family":"inet6", "local":"2001:db8::1", "prefixlen":128}],
        ):
            with self.subTest(addrs=addrs), self.assertRaises(ValueError):
                validate_xray_tun(72, LINK, [{"ifname":"podlaz0", "ifindex":72, "addr_info":addrs}])

    def test_refuses_missing_or_malformed_kernel_snapshot(self):
        for broken in (None, [{"ifname":"podlaz0","ifindex":72}], [*LINK, *LINK]):
            with self.subTest(broken=broken), self.assertRaises(ValueError):
                validate_xray_tun(72, broken, ADDR)


if __name__ == '__main__':
    unittest.main()
