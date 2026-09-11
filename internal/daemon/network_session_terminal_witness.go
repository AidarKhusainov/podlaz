package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	transactionID := strings.TrimSpace(attempt.TransactionID)
	if attempt.CandidateMutation == networkSessionCandidateMutationRolledBack && transactionID == "" {
		return networkSessionTerminalCleanupWitness{}, errors.New("rolled-back terminal replay requires exact retained transaction evidence")
	}
	if !networkSessionRecoveryConverged(exactRecovery) {
		return networkSessionTerminalCleanupWitness{}, errNetworkSessionRecoveryIncomplete
	}
	if observe == nil {
		if err := observeProductionNetworkSessionTerminalDataPlane(ctx, runtimeDir, transactionID); err != nil {
			return networkSessionTerminalCleanupWitness{}, fmt.Errorf("observe terminal Network Session data plane: %w", err)
		}
	} else if err := observe(ctx, runtimeDir); err != nil {
		return networkSessionTerminalCleanupWitness{}, fmt.Errorf("observe terminal Network Session data plane: %w", err)
	}
	return networkSessionTerminalCleanupWitness{
		sessionID:     state.SessionID,
		recoveryEpoch: state.RecoveryEpoch,
		proven:        true,
	}, nil
}

func terminalizeNetworkSessionReplay(
	ctx context.Context,
	continuation networkSessionContinuationStore,
	stateStore networkSessionStateStore,
	attempt networkSessionReplayAttempt,
	exactRecovery api.RecoveryResponse,
	observe networkSessionTerminalObservationStage,
	continueTeardown networkSessionTeardownRecoveryStage,
) (bool, error) {
	state, exists, err := stateStore.Load()
	if err != nil {
		return false, fmt.Errorf("reload Network Session before terminal replay transition: %w", err)
	}
	if !exists {
		return false, nil
	}
	witness, err := deriveNetworkSessionTerminalCleanupWitness(ctx, state, attempt, exactRecovery, observe, continuation.runtimeDir)
	if err != nil {
		return false, err
	}
	outcome, err := stateStore.TransitionReplayToTerminal(attempt, witness)
	if err != nil {
		return false, fmt.Errorf("persist terminal Network Session replay transition: %w", err)
	}
	if outcome != networkSessionTerminalTransitionCommitted {
		return false, nil
	}
	if err := continueTeardown(ctx, stateStore); err != nil {
		return false, fmt.Errorf("continue terminalized Network Session teardown: %w", err)
	}
	_ = newNetworkSessionResumeDiagnosticStore(continuation.runtimeDir, continuation.readBootID).Remove()
	return true, nil
}

func observeProductionNetworkSessionTerminalDataPlane(ctx context.Context, runtimeDir, transactionID string) error {
	if strings.TrimSpace(transactionID) != "" {
		if err := recovery.VerifyRolledBackTransactionAbsence(ctx, runtimeDir, transactionID); err != nil {
			return fmt.Errorf("exact retained transaction absence proof failed: %w", err)
		}
	}
	// Generic recovery observation remains an additional guard for standalone or
	// legacy Podlaz resources. It is never sufficient in place of exact retained
	// transaction evidence for a replay that actually mutated and rolled back.
	plan := recovery.PlanWithOptions(ctx, recovery.Options{RuntimeDir: runtimeDir})
	if len(plan.Warnings) != 0 || len(plan.Candidates) != 0 {
		return fmt.Errorf("terminal data-plane observation is inconclusive: candidates=%d warnings=%d", len(plan.Candidates), len(plan.Warnings))
	}
	return nil
}
