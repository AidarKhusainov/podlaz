package api

import "testing"

func TestValidateNetworkSessionRecoveryStateAcceptsBlockedResume(t *testing.T) {
	state := NetworkSessionRecoveryState{
		Authority:           NetworkSessionRecoveryAuthorityPresent,
		Intent:              "resume",
		StartupGate:         NetworkSessionStartupGateBlocked,
		ResumeStage:         NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:   NetworkSessionResumeOutcomeFailed,
		LastTUNFailurePhase: "preflight",
		RollbackStatus:      "not-started",
		CleanupAuthority:    NetworkSessionCleanupAuthorityNone,
		NextAction:          NetworkSessionRecoveryActionRetryResume,
	}
	if err := ValidateNetworkSessionRecoveryState(state); err != nil {
		t.Fatalf("validate blocked resume state: %v", err)
	}
}

func TestValidateNetworkSessionRecoveryStateRejectsTerminalRetryResume(t *testing.T) {
	state := NetworkSessionRecoveryState{
		Authority:         NetworkSessionRecoveryAuthorityPresent,
		Intent:            "terminal",
		StartupGate:       NetworkSessionStartupGateBlocked,
		LastResumeOutcome: NetworkSessionResumeOutcomeNotAttempted,
		CleanupAuthority:  NetworkSessionCleanupAuthoritySessionProtection,
		NextAction:        NetworkSessionRecoveryActionRetryResume,
	}
	if err := ValidateNetworkSessionRecoveryState(state); err == nil {
		t.Fatal("terminal intent must never validate as retry-resume")
	}
}

func TestValidateRecoveryResponseValidatesNetworkSessionState(t *testing.T) {
	response := RecoveryResponse{
		Mode: "execute",
		NetworkSession: &NetworkSessionRecoveryState{
			Authority:         NetworkSessionRecoveryAuthorityPresent,
			Intent:            "resume",
			StartupGate:       NetworkSessionStartupGateOpen,
			LastResumeOutcome: NetworkSessionResumeOutcomeSucceeded,
			CleanupAuthority:  NetworkSessionCleanupAuthorityNone,
			NextAction:        NetworkSessionRecoveryActionNone,
		},
	}
	if err := ValidateRecoveryResponse(response); err != nil {
		t.Fatalf("validate recovery response: %v", err)
	}
}
