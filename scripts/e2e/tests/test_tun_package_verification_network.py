from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

HELPER_PATH = Path(__file__).resolve().parents[1] / "tun-package-verification-network.py"
FALLBACK_PATH = Path(__file__).resolve().parents[1] / "tun-package-fallback-network.py"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


VERIFICATION = load_module("tun_package_verification_network", HELPER_PATH)
FALLBACK = load_module("tun_package_fallback_network_for_verification_test", FALLBACK_PATH)


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


def transaction(*, state: str) -> dict[str, object]:
    return {
        "schema_version": "podlaz.transaction.v1",
        "owner": "podlaz",
        "state": state,
        "desired_plan": {
            "routes": [desired_main_route()],
            "steps": [],
        },
        "applied_steps": [],
        "rollback": {
            "routes": [],
            "policy_rules": [],
        },
    }


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

    def test_verification_snapshot_includes_unapplied_exact_desired_tuple(self) -> None:
        payload = transaction(state="verifying")

        verification = self.snapshot(payload)
        self.assertEqual(len(verification.routes), 1)
        captured = verification.routes[0]
        self.assertEqual(
            (
                captured.family,
                captured.table,
                captured.cidr,
                captured.via,
                captured.dev,
            ),
            ("-4", "main", "203.0.113.10/32", "192.0.2.1", "eth0"),
        )

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "tx.json").write_text(json.dumps(payload), encoding="utf-8")
            authoritative = FALLBACK.snapshot_transactions(root, Path(directory) / "authoritative.json")
        self.assertEqual(authoritative.routes, ())

    def test_verification_snapshot_ignores_terminal_rolled_back_intent(self) -> None:
        manifest = self.snapshot(transaction(state="rolled_back"))
        self.assertEqual(manifest.routes, ())
        self.assertEqual(manifest.rules, ())


if __name__ == "__main__":
    unittest.main()
