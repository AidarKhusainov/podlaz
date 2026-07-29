from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

HELPER_PATH = Path(__file__).resolve().parents[1] / "tun-package-verification-network.py"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


VERIFICATION = load_module("tun_package_verification_network", HELPER_PATH)


def desired_main_route() -> dict[str, object]:
    return {
        "kind": "route",
        "operation": "add",
        "owner": "podlaz:route",
        "table": "main",
        "cidr": "203.0.113.10/32",
        "via": "192.0.2.1",
        "dev": "eth0",
    }


def desired_main_rule() -> dict[str, object]:
    return {
        "kind": "policy-rule",
        "target": "priority 9999 to 203.0.113.10/32 lookup main",
        "owner": "podlaz:policy-rule",
    }


def transaction(*, state: str, durable: bool = False) -> dict[str, object]:
    route = desired_main_route()
    rule = desired_main_rule()
    applied_steps = []
    rollback_routes = []
    rollback_rules = []
    if durable:
        applied_steps = [
            {"kind": "route", "target": "main 203.0.113.10/32", "owner": "podlaz:route"},
            rule.copy(),
        ]
        rollback_routes = [route.copy()]
        rollback_rules = [
            {
                "owner": "podlaz:policy-rule",
                "priority": 9999,
                "to": "203.0.113.10/32",
                "table": "main",
            }
        ]
    return {
        "schema_version": "podlaz.transaction.v1",
        "owner": "podlaz",
        "state": state,
        "desired_plan": {"routes": [route], "steps": [rule]},
        "applied_steps": applied_steps,
        "rollback": {"routes": rollback_routes, "policy_rules": rollback_rules},
    }


ROUTE_PRESENT = "203.0.113.10 via 192.0.2.1 dev eth0\n"
RULE_PRESENT = "9999: from all to 203.0.113.10 lookup main\n"


class VerificationNetworkTests(unittest.TestCase):
    def snapshot(self, payload: dict[str, object]):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "tx.json").write_text(json.dumps(payload), encoding="utf-8")
            manifest_path = Path(directory) / "verification.json"
            manifest = VERIFICATION.snapshot_verification_transactions(root, manifest_path)
            self.assertTrue(manifest_path.is_file())
            return manifest

    def test_unowned_desired_tuples_present_at_capture_are_not_obligations(self) -> None:
        for state in ("planned", "verifying", "committed"):
            with self.subTest(state=state), mock.patch.object(
                VERIFICATION.NETWORK,
                "_inspection_output",
                side_effect=[ROUTE_PRESENT, RULE_PRESENT],
            ):
                verification = self.snapshot(transaction(state=state))
                self.assertEqual(verification.routes, ())
                self.assertEqual(verification.rules, ())

    def test_applying_desired_tuples_are_obligations_without_host_baseline(self) -> None:
        with mock.patch.object(VERIFICATION.NETWORK, "_inspection_output") as inspect:
            verification = self.snapshot(transaction(state="applying"))
        self.assertEqual(len(verification.routes), 1)
        self.assertEqual(len(verification.rules), 1)
        inspect.assert_not_called()

    def test_unowned_desired_tuples_absent_at_capture_become_obligations(self) -> None:
        with mock.patch.object(VERIFICATION.NETWORK, "_inspection_output", return_value=""):
            verification = self.snapshot(transaction(state="verifying"))
        self.assertEqual(
            verification.routes,
            (
                VERIFICATION.NETWORK.OwnedRoute(
                    "-4", "main", "203.0.113.10/32", "192.0.2.1", "eth0"
                ),
            ),
        )
        self.assertEqual(
            verification.rules,
            (
                VERIFICATION.NETWORK.OwnedPolicyRule(
                    "-4", 9999, "", "203.0.113.10/32", "", "main"
                ),
            ),
        )

    def test_durable_owned_tuples_remain_obligations_when_present_at_capture(self) -> None:
        with mock.patch.object(VERIFICATION.NETWORK, "_inspection_output", return_value=ROUTE_PRESENT):
            verification = self.snapshot(transaction(state="verifying", durable=True))
        self.assertEqual(len(verification.routes), 1)
        self.assertEqual(len(verification.rules), 1)

    def test_verification_snapshot_ignores_terminal_rolled_back_intent(self) -> None:
        with mock.patch.object(VERIFICATION.NETWORK, "_inspection_output") as inspect:
            manifest = self.snapshot(transaction(state="rolled_back"))
        self.assertEqual(manifest.routes, ())
        self.assertEqual(manifest.rules, ())
        inspect.assert_not_called()


if __name__ == "__main__":
    unittest.main()
