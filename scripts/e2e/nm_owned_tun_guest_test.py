"""Pure single-writer NM-owned TUN research contracts (disposable guest only)."""
import json
import struct
import unittest
from unittest.mock import patch

from nm_owned_tun_guest import (_prove_duplicated_fd_outlives_origin,
                                _qualified_peer_packets,
                                inspect_nm_tun_netlink_events,
                                verify_attached_tun, verify_nm_survives_xray_exit,
                                verify_live_nm_restart,
                                verify_xray_capabilities)


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

    def test_external_fd_xray_cannot_retain_privileged_link_writers(self):
        raw = 1 << 13

        def status(*, uid=1001, gid=1001, caps=None, nnp=1, groups=""):
            values = caps or {}
            lines = ["Name:\txray",
                     f"Uid:\t{uid}\t{uid}\t{uid}\t{uid}",
                     f"Gid:\t{gid}\t{gid}\t{gid}\t{gid}",
                     f"Groups:\t{groups}"]
            lines += [f"{key}:\t{values.get(key, raw):016x}" for key in
                      ("CapEff", "CapPrm", "CapInh", "CapBnd", "CapAmb")]
            return "\n".join(lines + [f"NoNewPrivs:\t{nnp}", ""])

        verify_xray_capabilities(status(), uid=1001, gid=1001)
        for field in ("CapEff", "CapPrm", "CapInh", "CapBnd", "CapAmb"):
            for invalid in (0, raw | (1 << 12), raw | (1 << 21)):
                with self.subTest(field=field, invalid=invalid):
                    with self.assertRaisesRegex(ValueError, "ownership capabilities"):
                        verify_xray_capabilities(status(caps={field: invalid}),
                                                 uid=1001, gid=1001)
        for payload in (status(uid=0), status(gid=0), status(nnp=0),
                        status(groups="0"), status().replace("CapAmb:", "Absent:")):
            with self.subTest(payload=payload[:25]):
                with self.assertRaises(ValueError):
                    verify_xray_capabilities(payload, uid=1001, gid=1001)


    def test_duplication_remains_usable_after_origin_close(self):
        # A passing witness proves non-revocation, never ownership fencing.
        link = {"ifname": "podlaz0", "ifindex": 9, "mtu": 1500,
                "linkinfo": {"info_kind": "tun"}, "flags": ["UP"]}
        calls = []

        def ioctl(fd, command, request, mutate):
            calls.append(("ioctl", fd, command))
            if command == 0x800454d2:
                request[:18] = struct.pack("16sH", b"podlaz0", 0x1001)

        def close(fd):
            calls.append(("close", fd))

        with (patch("nm_owned_tun_guest._assert_nm_owned_link", return_value=9),
              patch("nm_owned_tun_guest._link", return_value=link),
              patch("nm_owned_tun_guest.os.open", return_value=31),
              patch("nm_owned_tun_guest.os.dup", return_value=32),
              patch("nm_owned_tun_guest.os.close", side_effect=close),
              patch("nm_owned_tun_guest.fcntl.ioctl", side_effect=ioctl),
              patch("nm_owned_tun_guest._prove_physical_drop"),
              patch("nm_owned_tun_guest._prove_ipv6_no_leak")):
            _prove_duplicated_fd_outlives_origin("fixture-uuid", 9)

        self.assertLess(calls.index(("close", 31)),
                        calls.index(("ioctl", 32, 0x800454d2)))
        self.assertEqual(calls[-1], ("close", 32))

    def test_duplicate_fd_is_closed_on_failed_identity_inspection(self):
        link = {"ifname": "podlaz0", "ifindex": 9, "mtu": 1500,
                "linkinfo": {"info_kind": "tun"}, "flags": ["UP"]}
        closed = []
        with (patch("nm_owned_tun_guest._assert_nm_owned_link", return_value=9),
              patch("nm_owned_tun_guest._link", return_value=link),
              patch("nm_owned_tun_guest.os.open", return_value=31),
              patch("nm_owned_tun_guest.os.dup", return_value=32),
              patch("nm_owned_tun_guest.os.close", side_effect=closed.append),
              patch("nm_owned_tun_guest.fcntl.ioctl", side_effect=[
                  None, OSError("stale FD"),
              ])):
            with self.assertRaisesRegex(OSError, "stale FD"):
                _prove_duplicated_fd_outlives_origin("fixture-uuid", 9)
        self.assertEqual(closed, [31, 32])

    def test_live_nm_restart_needs_real_pid_change_and_exact_fd_link(self):
        verify_live_nm_restart("123", "456", 9, 9, 1500, 1500, True)
        for before_pid, after_pid, index, mtu, running, reason in (
            ("123", "123", 9, 1500, True, "PID"),
            ("0", "456", 9, 1500, True, "PID"),
            ("123", "456", 10, 1500, True, "identity"),
            ("123", "456", 9, 1400, True, "identity"),
            ("123", "456", 9, 1500, False, "Xray exited"),
        ):
            with self.subTest(case=reason):
                with self.assertRaisesRegex(ValueError, reason):
                    verify_live_nm_restart(before_pid, after_pid, 9, index,
                                           1500, mtu, running)

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
