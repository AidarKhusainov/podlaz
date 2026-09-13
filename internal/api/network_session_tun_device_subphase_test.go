package api

import "testing"

func TestValidateNetworkSessionRecoveryStateAcceptsTunDeviceApplySubphase(t *testing.T) {
	state := NetworkSessionRecoveryState{
		Authority:            NetworkSessionRecoveryAuthorityPresent,
		Intent:               "resume",
		StartupGate:          NetworkSessionStartupGateBlocked,
		ResumeStage:          NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:    NetworkSessionResumeOutcomeFailed,
		LastTUNFailurePhase:  "network-apply",
		ReplayDisposition:    NetworkSessionReplayDispositionIncomplete,
		NetworkApplySubphase: "tun-device",
		RollbackStatus:       "completed",
		CleanupAuthority:     NetworkSessionCleanupAuthoritySessionProtection,
		NextAction:           NetworkSessionRecoveryActionManualDiagnosis,
	}
	if err := ValidateNetworkSessionRecoveryState(state); err != nil {
		t.Fatalf("validate tun-device replay subphase: %v", err)
	}
}
