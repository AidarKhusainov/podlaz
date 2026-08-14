from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from scripts.e2e.lib import tun_soak_environment


class TunSoakEnvironmentTests(unittest.TestCase):
    def test_ubuntu_2404_is_reported_as_normalized_runtime_provenance(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "os-release"
            path.write_text('NAME="Ubuntu"\nID=ubuntu\nVERSION_ID="24.04"\n', encoding="utf-8")

            self.assertEqual(
                {"id": "ubuntu", "version_id": "24.04"},
                tun_soak_environment.verify_runtime_os(path),
            )

    def test_mislabeled_non_ubuntu_runner_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "os-release"
            path.write_text('ID=debian\nVERSION_ID="13"\n', encoding="utf-8")

            with self.assertRaisesRegex(ValueError, "Ubuntu 24.04"):
                tun_soak_environment.verify_runtime_os(path)


if __name__ == "__main__":
    unittest.main()
