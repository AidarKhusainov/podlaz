from __future__ import annotations

import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from release_acceptance.checkpoint import CheckpointStore
from release_acceptance.model import AmbiguousState, Checkpoint, UserIdentity
from release_acceptance.reboot import RebootCoordinator


class DummyRunner:
    pass


class FakeProduct:
    def __init__(self, profiles: set[str], *, terminal_profiles: set[str] | None = None):
        self.profiles = set(profiles)
        self.terminal_profiles = set(terminal_profiles or set())
        self.deleted: list[str] = []
        self.disabled = 0

    def profile_ids(self) -> set[str]:
        return set(self.profiles)

    def is_terminal_acceptance_profile(self, profile_id: str) -> bool:
        return profile_id in self.terminal_profiles

    def delete_profile(self, profile_id: str) -> None:
        self.deleted.append(profile_id)
        self.profiles.remove(profile_id)

    def autostart_disable(self) -> None:
        self.disabled += 1


class TerminalProfileRecoveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.user = UserIdentity("tester", os.getuid(), os.getgid(), self.root)
        self.store = CheckpointStore(self.root / "state" / "current.json", self.user)

    def tearDown(self):
        self.tmp.cleanup()

    def checkpoint(self, acquisition: dict) -> None:
        self.store.replace(
            Checkpoint(
                schema_version="podlaz.release-acceptance-checkpoint.v1",
                run_id="test-run",
                phase="awaiting-autostart-on-reboot",
                user={"name": self.user.name, "uid": self.user.uid, "gid": self.user.gid, "home": str(self.user.home)},
                candidate={"path": str(self.root / "candidate.deb"), "version": "2.0"},
                previous_boot_id="boot-a",
                original_autostart={"enabled": False},
                private={"terminal_profile_acquisition": acquisition},
            )
        )

    @patch("release_acceptance.reboot.restore_boot_manifest")
    def test_abort_recovers_single_profile_created_after_acquiring_checkpoint(self, restore_manifest):
        self.checkpoint({"state": "acquiring", "baseline_ids": ["existing-a", "existing-b"]})
        product = FakeProduct(
            {"existing-a", "existing-b", "synthetic-new"},
            terminal_profiles={"synthetic-new"},
        )

        RebootCoordinator(self.store, DummyRunner(), product).restore_original_policy()

        self.assertEqual(product.deleted, ["synthetic-new"])
        self.assertNotIn("terminal_profile_acquisition", self.store.load().private)
        restore_manifest.assert_called_once_with({"enabled": False})

    @patch("release_acceptance.reboot.restore_boot_manifest")
    def test_abort_refuses_ambiguous_terminal_profile_acquisition(self, restore_manifest):
        self.checkpoint({"state": "acquiring", "baseline_ids": ["existing"]})
        product = FakeProduct(
            {"existing", "extra-one", "extra-two"},
            terminal_profiles={"extra-one", "extra-two"},
        )

        with self.assertRaises(AmbiguousState):
            RebootCoordinator(self.store, DummyRunner(), product).restore_original_policy()

        self.assertEqual(product.deleted, [])
        self.assertIn("terminal_profile_acquisition", self.store.load().private)
        restore_manifest.assert_not_called()

    @patch("release_acceptance.reboot.restore_boot_manifest")
    def test_abort_refuses_single_foreign_profile_created_after_checkpoint(self, restore_manifest):
        self.checkpoint({"state": "acquiring", "baseline_ids": ["existing"]})
        product = FakeProduct({"existing", "foreign-new"}, terminal_profiles=set())

        with self.assertRaises(AmbiguousState):
            RebootCoordinator(self.store, DummyRunner(), product).restore_original_policy()

        self.assertEqual(product.deleted, [])
        self.assertIn("terminal_profile_acquisition", self.store.load().private)
        restore_manifest.assert_not_called()


if __name__ == "__main__":
    unittest.main()
