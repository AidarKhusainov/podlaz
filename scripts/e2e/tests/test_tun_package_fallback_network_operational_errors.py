from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

MODULE_PATH = Path(__file__).resolve().parents[1] / "tun-package-fallback-network.py"
SPEC = importlib.util.spec_from_file_location("tun_package_fallback_network_operational", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class FallbackNetworkOperationalErrorTests(unittest.TestCase):
    def test_snapshot_rejects_transaction_directory_stat_error(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            manifest = Path(directory) / "manifest.json"
            with mock.patch.object(Path, "stat", side_effect=PermissionError("denied")):
                with self.assertRaisesRegex(MODULE.MetadataError, "cannot be inspected"):
                    MODULE.snapshot_transactions(root, manifest)
            self.assertFalse(manifest.exists())

    def test_snapshot_rejects_transaction_directory_iteration_error(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "transactions"
            root.mkdir()
            manifest = Path(directory) / "manifest.json"
            with mock.patch.object(Path, "iterdir", side_effect=OSError("I/O failure")):
                with self.assertRaisesRegex(MODULE.MetadataError, "cannot be read"):
                    MODULE.snapshot_transactions(root, manifest)
            self.assertFalse(manifest.exists())

    def test_verify_rejects_inspection_command_launch_error(self) -> None:
        manifest = MODULE.NetworkManifest(
            routes=(),
            rules=(MODULE.OwnedPolicyRule("-4", 10000, "all", "", "", "51820"),),
        )
        with mock.patch.object(MODULE.subprocess, "run", side_effect=PermissionError("denied")):
            with self.assertRaisesRegex(MODULE.InspectionError, "launch failed"):
                MODULE.verify_manifest(manifest)


if __name__ == "__main__":
    unittest.main()
