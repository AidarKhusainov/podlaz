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
SPEC = importlib.util.spec_from_file_location("tun_package_fallback_network_rolled_back", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


def rolled_back_payload() -> dict:
    return {
        "schema_version": "podlaz.transaction.v1",
        "owner": "podlaz",
        "state": "rolled_back",
        "desired_plan": {},
        "applied_steps": [],
        "rollback": {
            "routes": [
                {
                    "table": "main",
                    "cidr": "203.0.113.10/32",
                    "via": "192.0.2.1",
                    "dev": "eth0",
                    "owner": "podlaz:route",
                }
            ],
            "policy_rules": [
                {
                    "priority": 9999,
                    "to": "203.0.113.10/32",
                    "table": "main",
                    "owner": "podlaz:policy-rule",
                }
            ],
        },
    }


class RolledBackTransactionTests(unittest.TestCase):
    def snapshot(self, payload: dict) -> MODULE.NetworkManifest:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            (root / "tx.json").write_text(json.dumps(payload), encoding="utf-8")
            return MODULE.snapshot_transactions(root, Path(directory) / "manifest.json")

    def test_rolled_back_transaction_does_not_authorize_network_deletion(self) -> None:
        manifest = self.snapshot(rolled_back_payload())
        self.assertEqual(manifest.routes, ())
        self.assertEqual(manifest.rules, ())

    def test_rolled_back_transaction_validates_only_stale_record_identity(self) -> None:
        payload = rolled_back_payload()
        payload["rollback"] = {
            "routes": "stale data is intentionally non-authoritative",
            "policy_rules": None,
        }
        manifest = self.snapshot(payload)
        self.assertEqual(manifest.routes, ())
        self.assertEqual(manifest.rules, ())

        for field, value in (("schema_version", "wrong"), ("owner", "foreign")):
            invalid = rolled_back_payload()
            invalid[field] = value
            with self.subTest(field=field):
                with self.assertRaises(MODULE.MetadataError):
                    self.snapshot(invalid)

    @mock.patch.object(MODULE.subprocess, "run")
    def test_empty_rolled_back_manifest_performs_no_network_commands(self, run: mock.Mock) -> None:
        run.return_value = subprocess.CompletedProcess([], 0, "", "")
        self.assertTrue(MODULE.cleanup_manifest(MODULE.NetworkManifest(routes=(), rules=())))
        run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
