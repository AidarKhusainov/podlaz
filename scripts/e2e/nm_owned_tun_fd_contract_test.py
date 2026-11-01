"""Pure positive and negative contract coverage for inherited TUN admission."""
import json
import os
from pathlib import Path
import tempfile
import unittest

from nm_owned_tun_fd_contract import (
    TUN_FLAGS, verify_tun_fd_identity, verify_xray_config,
)


class ExternalFdAdmissionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "xray.json"
        self.payload = {"inbounds": [{
            "protocol": "tun", "settings": {
                "name": "podlaz0", "MTU": 1500, "userLevel": 0,
            },
        }], "outbounds": [{"protocol": "freedom"}]}
        self.store()

    def store(self):
        self.path.write_text(json.dumps(self.payload), encoding="utf-8")
        os.chmod(self.path, 0o600)

    def test_single_exact_tun_passes(self):
        verify_xray_config(self.path, ifname="podlaz0", mtu=1500)

    def test_mutators_and_mismatches_rejected(self):
        for key, value in (("gateway", ["198.18.0.1/32"]),
                           ("autoSystemRoutingTable", ["0.0.0.0/0"]),
                           ("autoSystemDnsToGateway", True),
                           ("autoOutboundsInterface", "auto")):
            with self.subTest(key=key):
                self.payload["inbounds"][0]["settings"][key] = value
                self.store()
                with self.assertRaisesRegex(ValueError, "must not manage"):
                    verify_xray_config(self.path, ifname="podlaz0", mtu=1500)
                del self.payload["inbounds"][0]["settings"][key]
        self.store()
        with self.assertRaisesRegex(ValueError, "MTU mismatch"):
            verify_xray_config(self.path, ifname="podlaz0", mtu=1400)
        with self.assertRaisesRegex(ValueError, "name collision"):
            verify_xray_config(self.path, ifname="foreign0", mtu=1500)
        self.payload["inbounds"].append(self.payload["inbounds"][0])
        self.store()
        with self.assertRaisesRegex(ValueError, "exactly one"):
            verify_xray_config(self.path, ifname="podlaz0", mtu=1500)

    def test_unsafe_file_rejected(self):
        os.chmod(self.path, 0o644)
        with self.assertRaisesRegex(ValueError, "private"):
            verify_xray_config(self.path, ifname="podlaz0", mtu=1500)

    def test_exact_ifindex_mtu_and_fd_type(self):
        link = {"ifname": "podlaz0", "ifindex": 12, "mtu": 1500,
                "flags": ["UP"], "linkinfo": {"info_kind": "tun"}}
        def check(before, after, flags=TUN_FLAGS):
            verify_tun_fd_identity(
                name="podlaz0", flags=flags, expected_name="podlaz0",
                before=before, after=after, expected_index=12,
                expected_mtu=1500,
            )
        check(link, link)
        for field, wrong in (("ifindex", 13), ("mtu", 1400),
                             ("ifname", "foreign0"), ("flags", [])):
            with self.subTest(field=field):
                bad = {**link, field: wrong}
                with self.assertRaisesRegex(ValueError, "ownership changed"):
                    check(link, bad)
        with self.assertRaisesRegex(ValueError, "not an"):
            check(link, link, flags=0)
        with self.assertRaisesRegex(ValueError, "packet mode"):
            check(link, link, flags=TUN_FLAGS | 0x4000)


if __name__ == "__main__":
    unittest.main()
