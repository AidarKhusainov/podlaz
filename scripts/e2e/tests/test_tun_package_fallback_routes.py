from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).resolve().parents[1] / "tun-package-fallback-routes.py"
SPEC = importlib.util.spec_from_file_location("tun_package_fallback_routes", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class FallbackRouteTests(unittest.TestCase):
    def test_validated_routes_accepts_only_podlaz_owned_safe_route(self) -> None:
        route = {
            "table": "main",
            "cidr": "203.0.113.10/32",
            "via": "192.0.2.1",
            "dev": "eth0",
            "owner": "podlaz:route",
        }
        self.assertEqual(
            MODULE.validated_routes(route),
            [MODULE.OwnedRoute("-4", "main", "203.0.113.10/32", "192.0.2.1", "eth0")],
        )
        self.assertEqual(MODULE.validated_routes({**route, "owner": "foreign"}), [])
        self.assertEqual(MODULE.validated_routes({**route, "table": "42424"}), [])
        self.assertEqual(MODULE.validated_routes({**route, "dev": "eth0;rm"}), [])

    def test_transaction_routes_require_schema_and_owner(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "transaction.json"
            payload = {
                "schema_version": "podlaz.transaction.v1",
                "owner": "podlaz",
                "rollback": {
                    "routes": [
                        {
                            "table": "main",
                            "cidr": "2001:db8::1/128",
                            "dev": "eth0",
                            "owner": "podlaz:route",
                        }
                    ]
                },
            }
            path.write_text(json.dumps(payload), encoding="utf-8")
            self.assertEqual(
                MODULE.transaction_routes(path),
                [MODULE.OwnedRoute("-6", "main", "2001:db8::1/128", "", "eth0")],
            )
            path.write_text(json.dumps({**payload, "owner": "foreign"}), encoding="utf-8")
            self.assertEqual(MODULE.transaction_routes(path), [])

    @mock.patch.object(MODULE.subprocess, "run")
    def test_reserved_rule_routes_accept_only_priority_9999_target(self, run: mock.Mock) -> None:
        run.side_effect = [
            subprocess.CompletedProcess([], 0, "9999: from all to 203.0.113.10 lookup main\n10000: from all lookup 51820\n", ""),
            subprocess.CompletedProcess([], 0, "", ""),
        ]
        self.assertEqual(
            MODULE.reserved_rule_routes(),
            [MODULE.OwnedRoute("-4", "main", "203.0.113.10/32")],
        )


if __name__ == "__main__":
    unittest.main()
