"""Fixture-only tests: DNS wire, multiple answers and actual Xray peer checks."""
from pathlib import Path
from tempfile import TemporaryDirectory
import socket
import struct
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from nm_vpn_l3_hostname_guest import (_Server, _DNSHandler, HOSTNAME,
                                     HostnameFixture, OTHER_IPV4, OTHER_IPV6)


def query(host: str, qtype: int) -> bytes:
    name = b"".join(bytes((len(label),)) + label.encode("ascii")
                    for label in host.split(".")) + b"\x00"
    return (b"\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00"
            + name + struct.pack("!HH", qtype, 1))


class DnsFixtureTest(unittest.TestCase):
    def test_real_loopback_dns_responses_are_controlled_and_multi_family(self):
        with _Server(("127.0.0.55", 0), _DNSHandler) as server:
            server.records = (("172.31.253.1", OTHER_IPV4), (OTHER_IPV6,), False)
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
                client.settimeout(2)
                for kind, count in ((1, 2), (28, 1)):
                    client.sendto(query(HOSTNAME, kind), server.server_address)
                    server.handle_request()
                    response, _ = client.recvfrom(4096)
                    self.assertEqual(response[:2], b"\x12\x34")
                    self.assertEqual(int.from_bytes(response[6:8], "big"), count)
                    self.assertEqual(response[3] & 15, 0)
                server.records = ((), (), True)
                client.sendto(query(HOSTNAME, 1), server.server_address)
                server.handle_request()
                response, _ = client.recvfrom(4096)
                self.assertEqual(response[3] & 15, 3)
                self.assertEqual(response[6:8], b"\x00\x00")

    def test_rebind_rejects_four_negative_resolution_classes(self):
        fixture = HostnameFixture(Path("/tmp/missing-xray"), "172.31.253.1")
        fixture.server = SimpleNamespace(records=None)
        def observed():
            records = fixture.server.records
            if records != (("172.31.253.1",), (), False):
                raise ValueError("not a stable pinned gateway")
        with patch.object(fixture, "assert_single_gateway", side_effect=observed) as guard:
            fixture.qualify_negative_answers()
        self.assertEqual(guard.call_count, 5)
        self.assertEqual(fixture.server.records, (("172.31.253.1",), (), False))

    def test_managed_resolver_restored_before_service_restart(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            resolv = root / "resolv.conf"
            fixture = HostnameFixture(root / "original.json", "172.31.253.1")
            fixture.previous_link = "/run/systemd/resolve/stub-resolv.conf"
            fixture.changed_resolver = True
            fixture.config_path.write_text("{}")
            resolv.write_text("nameserver 127.0.0.55\n")
            def restart(*args, **kwargs):
                self.assertTrue(resolv.is_symlink())
                self.assertEqual(resolv.readlink().as_posix(), fixture.previous_link)
                self.assertEqual(args[0], ("systemctl", "restart", "systemd-resolved.service"))
                return SimpleNamespace()
            with patch("nm_vpn_l3_hostname_guest.RESOLV", resolv), patch(
                 "nm_vpn_l3_hostname_guest.subprocess.run", side_effect=restart) as command:
                fixture.__exit__(None, None, None)
            command.assert_called_once()
            self.assertFalse(fixture.config_path.exists())

    def test_unmodified_resolver_requires_no_service_mutation(self):
        fixture = HostnameFixture(Path("/tmp/source-missing"), "172.31.253.1")
        with patch("nm_vpn_l3_hostname_guest.subprocess.run") as command:
            fixture.__exit__(None, None, None)
        command.assert_not_called()

    def test_a_peer_with_different_address_is_not_a_gateway_proof(self):
        fixture = HostnameFixture(Path("/tmp/missing-xray"), "172.31.253.1")
        fixture.port = 18443
        with patch("nm_vpn_l3_hostname_guest.subprocess.run",
                   return_value=SimpleNamespace(stdout=
                     'ESTAB 0 0 172.31.254.2:3333 172.31.253.1:18443 users:(("xray",pid=1234,fd=4))')):
            fixture.verify_actual_peer(1234)
        with patch("nm_vpn_l3_hostname_guest.subprocess.run",
                   return_value=SimpleNamespace(stdout=
                     'ESTAB 0 0 172.31.254.2:3333 172.31.254.1:18443 users:(("xray",pid=1234,fd=4))')):
            with self.assertRaisesRegex(ValueError, "differs"):
                fixture.verify_actual_peer(1234)
        with patch("nm_vpn_l3_hostname_guest.subprocess.run",
                   return_value=SimpleNamespace(stdout="")):
            with self.assertRaisesRegex(ValueError, "no real"):
                fixture.verify_actual_peer(1234)


if __name__ == "__main__":
    unittest.main()
