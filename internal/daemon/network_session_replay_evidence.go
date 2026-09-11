package daemon

import (
	"context"
	"errors"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
)

type networkSessionReplayProjectionError struct {
	disposition networkSessionReplayDisposition
	subphase    string
	err         error
}

func (e networkSessionReplayProjectionError) Error() string {
	if e.err == nil {
		return "network session replay failed"
	}
	return e.err.Error()
}

func (e networkSessionReplayProjectionError) Unwrap() error { return e.err }

func withNetworkSessionReplayProjection(disposition networkSessionReplayDisposition, subphase string, err error) error {
	if err == nil {
		return nil
	}
	return networkSessionReplayProjectionError{
		disposition: disposition,
		subphase:    subphase,
		err:         err,
	}
}

func networkSessionReplayProjection(err error) (networkSessionReplayDisposition, string, bool) {
	var projected networkSessionReplayProjectionError
	if !errors.As(err, &projected) || !validNetworkSessionReplayDisposition(projected.disposition) {
		return "", "", false
	}
	if projected.subphase != "" && !validNetworkSessionApplySubphase(projected.subphase) {
		return "", "", false
	}
	return projected.disposition, projected.subphase, true
}

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
	_, transactionID, _ := tunFailureLogFields(err)
	if transactionID == noTunTransactionID {
		transactionID = ""
	}
	record.RecoveryEpoch = attemptState.RecoveryEpoch
	record.ReplayDisposition = string(disposition)
	applyFailureCause := ""
	if record.TUNFailurePhase == "network-apply" {
		record.NetworkApplySubphase = netexecutor.ApplyFailureSubphase(err)
		if cause := netexecutor.ApplyFailureCause(err); cause != netexecutor.ApplyFailureCauseUnknown {
			applyFailureCause = cause
		}
	}
	attempt := networkSessionReplayAttempt{
		SessionID:                attemptState.SessionID,
		RecoveryEpoch:            attemptState.RecoveryEpoch,
		ReplayDisposition:        disposition,
		ResumeStage:              api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:          record.TUNFailurePhase,
		NetworkApplySubphase:     record.NetworkApplySubphase,
		NetworkApplyFailureCause: applyFailureCause,
		RollbackStatus:           record.RollbackStatus,
		TransactionPresent:       record.TransactionPresent,
		TransactionID:            transactionID,
		LegacyMigration:          legacyMigration,
		CandidateMutation:        mutation,
	}
	projected := withNetworkSessionReplayProjection(disposition, record.NetworkApplySubphase, wrapped)
	store := newNetworkSessionResumeDiagnosticStore(continuation.runtimeDir, continuation.readBootID)
	if persistErr := store.SaveReplayFailure(record, attempt); persistErr != nil {
		cleanupErr := removeRetainedNetworkSessionReplayTransaction(continuation.runtimeDir, transactionID)
		return errors.Join(projected, persistErr, cleanupErr)
	}
	if disposition != networkSessionReplayDispositionTerminal {
		if cleanupErr := removeRetainedNetworkSessionReplayTransaction(continuation.runtimeDir, transactionID); cleanupErr != nil {
			return errors.Join(projected, cleanupErr)
		}
	}
	return projected
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
