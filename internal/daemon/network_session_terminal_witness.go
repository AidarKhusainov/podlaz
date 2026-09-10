package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

type networkSessionTerminalObservationStage func(context.Context, string) error

func deriveNetworkSessionTerminalCleanupWitness(
	ctx context.Context,
	state networkSessionState,
	attempt networkSessionReplayAttempt,
	exactRecovery api.RecoveryResponse,
	observe networkSessionTerminalObservationStage,
	runtimeDir string,
) (networkSessionTerminalCleanupWitness, error) {
	if state.Intent != networkSessionIntentResume || state.Protection == nil {
		return networkSessionTerminalCleanupWitness{}, errors.New("terminal replay cleanup witness requires protected resume authority")
	}
	if state.SessionID != attempt.SessionID || state.RecoveryEpoch != attempt.RecoveryEpoch {
		return networkSessionTerminalCleanupWitness{}, errors.New("terminal replay evidence is stale")
	}
	if !networkSessionTerminalAttemptCleanupSafe(attempt) {
		return networkSessionTerminalCleanupWitness{}, errors.New("terminal replay candidate cleanup is not proven")
	}
	if !networkSessionRecoveryConverged(exactRecovery) {
		return networkSessionTerminalCleanupWitness{}, errNetworkSessionRecoveryIncomplete
	}
	if observe == nil {
		observe = observeProductionNetworkSessionTerminalDataPlane
	}
	if err := observe(ctx, runtimeDir); err != nil {
		return networkSessionTerminalCleanupWitness{}, fmt.Errorf("observe terminal Network Session data plane: %w", err)
	}
	return networkSessionTerminalCleanupWitness{
		sessionID:     state.SessionID,
		recoveryEpoch: state.RecoveryEpoch,
		proven:        true,
	}, nil
}

func observeProductionNetworkSessionTerminalDataPlane(ctx context.Context, runtimeDir string) error {
	plan := recovery.PlanWithOptions(ctx, recovery.Options{RuntimeDir: runtimeDir})
	if len(plan.Warnings) != 0 || len(plan.Candidates) != 0 {
		return fmt.Errorf("terminal data-plane observation is inconclusive: candidates=%d warnings=%d", len(plan.Candidates), len(plan.Warnings))
	}
	return nil
}
