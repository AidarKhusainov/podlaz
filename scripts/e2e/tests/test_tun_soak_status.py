from __future__ import annotations

import os
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from io import StringIO
from pathlib import Path

from scripts.e2e.lib import tun_soak_status


class TunSoakStatusTests(unittest.TestCase):
    shell_library = Path(__file__).resolve().parents[1] / "lib" / "tun_soak_health.sh"
    status_tool = Path(__file__).resolve().parents[1] / "lib" / "tun_soak_status.py"
    metrics_tool = Path(__file__).resolve().parents[1] / "lib" / "tun_soak_metrics.py"

    def status_output(
        self,
        *,
        connection: str,
        state: str | None,
        generation: int | None = None,
        classification: str | None = None,
        tun_base: str = "enabled (podlaz0)",
    ) -> str:
        del generation, classification
        mode = "tun"
        if tun_base != "enabled (podlaz0)":
            mode = "proxy-only"
        if connection == "inactive":
            product_state = "Disconnected"
            mode = ""
        elif connection.startswith("active (revalidating:") or connection.startswith("active (degraded:"):
            product_state = "Reconnecting"
        elif connection.startswith("active (cleanup-required:"):
            product_state = "Unknown"
        elif connection == "active" and state == "verified":
            product_state = "Connected"
        elif connection == "active" and state is None:
            product_state = "Connecting"
        else:
            product_state = "Unknown"
        lines = [f"Status: {product_state}", "Profile: Example VPN"]
        if mode:
            lines.append(f"Mode: {mode}")
        lines.append("Autostart: Disabled")
        return "\n".join(lines) + "\n"

    def test_accepts_exact_verified_active_status(self) -> None:
        output = self.status_output(connection="active", state="verified", generation=1)
        self.assertEqual("verified", tun_soak_status.classify_status(output, exit_code=0))

    def test_retries_active_status_while_tun_health_is_initializing(self) -> None:
        output = self.status_output(connection="active", state=None)
        self.assertEqual("retry-initializing", tun_soak_status.classify_status(output, exit_code=0))

    def test_active_without_health_stays_fail_closed_for_non_enabled_tun(self) -> None:
        output = self.status_output(connection="active", state=None, tun_base="disabled")
        self.assertEqual("invalid-status", tun_soak_status.classify_status(output, exit_code=0))

    def test_retries_exact_revalidating_status(self) -> None:
        output = self.status_output(
            connection="active (revalidating: uplink_revalidating)",
            state="revalidating",
            generation=1,
            classification="uplink_revalidating",
        )
        self.assertEqual("retry-revalidating", tun_soak_status.classify_status(output, exit_code=3))

    def test_retries_transient_degraded_status(self) -> None:
        output = self.status_output(
            connection="active (degraded: connectivity_failed)",
            state="degraded",
            generation=2,
            classification="connectivity_failed",
        )
        self.assertEqual("retry-revalidating", tun_soak_status.classify_status(output, exit_code=3))

    def test_rejects_cleanup_required_as_terminal(self) -> None:
        output = self.status_output(
            connection="active (cleanup-required: owned_state_invalid)",
            state="cleanup-required",
            generation=2,
            classification="owned_state_invalid",
        )
        self.assertEqual("invalid-status", tun_soak_status.classify_status(output, exit_code=3))

    def test_rejects_clean_inactive_status_after_connect(self) -> None:
        output = self.status_output(connection="inactive", state=None, tun_base="disabled")
        self.assertEqual("terminal-inactive", tun_soak_status.classify_status(output, exit_code=0))

    def test_fails_closed_on_exit_state_mismatch(self) -> None:
        revalidating = self.status_output(
            connection="active (revalidating: uplink_revalidating)",
            state="revalidating",
            generation=1,
            classification="uplink_revalidating",
        )
        verified = self.status_output(connection="active", state="verified", generation=1)
        self.assertEqual("retry-revalidating", tun_soak_status.classify_status(revalidating, exit_code=0))
        self.assertEqual("invalid-status", tun_soak_status.classify_status(verified, exit_code=3))

    def test_fails_closed_on_duplicate_or_wrong_mode_structural_fields(self) -> None:
        verified = self.status_output(connection="active", state="verified", generation=1)
        duplicate = verified + "Status: Connected\n"
        wrong_mode = self.status_output(
            connection="active",
            state="verified",
            generation=1,
            tun_base="disabled",
        )
        self.assertEqual("invalid-status", tun_soak_status.classify_status(duplicate, exit_code=0))
        self.assertEqual("invalid-status", tun_soak_status.classify_status(wrong_mode, exit_code=0))

    def test_fails_closed_on_unknown_health_or_unbounded_output(self) -> None:
        unknown = self.status_output(connection="active", state="future-state", generation=1)
        oversized = "x" * (tun_soak_status.MAX_STATUS_BYTES + 1)
        self.assertEqual("invalid-status", tun_soak_status.classify_status(unknown, exit_code=3))
        self.assertEqual("invalid-status", tun_soak_status.classify_status(oversized, exit_code=3))

    def test_cli_emits_only_one_allowlisted_verdict(self) -> None:
        output = self.status_output(
            connection="active (revalidating: uplink_revalidating)",
            state="revalidating",
            generation=1,
            classification="uplink_revalidating",
        )
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "private-status.stdout"
            path.write_text(output, encoding="utf-8")
            stdout = StringIO()
            with redirect_stdout(stdout):
                exit_code = tun_soak_status.main(
                    ["classify", "--stdout-file", str(path), "--exit-code", "3"]
                )
        self.assertEqual(0, exit_code)
        self.assertEqual("retry-revalidating\n", stdout.getvalue())
        self.assertNotIn("uplink", stdout.getvalue())
        self.assertNotIn("podlaz0", stdout.getvalue())


    def run_shell_wait(
        self,
        fixtures: list[tuple[str, int, str]],
        *,
        timeout: int = 3,
    ) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for index, (stdout, exit_code, stderr) in enumerate(fixtures, start=1):
                (root / f"{index}.stdout").write_text(stdout, encoding="utf-8")
                (root / f"{index}.stderr").write_text(stderr, encoding="utf-8")
                (root / f"{index}.exit").write_text(str(exit_code), encoding="ascii")
            script = """
set -u -o pipefail
FAIL_MESSAGE=""
fail() {
  FAIL_MESSAGE="$1"
  return 1
}
CALL_INDEX=0
run_installed_podlaz_bounded() {
  local invocation_timeout="$1"
  shift
  [[ "${invocation_timeout}" =~ ^[1-9][0-9]*$ && "$1" == "status" ]] || return 90
  CALL_INDEX=$((CALL_INDEX + 1))
  local selected="${CALL_INDEX}"
  if [[ ! -f "${FIXTURE_DIR}/${selected}.exit" ]]; then
    selected="${FIXTURE_COUNT}"
  fi
  cat "${FIXTURE_DIR}/${selected}.stdout"
  cat "${FIXTURE_DIR}/${selected}.stderr" >&2
  return "$(cat "${FIXTURE_DIR}/${selected}.exit")"
}
source "${HEALTH_LIBRARY}"
SOAK_PRIVATE_DIR="${FIXTURE_DIR}/private"
mkdir -p "${SOAK_PRIVATE_DIR}"
SOAK_STATUS_VERDICT=""
SOAK_COMMAND_EXIT=""
SOAK_COMMAND_CLASSIFICATION=""
PODLAZ_E2E_TUN_HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT}"
PODLAZ_E2E_TUN_STATUS_TIMEOUT_SECONDS=2
PODLAZ_E2E_TUN_HEALTH_POLL_SECONDS=1
TUN_SOAK_STATUS_TOOL="${STATUS_TOOL}"
METRICS_TOOL="${METRICS_TOOL_PATH}"
if wait_for_verified_tun_status test; then
  result=0
else
  result=$?
fi
printf 'result=%s\\n' "${result}"
printf 'calls=%s\\n' "${CALL_INDEX}"
printf 'verdict=%s\\n' "${SOAK_STATUS_VERDICT}"
printf 'command_exit=%s\\n' "${SOAK_COMMAND_EXIT}"
printf 'command_classification=%s\\n' "${SOAK_COMMAND_CLASSIFICATION}"
printf 'failure=%s\\n' "${FAIL_MESSAGE}"
"""
            env = os.environ.copy()
            env.update(
                {
                    "FIXTURE_DIR": str(root),
                    "FIXTURE_COUNT": str(len(fixtures)),
                    "HEALTH_LIBRARY": str(self.shell_library),
                    "HEALTH_TIMEOUT": str(timeout),
                    "STATUS_TOOL": str(self.status_tool),
                    "METRICS_TOOL_PATH": str(self.metrics_tool),
                }
            )
            return subprocess.run(
                ["bash", "-c", script],
                check=False,
                capture_output=True,
                text=True,
                env=env,
            )

    def test_shell_wait_retries_initializing_then_accepts_verified(self) -> None:
        initializing = self.status_output(connection="active", state=None)
        verified = self.status_output(connection="active", state="verified", generation=1)
        result = self.run_shell_wait([(initializing, 0, ""), (verified, 0, "")])
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("result=0", result.stdout)
        self.assertIn("calls=2", result.stdout)
        self.assertIn("verdict=", result.stdout)
        self.assertNotIn("podlaz0", result.stdout)

    def test_shell_wait_retries_revalidating_then_accepts_verified(self) -> None:
        revalidating = self.status_output(
            connection="active (revalidating: uplink_revalidating)",
            state="revalidating",
            generation=1,
            classification="uplink_revalidating",
        )
        verified = self.status_output(connection="active", state="verified", generation=1)
        result = self.run_shell_wait([(revalidating, 3, ""), (verified, 0, "")])
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("result=0", result.stdout)
        self.assertIn("calls=2", result.stdout)
        self.assertIn("verdict=", result.stdout)
        self.assertNotIn("uplink_revalidating", result.stdout)

    def test_shell_wait_fails_closed_on_cleanup_required(self) -> None:
        cleanup_required = self.status_output(
            connection="active (cleanup-required: owned_state_invalid)",
            state="cleanup-required",
            generation=2,
            classification="owned_state_invalid",
        )
        result = self.run_shell_wait([(cleanup_required, 3, "")])
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("result=1", result.stdout)
        self.assertIn("calls=1", result.stdout)
        self.assertIn("verdict=invalid-status", result.stdout)
        self.assertNotIn("owned_state_invalid", result.stdout)

    def test_shell_wait_classifies_status_command_failure_without_raw_text(self) -> None:
        result = self.run_shell_wait(
            [("", 5, "podlaz: daemon is unavailable at private-host.example\n")]
        )
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("result=1", result.stdout)
        self.assertIn("verdict=command-error", result.stdout)
        self.assertIn("command_exit=5", result.stdout)
        self.assertIn("command_classification=daemon-unavailable", result.stdout)
        self.assertNotIn("private-host", result.stdout)
        self.assertNotIn("private-host", result.stderr)


if __name__ == "__main__":
    unittest.main()
