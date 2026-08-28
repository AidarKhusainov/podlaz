from __future__ import annotations

from dataclasses import asdict
from typing import Iterable

from .artifacts import ArtifactStore
from .model import Checkpoint, Qualification, ScenarioOutcome


MANDATORY_SCENARIOS = {
    "lower_release_upgrade_continuity",
    "candidate_protected_data_plane",
    "graceful_daemon_restart",
    "unexpected_daemon_death",
    "durable_rollback_interruption",
    "explicit_stop_start_no_reconnect",
    "candidate_reinstall_continuity",
    "privacy_candidate_windows",
    "preconnect_collision",
    "resource_soak",
    "disconnect_cleanup",
    "runtime_terminal_convergence",
    "reboot_autostart_off",
    "reboot_autostart_on",
    "explicit_disconnect_no_same_boot_retry",
    "reboot_terminal_autostart",
    "terminal_no_same_boot_retry",
    "final_restoration",
}


class QualificationEvaluator:
    def evaluate(self, checkpoint: Checkpoint) -> Qualification:
        outcomes = {name: record.outcome for name, record in checkpoint.scenarios.items()}
        if any(outcome in {ScenarioOutcome.FAIL, ScenarioOutcome.INCONCLUSIVE} for outcome in outcomes.values()):
            return Qualification.FAIL
        if checkpoint.private.get("cleanup_failed"):
            return Qualification.FAIL
        if checkpoint.private.get("soak_minutes", 0) < 60:
            return Qualification.PARTIAL_PASS
        if checkpoint.private.get("user_forced_partial"):
            return Qualification.PARTIAL_PASS
        if not MANDATORY_SCENARIOS.issubset(outcomes):
            return Qualification.PARTIAL_PASS
        for name in MANDATORY_SCENARIOS:
            outcome = outcomes[name]
            if outcome == ScenarioOutcome.SKIP_USER_REQUEST:
                return Qualification.PARTIAL_PASS
            if outcome not in {
                ScenarioOutcome.PASS,
                ScenarioOutcome.SKIP_HOST_CAPABILITY,
                ScenarioOutcome.SKIP_RELEASE_CAPABILITY,
                ScenarioOutcome.SKIP_REMOTE_SESSION,
            }:
                return Qualification.PARTIAL_PASS
        return Qualification.QUALIFIED_PASS


class ReportWriter:
    def __init__(self, artifacts: ArtifactStore):
        self.artifacts = artifacts

    def write(self, checkpoint: Checkpoint, qualification: Qualification) -> None:
        scenarios = {
            name: {
                "outcome": record.outcome.value,
                "reason": record.reason,
                "evidence": record.evidence,
            }
            for name, record in sorted(checkpoint.scenarios.items())
        }
        report = {
            "schema_version": "podlaz.release-acceptance-report.v1",
            "qualification": qualification.value,
            "phase": checkpoint.phase,
            "scenarios": scenarios,
            "soak_minutes": checkpoint.private.get("soak_minutes"),
            "soak_measured_seconds": checkpoint.private.get("soak_measured_seconds"),
            "resource_summary": checkpoint.private.get("resource_summary", {}),
            "cleanup": "failed" if checkpoint.private.get("cleanup_failed") else "verified",
        }
        self.artifacts.write_public_json("report.json", report)
        observations = {
            "schema_version": "podlaz.release-requirements-observation.v1",
            "scope": "single-maintainer-laptop-observation",
            "qualification": qualification.value,
            "resource_summary": checkpoint.private.get("resource_summary", {}),
            "soak_measured_seconds": checkpoint.private.get("soak_measured_seconds"),
        }
        self.artifacts.write_public_json("requirements-observation.json", observations)
        lines = [f"Result: {qualification.value}", "", "Scenarios:"]
        for name, value in scenarios.items():
            reason = f" ({value['reason']})" if value["reason"] else ""
            lines.append(f"- {name}: {value['outcome']}{reason}")
        lines.append("")
        lines.append("Candidate remains installed. Public artifacts contain structural evidence only.")
        self.artifacts.write_public_text("summary.txt", "\n".join(lines) + "\n")
