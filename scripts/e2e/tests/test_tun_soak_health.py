from __future__ import annotations

import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
HEALTH_HELPER = ROOT / "scripts" / "e2e" / "lib" / "tun_soak_health.sh"
METRICS_TOOL = ROOT / "scripts" / "e2e" / "lib" / "tun_soak_metrics.py"


class TunSoakHealthTests(unittest.TestCase):
    def run_probe(self, *, exit_code: int, stderr: str = "") -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as directory:
            script = textwrap.dedent(
                f"""
                set -u
                source {str(HEALTH_HELPER)!r}

                SOAK_PRIVATE_DIR={directory!r}
                METRICS_TOOL={str(METRICS_TOOL)!r}
                SOAK_COMMAND_EXIT=""
                SOAK_COMMAND_CLASSIFICATION=""
                SOAK_STATUS_VERDICT=""
                DOCTOR_RUNS=0
                DOCTOR_UNHEALTHY_RUNS=0
                VERIFIED_CALLS=0
                FAKE_EXIT={exit_code}
                FAKE_STDERR={stderr!r}

                fail() {{
                  printf 'FAIL:%s\\n' "$*" >&2
                  return 1
                }}

                run_tun_diagnostic_command() {{
                  printf 'PRIVATE-DOCTOR-STDOUT\\n'
                  printf '%s\\n' "${{FAKE_STDERR}}" >&2
                  return "${{FAKE_EXIT}}"
                }}

                wait_for_verified_tun_status() {{
                  VERIFIED_CALLS=$((VERIFIED_CALLS + 1))
                  printf '%s' "$1" >"${{SOAK_PRIVATE_DIR}}/verified-label"
                  return 0
                }}

                if run_bounded_tun_diagnostic active; then
                  result=0
                else
                  result=$?
                fi
                verified_label=""
                if [[ -f "${{SOAK_PRIVATE_DIR}}/verified-label" ]]; then
                  verified_label="$(cat "${{SOAK_PRIVATE_DIR}}/verified-label")"
                fi
                printf 'result=%s runs=%s unhealthy=%s verified_calls=%s verified_label=%s command_exit=%s classification=%s\\n' \\
                  "${{result}}" "${{DOCTOR_RUNS}}" "${{DOCTOR_UNHEALTHY_RUNS}}" "${{VERIFIED_CALLS}}" \\
                  "${{verified_label}}" "${{SOAK_COMMAND_EXIT}}" "${{SOAK_COMMAND_CLASSIFICATION}}"
                """
            )
            return subprocess.run(
                ["bash", "-c", script],
                cwd=ROOT,
                text=True,
                capture_output=True,
                check=False,
            )

    def test_exit_zero_is_bounded_read_only_success(self) -> None:
        result = self.run_probe(exit_code=0)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(
            "result=0 runs=1 unhealthy=0 verified_calls=1 verified_label=active-post-doctor command_exit= classification=",
            result.stdout,
        )
        self.assertNotIn("PRIVATE-DOCTOR", result.stdout)
        self.assertNotIn("PRIVATE-DOCTOR", result.stderr)

    def test_diagnostic_exit_three_is_observed_but_not_a_soak_failure(self) -> None:
        result = self.run_probe(exit_code=3)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(
            "result=0 runs=1 unhealthy=1 verified_calls=1 verified_label=active-post-doctor command_exit= classification=",
            result.stdout,
        )
        self.assertNotIn("PRIVATE-DOCTOR", result.stdout)
        self.assertNotIn("PRIVATE-DOCTOR", result.stderr)

    def test_non_diagnostic_exit_fails_with_allowlisted_classification(self) -> None:
        result = self.run_probe(exit_code=5, stderr="daemon is unavailable")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(
            "result=1 runs=1 unhealthy=0 verified_calls=0 verified_label= command_exit=5 classification=daemon-unavailable",
            result.stdout,
        )
        self.assertIn("FAIL:active TUN diagnostic command failed", result.stderr)
        self.assertNotIn("daemon is unavailable", result.stderr)
        self.assertNotIn("PRIVATE-DOCTOR", result.stdout)
        self.assertNotIn("PRIVATE-DOCTOR", result.stderr)

    def test_hanging_status_is_terminated_by_per_invocation_timeout(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            script = textwrap.dedent(
                f"""
                set -u
                source {str(HEALTH_HELPER)!r}

                SOAK_PRIVATE_DIR={directory!r}
                TUN_SOAK_STATUS_TOOL={str(ROOT / 'scripts' / 'e2e' / 'lib' / 'tun_soak_status.py')!r}
                METRICS_TOOL={str(METRICS_TOOL)!r}
                PODLAZ_E2E_TUN_HEALTH_TIMEOUT_SECONDS=2
                PODLAZ_E2E_TUN_STATUS_TIMEOUT_SECONDS=1
                PODLAZ_E2E_TUN_HEALTH_POLL_SECONDS=1
                SOAK_COMMAND_EXIT=""
                SOAK_COMMAND_CLASSIFICATION=""
                SOAK_STATUS_VERDICT=""
                BOUNDED_CALLS=0
                UNBOUNDED_CALLS=0

                fail() {{
                  printf 'FAIL:%s\n' "$*" >&2
                  return 1
                }}

                run_installed_podlaz_bounded() {{
                  local seconds="$1"
                  shift
                  BOUNDED_CALLS=$((BOUNDED_CALLS + 1))
                  timeout --signal=TERM --kill-after=1s "${{seconds}}s" bash -c 'sleep 30'
                }}

                run_installed_podlaz() {{
                  UNBOUNDED_CALLS=$((UNBOUNDED_CALLS + 1))
                  return 99
                }}

                start="$(date +%s)"
                if wait_for_verified_tun_status hanging; then
                  result=0
                else
                  result=$?
                fi
                elapsed=$(( $(date +%s) - start ))
                printf 'result=%s elapsed=%s bounded=%s unbounded=%s verdict=%s command_exit=%s\n' \
                  "${{result}}" "${{elapsed}}" "${{BOUNDED_CALLS}}" "${{UNBOUNDED_CALLS}}" \
                  "${{SOAK_STATUS_VERDICT}}" "${{SOAK_COMMAND_EXIT}}"
                """
            )
            result = subprocess.run(
                ["bash", "-c", script],
                cwd=ROOT,
                text=True,
                capture_output=True,
                check=False,
                timeout=8,
            )

        self.assertEqual(0, result.returncode, result.stderr)
        fields = dict(item.split("=", 1) for item in result.stdout.strip().split())
        self.assertEqual("1", fields["result"])
        self.assertLessEqual(int(fields["elapsed"]), 4)
        self.assertEqual("1", fields["bounded"])
        self.assertEqual("0", fields["unbounded"])
        self.assertEqual("command-timeout", fields["verdict"])
        self.assertIn(fields["command_exit"], {"124", "137"})
        self.assertIn("bounded invocation", result.stderr)



if __name__ == "__main__":
    unittest.main()
