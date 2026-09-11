package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestReplayAttemptRejectsApplySubphaseOutsideNetworkApply(t *testing.T) {
	attempt := networkSessionReplayAttempt{
		SessionID:            "0123456789abcdef0123456789abcdef",
		RecoveryEpoch:        1,
		ReplayDisposition:    networkSessionReplayDispositionIncomplete,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:      "preflight",
		NetworkApplySubphase: api.NetworkSessionApplySubphaseDNS,
		RollbackStatus:       "not-started",
		CandidateMutation:    networkSessionCandidateMutationNotOpened,
	}
	if err := validateNetworkSessionReplayAttempt(attempt); err == nil {
		t.Fatal("replay apply subphase must require network-apply failure phase")
	}
}

func TestResumeDiagnosticRejectsApplySubphaseOutsideNetworkApply(t *testing.T) {
	record := networkSessionResumeDiagnostic{
		SchemaVersion:        networkSessionResumeDiagnosticSchemaVersion,
		Owner:                networkSessionResumeDiagnosticOwner,
		BootID:               "boot-a",
		RecoveryEpoch:        1,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:    api.NetworkSessionResumeOutcomeFailed,
		TUNFailurePhase:      "preflight",
		ReplayDisposition:    api.NetworkSessionReplayDispositionIncomplete,
		NetworkApplySubphase: api.NetworkSessionApplySubphaseDNS,
		RollbackStatus:       "not-started",
	}
	if err := validateNetworkSessionResumeDiagnostic(record); err == nil {
		t.Fatal("top-level replay apply subphase must require network-apply failure phase")
	}
}
