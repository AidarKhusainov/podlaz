"""Pure single-writer NM-owned TUN research contracts (disposable guest only)."""
import unittest

from nm_owned_tun_guest import verify_attached_tun, verify_nm_survives_xray_exit


class NMOwnedTunContractTest(unittest.TestCase):
    def test_same_existing_tun_identity_is_required(self):
        verify_attached_tun(12, 12, True)
        with self.assertRaisesRegex(ValueError, "changed"):
            verify_attached_tun(12, 13, True)
        with self.assertRaisesRegex(ValueError, "not running"):
            verify_attached_tun(12, 12, False)
        with self.assertRaisesRegex(ValueError, "invalid"):
            verify_attached_tun(0, 0, True)

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
