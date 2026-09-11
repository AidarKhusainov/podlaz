package cli

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestRecoverDryRunShowsRetainedNetworkSessionAuthority(t *testing.T) {
	state := &api.NetworkSessionRecoveryState{
		Authority:            api.NetworkSessionRecoveryAuthorityPresent,
		Intent:               "resume",
		StartupGate:          api.NetworkSessionStartupGateBlocked,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:    api.NetworkSessionResumeOutcomeFailed,
		LastTUNFailurePhase:  "network-apply",
		ReplayDisposition:    "terminal",
		NetworkApplySubphase: "dns",
		RollbackStatus:       "completed",
		CleanupAuthority:     api.NetworkSessionCleanupAuthorityNone,
		NextAction:           api.NetworkSessionRecoveryActionContinueTeardown,
	}
	got := (recoverPlanView{NetworkSession: state}).String()
	if strings.Contains(got, "No podlaz-owned recovery candidates found") {
		t.Fatalf("retained Network Session was reported as no recovery work: %q", got)
	}
	for _, want := range []string{
		"Network Session authority: present",
		"Intent: resume",
		"Startup gate: blocked",
		"Resume stage: connect-replay",
		"Last resume outcome: failed",
		"TUN failure phase: network-apply",
		"Replay disposition: terminal",
		"Network apply subphase: dns",
		"Cleanup authority: none",
		"Next action: continue-teardown",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("recover dry-run missing %q: %q", want, got)
		}
	}
}

func TestRecoverExecuteFailedResumeUsesSameSemanticModelAndFails(t *testing.T) {
	state := &api.NetworkSessionRecoveryState{
		Authority:           api.NetworkSessionRecoveryAuthorityPresent,
		Intent:              "resume",
		StartupGate:         api.NetworkSessionStartupGateBlocked,
		ResumeStage:         api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:   api.NetworkSessionResumeOutcomeFailed,
		LastTUNFailurePhase: "preflight",
		ReplayDisposition:   "retryable",
		RollbackStatus:      "not-started",
		CleanupAuthority:    api.NetworkSessionCleanupAuthorityNone,
		NextAction:          api.NetworkSessionRecoveryActionRetryResume,
	}
	result := recoverExecuteView{NetworkSession: state}
	if !result.HasIncompleteCleanup() {
		t.Fatal("failed retained resume must produce the non-zero incomplete recovery outcome")
	}
	got := result.String()
	for _, want := range []string{"Network Session authority: present", "Replay disposition: retryable", "Next action: retry-resume"} {
		if !strings.Contains(got, want) {
			t.Fatalf("recover execute missing %q: %q", want, got)
		}
	}
}

func TestRecoverExecuteSuccessfulResumeReturnsSuccess(t *testing.T) {
	state := &api.NetworkSessionRecoveryState{
		Authority:         api.NetworkSessionRecoveryAuthorityPresent,
		Intent:            "resume",
		StartupGate:       api.NetworkSessionStartupGateOpen,
		LastResumeOutcome: api.NetworkSessionResumeOutcomeSucceeded,
		CleanupAuthority:  api.NetworkSessionCleanupAuthorityNone,
		NextAction:        api.NetworkSessionRecoveryActionNone,
	}
	result := recoverExecuteView{NetworkSession: state}
	if result.HasFailures() || result.HasIncompleteCleanup() {
		t.Fatalf("successful resumed Network Session must be a successful recover result: %#v", result)
	}
}
