package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestTerminalCleanupWitnessRejectsRolledBackAttemptWithoutExactTransactionIdentity(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	state, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load protected session: exists=%v err=%v", exists, err)
	}
	attempt := networkSessionReplayAttempt{
		SessionID:          state.SessionID,
		RecoveryEpoch:      state.RecoveryEpoch,
		ReplayDisposition:  networkSessionReplayDispositionTerminal,
		ResumeStage:        api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:    "network-apply",
		RollbackStatus:     "completed",
		TransactionPresent: true,
		CandidateMutation:  networkSessionCandidateMutationRolledBack,
	}

	_, err = deriveNetworkSessionTerminalCleanupWitness(
		context.Background(),
		state,
		attempt,
		api.RecoveryResponse{Mode: "execute"},
		func(context.Context, string) error { return nil },
		store.runtimeDir,
	)
	if err == nil {
		t.Fatal("rolled-back terminal witness without exact retained transaction identity must be rejected")
	}
}
