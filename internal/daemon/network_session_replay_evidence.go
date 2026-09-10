package daemon

import (
	"context"
	"errors"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
)

func (s networkSessionResumeDiagnosticStore) SaveReplayFailure(record networkSessionResumeDiagnostic, attempt networkSessionReplayAttempt) error {
	current, exists, err := s.Load()
	if err != nil {
		return err
	}
	record.ReplayDisposition = string(attempt.ReplayDisposition)
	record.NetworkApplySubphase = attempt.NetworkApplySubphase
	record.Current = cloneNetworkSessionReplayAttempt(&attempt)
	if exists && current.Originating != nil {
		record.Originating = cloneNetworkSessionReplayAttempt(current.Originating)
	} else {
		record.Originating = cloneNetworkSessionReplayAttempt(&attempt)
	}
	return s.Save(record)
}

func persistNetworkSessionReplayFailure(
	ctx context.Context,
	continuation networkSessionContinuationStore,
	attemptState networkSessionState,
	legacyMigration bool,
	err error,
) error {
	if err == nil {
		return nil
	}
	wrapped := newNetworkSessionResumeOutcomeError(
		api.NetworkSessionResumeStageConnectReplay,
		api.NetworkSessionResumeOutcomeFailed,
		legacyMigration,
		false,
		err,
	)
	record, ok := networkSessionResumeFailure(wrapped)
	if !ok {
		return wrapped
	}
	disposition, mutation := classifyNetworkSessionReplayFailure(ctx, err)
	record.RecoveryEpoch = attemptState.RecoveryEpoch
	record.ReplayDisposition = string(disposition)
	if record.TUNFailurePhase == "network-apply" {
		record.NetworkApplySubphase = netexecutor.ApplyFailureSubphase(err)
	}
	attempt := networkSessionReplayAttempt{
		SessionID:            attemptState.SessionID,
		RecoveryEpoch:        attemptState.RecoveryEpoch,
		ReplayDisposition:    disposition,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:      record.TUNFailurePhase,
		NetworkApplySubphase: record.NetworkApplySubphase,
		RollbackStatus:       record.RollbackStatus,
		TransactionPresent:   record.TransactionPresent,
		LegacyMigration:      legacyMigration,
		CandidateMutation:    mutation,
	}
	store := newNetworkSessionResumeDiagnosticStore(continuation.runtimeDir, continuation.readBootID)
	if persistErr := store.SaveReplayFailure(record, attempt); persistErr != nil {
		return errors.Join(wrapped, persistErr)
	}
	return wrapped
}

var errNetworkSessionReplayIncomplete = errors.New("network session replay outcome remains incomplete")

func currentNetworkSessionReplayAttempt(
	continuation networkSessionContinuationStore,
	state networkSessionState,
) (networkSessionReplayAttempt, bool, error) {
	record, exists, err := newNetworkSessionResumeDiagnosticStore(continuation.runtimeDir, continuation.readBootID).Load()
	if err != nil {
		return networkSessionReplayAttempt{}, false, err
	}
	if !exists || record.Current == nil {
		return networkSessionReplayAttempt{}, false, nil
	}
	current := *record.Current
	if current.SessionID != state.SessionID || current.RecoveryEpoch != state.RecoveryEpoch {
		return networkSessionReplayAttempt{}, false, nil
	}
	return current, true, nil
}

func networkSessionReplayReadmissionBlocker(attempt networkSessionReplayAttempt) error {
	if attempt.ReplayDisposition != networkSessionReplayDispositionIncomplete {
		return nil
	}
	phased := withTunFailurePhase(attempt.TUNFailurePhase, "", attempt.RollbackStatus, errNetworkSessionReplayIncomplete)
	return newNetworkSessionResumeOutcomeError(
		api.NetworkSessionResumeStageConnectReplay,
		api.NetworkSessionResumeOutcomeFailed,
		attempt.LegacyMigration,
		attempt.TransactionPresent,
		phased,
	)
}
