package daemon

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestStartupScanStatusDoctorAndRecoveryRenderResolvedCandidateConsistently(t *testing.T) {
	candidate := recovery.Candidate{Kind: "dns-link", Description: "systemd-resolved link state", Target: "podlaz0"}
	scan := recovery.PlanResult{Candidates: []recovery.Candidate{candidate}}

	status := startupScanToAPI(scan)
	if status == nil || status.Status != api.StartupScanStatusStale {
		t.Fatalf("expected stale startup scan status, got %#v", status)
	}
	if len(status.Candidates) != 1 || status.Candidates[0].Kind != candidate.Kind || status.Candidates[0].Target != candidate.Target {
		t.Fatalf("startup status candidate diverged from recovery candidate: %#v", status.Candidates)
	}

	recoveryResponse := recoveryResponseToAPI(recovery.ExecuteResult{Results: []recovery.CleanupResult{{Candidate: candidate, Status: "skipped", Message: "systemd-resolved link record persisted after revert; restart systemd-resolved manually"}}})
	if len(recoveryResponse.Results) != 1 || recoveryResponse.Results[0].Candidate.Kind != status.Candidates[0].Kind || recoveryResponse.Results[0].Candidate.Target != status.Candidates[0].Target {
		t.Fatalf("recovery response candidate diverged from startup status: status=%#v recovery=%#v", status.Candidates, recoveryResponse.Results)
	}

	doctorResponse := withStartupScanDoctor(api.DoctorResponse{}, scan)
	check := findDoctorCheck(doctorResponse.Checks, "startup-recovery-scan")
	if check == nil || check.Severity != "WARN" || !strings.Contains(check.Message, "recovery candidates: 1") || !strings.Contains(check.Message, "suggested action: podlaz recover") {
		t.Fatalf("doctor startup scan check diverged from stale recovery state: %#v", check)
	}
}
