from __future__ import annotations

import os
from pathlib import Path
import tempfile
import unittest

from release_acceptance.checkpoint import CheckpointStore, MutationLedger
from release_acceptance.model import Checkpoint, MutationRecord, MutationState, UserIdentity
from release_acceptance.orchestrator import ReleaseAcceptance


class Result:
    def __init__(self, returncode: int = 0, stdout: str = "", stderr: str = ""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr

    def require_success(self, _what: str):
        if self.returncode != 0:
            raise AssertionError(self.stderr or "command failed")
        return self


class RecordingRunner:
    def __init__(self):
        self.commands: list[tuple[str, ...]] = []

    def run(self, argv, *, timeout, user=None, env=None, input_text=None):
        command = tuple(argv)
        self.commands.append(command)
        return Result()


class FakePackages:
    def __init__(self, installed: str):
        self.installed = installed
        self.installs: list[str] = []

    def installed_version(self) -> str:
        return self.installed

    def install_exact(self, identity) -> None:
        self.installs.append(identity.version)
        self.installed = identity.version


class Identity:
    def __init__(self, path: Path, version: str):
        self.path = path
        self.version = version


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.user = UserIdentity("tester", os.getuid(), os.getgid(), self.root)
        self.store = CheckpointStore(self.root / "state" / "current.json", self.user)
        self.harness = ReleaseAcceptance(self.user, self.store.path)

    def tearDown(self):
        self.tmp.cleanup()

    def checkpoint(self, *, mutation: tuple[str, MutationRecord] | None = None) -> None:
        mutations = {} if mutation is None else {mutation[0]: mutation[1]}
        self.store.replace(
            Checkpoint(
                schema_version="podlaz.release-acceptance-checkpoint.v1",
                run_id="test-run",
                phase="initializing",
                user={"name": self.user.name, "uid": self.user.uid, "gid": self.user.gid, "home": str(self.user.home)},
                candidate={"path": str(self.root / "candidate.deb"), "version": "2.0"},
                previous_boot_id="boot-a",
                mutations=mutations,
                private={"artifact_root": str(self.root / "artifacts")},
            )
        )

    def test_abort_restores_acquired_networkmanager_connection(self):
        self.checkpoint(
            mutation=(
                "wifi_reconnect",
                MutationRecord(
                    MutationState.ACQUIRED,
                    "networkmanager_connection",
                    {"connection": "acceptance-wifi", "uplink": "uplink0"},
                ),
            )
        )
        runner = RecordingRunner()

        self.harness._cleanup_owned_mutations(runner)

        self.assertIn(("nmcli", "connection", "up", "acceptance-wifi"), runner.commands)
        self.assertEqual(self.store.load().mutations["wifi_reconnect"].state, MutationState.RELEASED)

    def test_abort_finishes_acquiring_systemd_dropin_as_released(self):
        dropin = self.root / "99-acceptance.conf"
        hook_dir = self.root / "hooks"
        dropin.write_text("[Service]\n", encoding="utf-8")
        hook_dir.mkdir()
        (hook_dir / "marker").write_text("x", encoding="utf-8")
        self.checkpoint(
            mutation=(
                "fault_hook",
                MutationRecord(
                    MutationState.ACQUIRING,
                    "systemd_dropin",
                    {"path": str(dropin), "hook_dir": str(hook_dir)},
                ),
            )
        )

        self.harness._cleanup_owned_mutations(RecordingRunner())

        self.assertFalse(dropin.exists())
        self.assertFalse(hook_dir.exists())
        self.assertEqual(self.store.load().mutations["fault_hook"].state, MutationState.RELEASED)

    def test_package_setup_reconciliation_restores_candidate_from_exact_previous(self):
        candidate_path = self.root / "candidate.deb"
        previous_path = self.root / "previous.deb"
        candidate_path.write_bytes(b"candidate")
        previous_path.write_bytes(b"previous")
        self.checkpoint(
            mutation=(
                "package_setup",
                MutationRecord(
                    MutationState.ACQUIRED,
                    "previous_package",
                    {
                        "previous_path": str(previous_path),
                        "previous_version": "1.0",
                        "candidate_path": str(candidate_path),
                        "candidate_version": "2.0",
                    },
                ),
            )
        )
        packages = FakePackages("1.0")
        candidate = Identity(candidate_path, "2.0")
        previous = Identity(previous_path, "1.0")

        self.harness._reconcile_package_setup(packages, candidate, previous, MutationLedger(self.store))

        self.assertEqual(packages.installs, ["2.0"])
        self.assertEqual(packages.installed_version(), "2.0")
        self.assertEqual(self.store.load().mutations["package_setup"].state, MutationState.RELEASED)


if __name__ == "__main__":
    unittest.main()
