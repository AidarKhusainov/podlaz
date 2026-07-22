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


def route(
    *, table: str = "51820", cidr: str = "default", via: str = "", dev: str = "podlaz0"
) -> dict:
    return {
        "table": table,
        "cidr": cidr,
        "via": via,
        "dev": dev,
        "owner": "podlaz:route",
    }


def rule(
    *, priority: int = 10000, source: str = "all", destination: str = "", table: str = "51820"
) -> dict:
    value = {
        "priority": priority,
        "table": table,
        "owner": "podlaz:policy-rule",
    }
    if source:
        value["from"] = source
    if destination:
        value["to"] = destination
    return value


def route_target(value: dict) -> str:
    return f"{value['table']} {value['cidr']}"


def rule_target(value: dict) -> str:
    selector = f"to {value['to']}" if value.get("to") else f"from {value['from']}"
    return f"priority {value['priority']} {selector} lookup {value['table']}"


def transaction_payload(
    *,
    desired_routes: list[dict] | None = None,
    desired_rules: list[dict] | None = None,
    applied_routes: list[dict] | None = None,
    applied_rules: list[dict] | None = None,
    rollback_routes: list[dict] | None = None,
    rollback_rules: list[dict] | None = None,
    state: str = "rolling_back",
) -> dict:
    desired_routes = desired_routes or []
    desired_rules = desired_rules or []
    applied_routes = desired_routes if applied_routes is None else applied_routes
    applied_rules = desired_rules if applied_rules is None else applied_rules
    return {
        "schema_version": "podlaz.transaction.v1",
        "owner": "podlaz",
        "state": state,
        "desired_plan": {
            "routes": [
                {
                    "kind": "route",
                    "table": item["table"],
                    "cidr": item["cidr"],
                    "via": item.get("via", ""),
                    "dev": item.get("dev", ""),
                    "owner": "podlaz:route",
                    "operation": "add",
                }
                for item in desired_routes
            ],
            "steps": [
                {
                    "kind": "policy-rule",
                    "target": rule_target(item),
                    "owner": "podlaz:policy-rule",
                }
                for item in desired_rules
            ],
        },
        "applied_steps": [
            {
                "kind": "route",
                "target": route_target(item),
                "owner": "podlaz:route",
            }
            for item in applied_routes
        ]
        + [
            {
                "kind": "policy-rule",
                "target": rule_target(item),
                "owner": "podlaz:policy-rule",
            }
            for item in applied_rules
        ],
        "rollback": {
            "routes": desired_routes if rollback_routes is None else rollback_routes,
            "policy_rules": desired_rules if rollback_rules is None else rollback_rules,
        },
    }


class FallbackNetworkTests(unittest.TestCase):
    def snapshot(self, payload: dict) -> MODULE.NetworkManifest:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "tx.json").write_text(json.dumps(payload), encoding="utf-8")
            return MODULE.snapshot_transactions(root, Path(directory) / "manifest.json")

    def test_snapshot_accepts_only_exact_persisted_route_and_rule_tuples(self) -> None:
        bypass_route = route(
            table="main", cidr="203.0.113.10/32", via="192.0.2.1", dev="eth0"
        )
        bypass_rule = rule(
            priority=9999, source="", destination="203.0.113.10/32", table="main"
        )
        manifest = self.snapshot(
            transaction_payload(desired_routes=[bypass_route], desired_rules=[bypass_rule])
        )
        self.assertEqual(
            manifest.routes,
            (MODULE.OwnedRoute("-4", "main", "203.0.113.10/32", "192.0.2.1", "eth0"),),
        )
        self.assertEqual(
            manifest.rules,
            (MODULE.OwnedPolicyRule("-4", 9999, "", "203.0.113.10/32", "", "main"),),
        )

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
                with self.assertRaises(MODULE.MetadataError):
                    self.snapshot(payload)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "leftover.tmp").write_text("temporary", encoding="utf-8")
            with self.assertRaises(MODULE.MetadataError):
                MODULE.snapshot_transactions(root, Path(directory) / "manifest.json")

    def test_snapshot_rejects_two_applied_routes_with_one_rollback_route(self) -> None:
        managed = route()
        bypass = route(table="main", cidr="203.0.113.10/32", via="192.0.2.1", dev="eth0")
        payload = transaction_payload(
            desired_routes=[managed, bypass],
            rollback_routes=[managed],
        )
        with self.assertRaisesRegex(MODULE.MetadataError, "exactly cover applied route"):
            self.snapshot(payload)

    def test_snapshot_preserves_exact_duplicate_rule_count(self) -> None:
        duplicate = rule()
        manifest = self.snapshot(
            transaction_payload(
                desired_rules=[duplicate, duplicate],
                applied_rules=[duplicate, duplicate],
                rollback_rules=[duplicate, duplicate],
            )
        )
        self.assertEqual(
            manifest.rules,
            (
                MODULE.OwnedPolicyRule("-4", 10000, "all", "", "", "51820"),
                MODULE.OwnedPolicyRule("-4", 10000, "all", "", "", "51820"),
            ),
        )

    def test_snapshot_rejects_two_applied_rules_with_one_rollback_rule(self) -> None:
        managed = rule()
        bypass = rule(priority=9999, source="", destination="203.0.113.10/32", table="main")
        payload = transaction_payload(
            desired_rules=[managed, bypass],
            rollback_rules=[managed],
        )
        with self.assertRaisesRegex(MODULE.MetadataError, "exactly cover applied policy"):
            self.snapshot(payload)

    def test_snapshot_rejects_missing_main_table_bypass_tuple(self) -> None:
        managed = route()
        bypass = route(table="main", cidr="203.0.113.10/32", via="192.0.2.1", dev="eth0")
        payload = transaction_payload(
            desired_routes=[managed, bypass],
            applied_routes=[managed, bypass],
            rollback_routes=[managed],
        )
        with self.assertRaises(MODULE.MetadataError):
            self.snapshot(payload)

    def test_snapshot_rejects_applied_target_or_owner_not_in_desired_plan(self) -> None:
        managed = route()
        payload = transaction_payload(desired_routes=[managed])
        payload["applied_steps"][0]["target"] = "main 203.0.113.77/32"
        with self.assertRaisesRegex(MODULE.MetadataError, "absent from desired"):
            self.snapshot(payload)
        payload = transaction_payload(desired_routes=[managed])
        payload["applied_steps"][0]["owner"] = "podlaz"
        with self.assertRaisesRegex(MODULE.MetadataError, "invalid owner"):
            self.snapshot(payload)

    def test_applying_rejects_desired_network_without_applied_proof(self) -> None:
        payload = transaction_payload(
            desired_routes=[route()],
            applied_routes=[],
            rollback_routes=[],
            state="applying",
        )
        with self.assertRaisesRegex(MODULE.MetadataError, "lacks applied route proof"):
            self.snapshot(payload)

    def test_snapshot_rejects_priority_as_ownership_and_invalid_exact_tuple(self) -> None:
        base_rule = rule(priority=9999, source="", destination="203.0.113.10/32", table="main")
        invalid_rules = [
            {**base_rule, "owner": "foreign"},
            {**base_rule, "owner": "podlaz"},
            {**base_rule, "priority": 10000},
            {**base_rule, "from": "all"},
            {**base_rule, "table": "42424"},
        ]
        for invalid in invalid_rules:
            with self.subTest(rule=invalid):
                with self.assertRaises(MODULE.MetadataError):
                    MODULE.validated_policy_rule(invalid)

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

    @mock.patch.object(MODULE.subprocess, "run")
    def test_verify_rejects_nonzero_rule_inspection(self, run: mock.Mock) -> None:
        manifest = MODULE.NetworkManifest(
            routes=(),
            rules=(MODULE.OwnedPolicyRule("-4", 10000, "all", "", "", "51820"),),
        )
        run.return_value = subprocess.CompletedProcess([], 2, "", "netlink failure")
        with self.assertRaises(MODULE.InspectionError):
            MODULE.verify_manifest(manifest)

    @mock.patch.object(MODULE.subprocess, "run")
    def test_verify_rejects_nonzero_route_inspection(self, run: mock.Mock) -> None:
        manifest = MODULE.NetworkManifest(
            routes=(MODULE.OwnedRoute("-4", "51820", "default", "", "podlaz0"),),
            rules=(),
        )
        run.return_value = subprocess.CompletedProcess([], 1, "", "permission denied")
        with self.assertRaises(MODULE.InspectionError):
            MODULE.verify_manifest(manifest)

    @mock.patch.object(MODULE.subprocess, "run")
    def test_verify_normalizes_observed_podlaz_lookup_alias(self, run: mock.Mock) -> None:
        manifest = MODULE.NetworkManifest(
            routes=(),
            rules=(MODULE.OwnedPolicyRule("-4", 10000, "all", "", "", "51820"),),
        )
        run.return_value = subprocess.CompletedProcess(
            [], 0, "10000: from all lookup podlaz\n", ""
        )
        self.assertFalse(MODULE.verify_manifest(manifest))

    def test_managed_table_alias_is_normalized_to_numeric_table(self) -> None:
        self.assertEqual(MODULE.validated_route(route(table="podlaz")).table, "51820")
        self.assertEqual(MODULE.validated_policy_rule(rule(table="podlaz")).table, "51820")

    def test_committed_desired_network_without_owned_steps_is_non_mutating(self) -> None:
        payload = transaction_payload(
            desired_routes=[route()],
            applied_routes=[],
            rollback_routes=[],
            state="committed",
        )
        manifest = self.snapshot(payload)
        self.assertEqual(manifest.routes, ())
        self.assertEqual(manifest.rules, ())


if __name__ == "__main__":
    unittest.main()
