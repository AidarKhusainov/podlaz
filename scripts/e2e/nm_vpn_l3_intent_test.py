"""The throwaway NM service must never activate with a second L3 writer."""
import json
import os
import tempfile
from pathlib import Path
import unittest
from nm_vpn_l3_intent import load_guest_intent, GuestPaths, retained_ipv4_reapply_authorized


DATA = {
    "schema": "podlaz.nm-l3-research.v1",
    "owner": "podlaz-guest-nm-l3",
    "tun_ifindex": 72,
    "tun_name": "podlaz0",
    "external_gateway": "172.31.253.1",
    "tun_address": "198.18.0.17/32",
    "dns": ["1.1.1.1"],
}


class GuestIntentTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / "run/systemd").mkdir(parents=True)
        (self.root / "run/systemd/container").write_text("systemd-nspawn\n")
        (self.root / "run/podlaz-nm-l3").mkdir(parents=True)
        self.intent = self.root / "run/podlaz-nm-l3/intent.json"
        self.intent.write_text(json.dumps(DATA))
        self.intent.chmod(0o600)

    def paths(self):
        return GuestPaths(container=self.root / "run/systemd/container",
                          intent=self.intent,
                          legacy_session=self.root / "run/podlaz/network-session-continuation.json")

    def test_loads_only_explicit_guest_intent(self):
        config, ifindex = load_guest_intent(self.paths(), expected_uid=os.getuid())
        self.assertEqual(config.gateway, "172.31.253.1")
        self.assertEqual(config.address, "198.18.0.17/32")
        self.assertEqual(ifindex, 72)

    def test_refuses_legacy_podlaz_l3_session(self):
        (self.root / "run/podlaz").mkdir(parents=True)
        (self.root / "run/podlaz/network-session-continuation.json").write_text("{}")
        with self.assertRaises(ValueError):
            load_guest_intent(self.paths(), expected_uid=os.getuid())

    def test_refuses_outside_nspawn_and_symlink(self):
        (self.root / "run/systemd/container").unlink()
        with self.assertRaises(ValueError):
            load_guest_intent(self.paths(), expected_uid=os.getuid())
        (self.root / "run/systemd/container").write_text("docker\n")
        with self.assertRaises(ValueError):
            load_guest_intent(self.paths(), expected_uid=os.getuid())
        (self.root / "run/systemd/container").write_text("systemd-nspawn\n")
        self.intent.rename(self.intent.with_suffix(".real"))
        self.intent.symlink_to(self.intent.with_suffix(".real"))
        with self.assertRaises(ValueError):
            load_guest_intent(self.paths(), expected_uid=os.getuid())

    def test_reapply_permit_is_private_empty_and_regular(self):
        marker=self.root / "run/podlaz-nm-l3/retained-ip4-reapply"
        self.assertFalse(retained_ipv4_reapply_authorized(marker,expected_uid=os.getuid()))
        marker.touch(mode=0o600)
        self.assertTrue(retained_ipv4_reapply_authorized(marker,expected_uid=os.getuid()))
        marker.write_text("foreign")
        with self.assertRaises(ValueError):
            retained_ipv4_reapply_authorized(marker,expected_uid=os.getuid())
        marker.write_text("")
        marker.chmod(0o644)
        with self.assertRaises(ValueError):
            retained_ipv4_reapply_authorized(marker,expected_uid=os.getuid())
        marker.chmod(0o600)
        marker.rename(marker.with_suffix(".real"))
        marker.symlink_to(marker.with_suffix(".real"))
        with self.assertRaises(OSError):
            retained_ipv4_reapply_authorized(marker,expected_uid=os.getuid())

    def test_refuses_weak_permissions_and_opaque_invalid_data(self):
        self.intent.chmod(0o644)
        with self.assertRaises(ValueError):
            load_guest_intent(self.paths(), expected_uid=os.getuid())
        self.intent.chmod(0o600)
        self.intent.write_text(json.dumps({**DATA, "tun_ifindex": 0}))
        with self.assertRaises(ValueError):
            load_guest_intent(self.paths(), expected_uid=os.getuid())
        self.intent.write_text(json.dumps({**DATA, "debug": "secret.example"}))
        with self.assertRaises(ValueError):
            load_guest_intent(self.paths(), expected_uid=os.getuid())


if __name__ == "__main__":
    unittest.main()
