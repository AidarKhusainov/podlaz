from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "tun-package-fallback-network.py"
SPEC = importlib.util.spec_from_file_location("tun_package_fallback_network_states", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def route(
    *, table: str = "51820", cidr: str = "default", via: str = "", dev: str = "podlaz0"
) -> dict[str, object]:
    return {
        "table": table,
        "cidr": cidr,
        "via": via,
        "dev": dev,
        "owner": "podlaz:route",
    }


def rule(
    *, priority: int = 10000, source: str = "all", destination: str = "", table: str = "51820"
) -> dict[str, object]:
    value: dict[str, object] = {
        "priority": priority,
        "table": table,
        "owner": "podlaz:policy-rule",
    }
    if source:
        value["from"] = source
    if destination:
        value["to"] = destination
    return value


def route_target(value: dict[str, object]) -> str:
    return f"{value['table']} {value['cidr']}"


def rule_target(value: dict[str, object]) -> str:
    selector = f"to {value['to']}" if value.get("to") else f"from {value['from']}"
    return f"priority {value['priority']} {selector} lookup {value['table']}"


def transaction_payload(
    *,
    state: str,
    desired_routes: list[dict[str, object]] | None = None,
    desired_rules: list[dict[str, object]] | None = None,
    applied_routes: list[dict[str, object]] | None = None,
    applied_rules: list[dict[str, object]] | None = None,
    rollback_routes: list[dict[str, object]] | None = None,
    rollback_rules: list[dict[str, object]] | None = None,
) -> dict[str, object]:
    desired_routes = desired_routes or []
    desired_rules = desired_rules or []
    applied_routes = [] if applied_routes is None else applied_routes
    applied_rules = [] if applied_rules is None else applied_rules
    rollback_routes = [] if rollback_routes is None else rollback_routes
    rollback_rules = [] if rollback_rules is None else rollback_rules
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
            "routes": rollback_routes,
            "policy_rules": rollback_rules,
        },
    }


class TransactionOwnershipStateTests(unittest.TestCase):
    def snapshot(self, payload: dict[str, object]) -> MODULE.NetworkManifest:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "tx.json").write_text(json.dumps(payload), encoding="utf-8")
            return MODULE.snapshot_transactions(root, Path(directory) / "manifest.json")

    def test_committed_or_verifying_zero_owned_preexisting_network_is_non_mutating(self) -> None:
        preexisting_route = route(
            table="main", cidr="203.0.113.20/32", via="192.0.2.1", dev="eth0"
        )
        preexisting_rule = rule(
            priority=9999, source="", destination="203.0.113.20/32", table="main"
        )
        for state in ("verifying", "committed"):
            with self.subTest(state=state):
                manifest = self.snapshot(
                    transaction_payload(
                        state=state,
                        desired_routes=[preexisting_route],
                        desired_rules=[preexisting_rule],
                    )
                )
                self.assertEqual(manifest.routes, ())
                self.assertEqual(manifest.rules, ())

    def test_mixed_owned_route_and_preexisting_rules_contains_only_durable_route(self) -> None:
        owned_route = route()
        preexisting_managed_rule = rule()
        preexisting_bypass_rule = rule(
            priority=9999, source="", destination="203.0.113.21/32", table="main"
        )
        manifest = self.snapshot(
            transaction_payload(
                state="committed",
                desired_routes=[owned_route],
                desired_rules=[preexisting_managed_rule, preexisting_bypass_rule],
                applied_routes=[owned_route],
                rollback_routes=[owned_route],
            )
        )
        self.assertEqual(
            manifest.routes,
            (MODULE.OwnedRoute("-4", "51820", "default", "", "podlaz0"),),
        )
        self.assertEqual(manifest.rules, ())

    def test_planned_desired_network_is_non_mutating(self) -> None:
        manifest = self.snapshot(
            transaction_payload(
                state="planned",
                desired_routes=[route()],
                desired_rules=[rule()],
            )
        )
        self.assertEqual(manifest.routes, ())
        self.assertEqual(manifest.rules, ())

    def test_planned_state_rejects_durable_network_ownership(self) -> None:
        owned_route = route()
        payload = transaction_payload(
            state="planned",
            desired_routes=[owned_route],
            applied_routes=[owned_route],
            rollback_routes=[owned_route],
        )
        with self.assertRaisesRegex(MODULE.MetadataError, "planned transaction"):
            self.snapshot(payload)

    def test_applying_without_category_ownership_proof_remains_ambiguous(self) -> None:
        owned_route = route()
        preexisting_rule = rule()
        payload = transaction_payload(
            state="applying",
            desired_routes=[owned_route],
            desired_rules=[preexisting_rule],
            applied_routes=[owned_route],
            rollback_routes=[owned_route],
        )
        with self.assertRaisesRegex(MODULE.MetadataError, "policy-rule proof"):
            self.snapshot(payload)


if __name__ == "__main__":
    unittest.main()
