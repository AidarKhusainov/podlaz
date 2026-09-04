from __future__ import annotations

import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
CLEANUP_HELPER = ROOT / "scripts" / "e2e" / "lib" / "tun_soak_cleanup.sh"


class TunSoakCleanupTests(unittest.TestCase):
    def run_cleanup(self, outcomes: str) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as directory:
            script = textwrap.dedent(
                f"""
                set -u
                source {str(CLEANUP_HELPER)!r}

                SOAK_PRIVATE_DIR={directory!r}
                PODLAZ_E2E_SOAK_CLEANUP_ATTEMPTS=2
                PODLAZ_E2E_SOAK_CLEANUP_RETRY_SECONDS=0
                ATTEMPTS=0
                OUTCOMES={outcomes!r}

                run_tun_package_cleanup_once() {{
                  ATTEMPTS=$((ATTEMPTS + 1))
                  printf 'PRIVATE-CLEANUP-STDOUT-%s\\n' "${{ATTEMPTS}}"
                  printf 'PRIVATE-CLEANUP-STDERR-%s\\n' "${{ATTEMPTS}}" >&2
                  outcome="${{OUTCOMES:${{ATTEMPTS}}-1:1}}"
                  return "${{outcome}}"
                }}

                if run_tun_soak_cleanup test; then
                  result=0
                else
                  result=$?
                fi
                printf 'result=%s attempts=%s\\n' "${{result}}" "${{ATTEMPTS}}"
                """
            )
            return subprocess.run(
                ["bash", "-c", script],
                cwd=ROOT,
                text=True,
                capture_output=True,
                check=False,
            )

    def test_first_success_uses_one_cleanup_attempt(self) -> None:
        result = self.run_cleanup("0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "result=0 attempts=1\n")
        self.assertEqual(result.stderr, "")

    def test_transient_cleanup_failure_is_retried_once(self) -> None:
        result = self.run_cleanup("10")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "result=0 attempts=2\n")
        self.assertEqual(result.stderr, "")

    def test_two_cleanup_failures_return_structural_error_without_private_output(self) -> None:
        result = self.run_cleanup("11")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "result=1 attempts=2\n")
        self.assertIn("ERROR: test cleanup failed after 2 bounded attempts", result.stderr)
        self.assertNotIn("PRIVATE-CLEANUP", result.stderr)
        self.assertNotIn("PRIVATE-CLEANUP", result.stdout)


if __name__ == "__main__":
    unittest.main()
