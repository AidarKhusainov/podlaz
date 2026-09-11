from __future__ import annotations

import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "tun-package-restart-recovery.sh"


class TunPackageRestartFailureCauseContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.text = SCRIPT.read_text(encoding="utf-8")

    def function_body(self, name: str, next_marker: str) -> str:
        start = self.text.index(f"{name}() {{")
        end = self.text.index(next_marker, start)
        return self.text[start:end]

    def test_blocked_candidate_captures_only_bounded_private_replay_cause_before_failing(self) -> None:
        self.assertIn("network-session-resume.json", self.text)
        body = self.function_body(
            "capture_blocked_replay_evidence",
            "\n}\n\nclassify_package_restart_candidate",
        )
        for required in (
            "RESUME_DIAGNOSTIC",
            "replay_disposition",
            "network_apply_subphase",
            "network_apply_failure_cause",
            "candidate_mutation",
            "rollback_status",
            "command-exit",
            "command-timeout",
            "command-unavailable",
            "unknown",
            "blocked_replay_failure_cause",
        ):
            self.assertIn(required, body)
        for forbidden in ("raw_stderr", "stderr", "transaction_id", "session_id"):
            self.assertNotIn(forbidden, body)

        classify = self.function_body(
            "classify_package_restart_candidate",
            "\n}\n\nassert_clean_recovery_view",
        )
        capture = classify.index("capture_blocked_replay_evidence")
        failure = classify.index("remained blocked instead of converging")
        self.assertLess(capture, failure)

    def test_terminal_evidence_records_bounded_apply_cause_without_using_it_for_terminality(self) -> None:
        body = self.function_body(
            "capture_typed_terminal_replay_evidence",
            "\n}\n\nwait_for_candidate_start_boundary",
        )
        self.assertIn("network_apply_failure_cause", body)
        self.assertIn("typed_terminal_network_apply_failure_cause", body)
        self.assertIn("replay_disposition", body)
        self.assertIn("terminal", body)


if __name__ == "__main__":
    unittest.main()
