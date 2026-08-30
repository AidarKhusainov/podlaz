from __future__ import annotations

import json
import os
from pathlib import Path
import tempfile
import unittest

from release_acceptance.checkpoint import CheckpointStore, MutationLedger
from release_acceptance.fixtures import FixtureLease, FIXTURE_A
from release_acceptance.model import AmbiguousState, Checkpoint, MutationRecord, MutationState, UserIdentity


class Result:
    def __init__(self, returncode: int = 0, stdout: str = "", stderr: str = ""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr

    def require_success(self, _what: str):
        if self.returncode != 0:
            raise AssertionError(self.stderr or "command failed")
        return self


class FixtureRunner:
    def __init__(self, responses: dict[tuple[str, ...], Result]):
        self.responses = responses
        self.commands: list[tuple[str, ...]] = []

    def run(self, argv, *, timeout, user=None, env=None, input_text=None):
        command = tuple(argv)
        self.commands.append(command)
        return self.responses.get(command, Result())


class FixtureRecoveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        self.user = UserIdentity("tester", os.getuid(), os.getgid(), root)
        self.store = CheckpointStore(root / "state" / "current.json", self.user)
        identity = FIXTURE_A.__dict__.copy()
        identity["current_route"] = FIXTURE_A.route
        self.store.replace(
            Checkpoint(
                schema_version="podlaz.release-acceptance-checkpoint.v1",
                run_id="fixture-test",
                phase="running-pre-reboot",
                user={"name": self.user.name, "uid": self.user.uid, "gid": self.user.gid, "home": str(self.user.home)},
                candidate={"path": str(root / "candidate.deb"), "version": "2.0"},
                previous_boot_id="boot-a",
                mutations={
                    FIXTURE_A.name: MutationRecord(
                        MutationState.ACQUIRING,
                        "network_fixture",
                        identity,
                    )
                },
            )
        )

    def tearDown(self):
        self.tmp.cleanup()

    def test_partial_fixture_cleanup_removes_only_exact_present_components(self):
        s = FIXTURE_A
        responses = {
            ("ip", "-j", "-d", "link", "show", "dev", s.tun): Result(stdout=json.dumps([{"ifname": s.tun, "linkinfo": {"info_kind": "tun"}}])),
            ("ip", "-j", "-d", "link", "show", "dev", s.dns_link): Result(returncode=1),
            ("ip", "-j", "-4", "route", "show", "table", s.table): Result(stdout=json.dumps([{"type": "blackhole", "dst": s.route, "table": int(s.table)}])),
            ("ip", "-4", "rule", "show", "priority", str(s.priority_a)): Result(stdout=f"{s.priority_a}: from all to {s.rule_a} lookup {s.table}\n"),
            ("ip", "-4", "rule", "show", "priority", str(s.priority_b)): Result(returncode=1),
            ("nft", "-j", "list", "table", "inet", s.nft_table): Result(returncode=1),
        }
        runner = FixtureRunner(responses)
        lease = FixtureLease(runner, MutationLedger(self.store), s)

        lease.release_partial()

        self.assertIn(("ip", "-4", "rule", "del", "priority", str(s.priority_a), "to", s.rule_a, "lookup", s.table), runner.commands)
        self.assertIn(("ip", "-4", "route", "del", "blackhole", s.route, "table", s.table), runner.commands)
        self.assertIn(("ip", "link", "del", "dev", s.tun), runner.commands)
        self.assertNotIn(("ip", "link", "del", "dev", s.dns_link), runner.commands)
        self.assertEqual(self.store.load().mutations[s.name].state, MutationState.RELEASED)

    def test_partial_fixture_cleanup_refuses_foreign_route_in_owned_table(self):
        s = FIXTURE_A
        responses = {
            ("ip", "-j", "-d", "link", "show", "dev", s.tun): Result(returncode=1),
            ("ip", "-j", "-d", "link", "show", "dev", s.dns_link): Result(returncode=1),
            ("ip", "-j", "-4", "route", "show", "table", s.table): Result(stdout=json.dumps([{"type": "unicast", "dst": "203.0.113.7/32", "table": int(s.table)}])),
            ("ip", "-4", "rule", "show", "priority", str(s.priority_a)): Result(returncode=1),
            ("ip", "-4", "rule", "show", "priority", str(s.priority_b)): Result(returncode=1),
            ("nft", "-j", "list", "table", "inet", s.nft_table): Result(returncode=1),
        }
        runner = FixtureRunner(responses)
        lease = FixtureLease(runner, MutationLedger(self.store), s)

        with self.assertRaises(AmbiguousState):
            lease.release_partial()

        self.assertEqual(self.store.load().mutations[s.name].state, MutationState.ACQUIRING)
        self.assertNotIn(("ip", "-4", "route", "del", "blackhole", s.route, "table", s.table), runner.commands)

    def test_partial_fixture_cleanup_refuses_foreign_link_kind(self):
        s = FIXTURE_A
        responses = {
            ("ip", "-j", "-d", "link", "show", "dev", s.tun): Result(stdout=json.dumps([{"ifname": s.tun, "linkinfo": {"info_kind": "dummy"}}])),
            ("ip", "-j", "-d", "link", "show", "dev", s.dns_link): Result(returncode=1),
            ("ip", "-j", "-4", "route", "show", "table", s.table): Result(stdout="[]"),
            ("ip", "-4", "rule", "show", "priority", str(s.priority_a)): Result(returncode=1),
            ("ip", "-4", "rule", "show", "priority", str(s.priority_b)): Result(returncode=1),
            ("nft", "-j", "list", "table", "inet", s.nft_table): Result(returncode=1),
        }
        runner = FixtureRunner(responses)
        lease = FixtureLease(runner, MutationLedger(self.store), s)

        with self.assertRaises(AmbiguousState):
            lease.release_partial()

        self.assertEqual(self.store.load().mutations[s.name].state, MutationState.ACQUIRING)
        self.assertNotIn(("ip", "link", "del", "dev", s.tun), runner.commands)


if __name__ == "__main__":
    unittest.main()
