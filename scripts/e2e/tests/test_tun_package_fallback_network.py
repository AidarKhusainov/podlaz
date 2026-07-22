from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).resolve().parents[1] / "tun-package-fallback-network.py"
SPEC = importlib.util.spec_from_file_location("tun_package_fallback_network", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def transaction_payload(*, routes: list[dict] | None = None, rules: list[dict] | None = None) -> dict:
    return {
        "schema_version": "podlaz.transaction.v1",
        "owner": "podlaz",
        "state": "rolling_back",
        "desired_plan": {"routes": [], "steps": []},
        "applied_steps": [],
        "rollback": {
            "routes": routes or [],
            "policy_rules": rules or [],
        },
    }


class FallbackNetworkTests(unittest.TestCase):
    def test_snapshot_accepts_only_exact_persisted_route_and_rule_tuples(self) -> None:
        route = {
            "table": "main",
            "cidr": "203.0.113.10/32",
            "via": "192.0.2.1",
            "dev": "eth0",
            "owner": "podlaz:route",
        }
        rule = {
            "priority": 9999,
            "to": "203.0.113.10/32",
            "table": "main",
            "owner": "podlaz:policy-rule",
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "tx.json").write_text(json.dumps(transaction_payload(routes=[route], rules=[rule])), encoding="utf-8")
            manifest_path = Path(directory) / "manifest.json"

            manifest = MODULE.snapshot_transactions(root, manifest_path)

            self.assertEqual(
                manifest.routes,
                (MODULE.OwnedRoute("-4", "main", "203.0.113.10/32", "192.0.2.1", "eth0"),),
            )
            self.assertEqual(
                manifest.rules,
                (MODULE.OwnedPolicyRule("-4", 9999, "", "203.0.113.10/32", "", "main"),),
            )
            self.assertTrue(manifest_path.is_file())

    def test_snapshot_rejects_invalid_json_without_writing_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "broken.json").write_text("{", encoding="utf-8")
            manifest_path = Path(directory) / "manifest.json"

            with self.assertRaises(MODULE.MetadataError):
                MODULE.snapshot_transactions(root, manifest_path)

            self.assertFalse(manifest_path.exists())

    def test_snapshot_rejects_wrong_schema_owner_and_non_json_entries(self) -> None:
        cases = [
            {**transaction_payload(), "schema_version": "wrong"},
            {**transaction_payload(), "owner": "foreign"},
        ]
        for payload in cases:
            with self.subTest(payload=payload):
                with tempfile.TemporaryDirectory() as directory:
                    root = Path(directory) / "transactions"
                    root.mkdir()
                    (root / "tx.json").write_text(json.dumps(payload), encoding="utf-8")
                    with self.assertRaises(MODULE.MetadataError):
                        MODULE.snapshot_transactions(root, Path(directory) / "manifest.json")

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "leftover.tmp").write_text("temporary", encoding="utf-8")
            with self.assertRaises(MODULE.MetadataError):
                MODULE.snapshot_transactions(root, Path(directory) / "manifest.json")

    def test_snapshot_rejects_ambiguous_cleanup_required_transaction(self) -> None:
        payload = transaction_payload()
        payload["state"] = "applying"
        payload["desired_plan"] = {
            "routes": [{"table": "main", "cidr": "203.0.113.10/32"}],
            "steps": [{"kind": "policy-rule", "target": "priority 9999"}],
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "tx.json").write_text(json.dumps(payload), encoding="utf-8")
            with self.assertRaises(MODULE.MetadataError):
                MODULE.snapshot_transactions(root, Path(directory) / "manifest.json")

    def test_snapshot_rejects_priority_as_ownership_and_invalid_exact_tuple(self) -> None:
        base_rule = {
            "priority": 9999,
            "to": "203.0.113.10/32",
            "table": "main",
            "owner": "podlaz:policy-rule",
        }
        invalid_rules = [
            {**base_rule, "owner": "foreign"},
            {**base_rule, "priority": 10000},
            {**base_rule, "from": "all"},
            {**base_rule, "table": "42424"},
        ]
        for rule in invalid_rules:
            with self.subTest(rule=rule):
                with tempfile.TemporaryDirectory() as directory:
                    root = Path(directory) / "transactions"
                    root.mkdir()
                    (root / "tx.json").write_text(json.dumps(transaction_payload(rules=[rule])), encoding="utf-8")
                    with self.assertRaises(MODULE.MetadataError):
                        MODULE.snapshot_transactions(root, Path(directory) / "manifest.json")

    @mock.patch.object(MODULE.subprocess, "run")
    def test_cleanup_deletes_only_manifest_exact_tuples(self, run: mock.Mock) -> None:
        manifest = MODULE.NetworkManifest(
            routes=(MODULE.OwnedRoute("-4", "main", "203.0.113.10/32", "192.0.2.1", "eth0"),),
            rules=(MODULE.OwnedPolicyRule("-4", 9999, "", "203.0.113.10/32", "", "main"),),
        )
        run.side_effect = [
            subprocess.CompletedProcess([], 0, "", ""),
            subprocess.CompletedProcess([], 0, "", ""),
            subprocess.CompletedProcess([], 0, "", ""),
            subprocess.CompletedProcess([], 0, "", ""),
        ]

        self.assertTrue(MODULE.cleanup_manifest(manifest))

        commands = [call.args[0] for call in run.call_args_list]
        self.assertEqual(
            commands[0],
            ["ip", "-4", "rule", "del", "priority", "9999", "to", "203.0.113.10/32", "lookup", "main"],
        )
        self.assertEqual(
            commands[1],
            ["ip", "-4", "route", "del", "203.0.113.10/32", "via", "192.0.2.1", "dev", "eth0", "table", "main"],
        )
        self.assertNotIn(["ip", "-4", "rule", "del", "priority", "9999"], commands)

    @mock.patch.object(MODULE.subprocess, "run")
    def test_cleanup_does_not_treat_unrelated_same_priority_rule_as_recorded(self, run: mock.Mock) -> None:
        manifest = MODULE.NetworkManifest(
            routes=(),
            rules=(MODULE.OwnedPolicyRule("-4", 9999, "", "203.0.113.10/32", "", "main"),),
        )
        run.side_effect = [
            subprocess.CompletedProcess([], 2, "", "not found"),
            subprocess.CompletedProcess([], 0, "9999: from all to 198.51.100.44 lookup main\n", ""),
        ]

        self.assertTrue(MODULE.cleanup_manifest(manifest))

    @mock.patch.object(MODULE.subprocess, "run")
    def test_cleanup_fails_when_recorded_exact_tuple_remains(self, run: mock.Mock) -> None:
        manifest = MODULE.NetworkManifest(
            routes=(),
            rules=(MODULE.OwnedPolicyRule("-4", 9999, "", "203.0.113.10/32", "", "main"),),
        )
        run.side_effect = [
            subprocess.CompletedProcess([], 1, "", "failed"),
            subprocess.CompletedProcess([], 0, "9999: from all to 203.0.113.10 lookup main\n", ""),
        ]

        self.assertFalse(MODULE.cleanup_manifest(manifest))

    def test_managed_table_alias_is_normalized_to_numeric_table(self) -> None:
        route = {
            "table": "podlaz",
            "cidr": "default",
            "dev": "podlaz0",
            "owner": "podlaz:route",
        }
        rule = {
            "priority": 10000,
            "from": "all",
            "table": "podlaz",
            "owner": "podlaz:policy-rule",
        }
        self.assertEqual(MODULE.validated_route(route).table, "51820")
        self.assertEqual(MODULE.validated_policy_rule(rule).table, "51820")

    def test_committed_transaction_with_desired_network_but_no_rollback_metadata_is_ambiguous(self) -> None:
        payload = transaction_payload()
        payload["state"] = "committed"
        payload["desired_plan"] = {
            "routes": [{"table": "main", "cidr": "203.0.113.10/32"}],
            "steps": [{"kind": "policy-rule", "target": "priority 9999"}],
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "tx.json").write_text(json.dumps(payload), encoding="utf-8")
            with self.assertRaises(MODULE.MetadataError):
                MODULE.snapshot_transactions(root, Path(directory) / "manifest.json")


if __name__ == "__main__":
    unittest.main()
