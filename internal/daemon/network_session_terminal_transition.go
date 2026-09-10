package daemon

type networkSessionTerminalTransitionOutcome string

const (
	networkSessionTerminalTransitionRejected  networkSessionTerminalTransitionOutcome = "rejected"
	networkSessionTerminalTransitionCommitted networkSessionTerminalTransitionOutcome = "committed"
)

type networkSessionTerminalCleanupWitness struct {
	sessionID     string
	recoveryEpoch uint64
	proven        bool
}

func (s networkSessionStateStore) TransitionReplayToTerminal(
	attempt networkSessionReplayAttempt,
	witness networkSessionTerminalCleanupWitness,
) (networkSessionTerminalTransitionOutcome, error) {
	if !networkSessionTerminalAttemptCleanupSafe(attempt) || !witness.proven {
		return networkSessionTerminalTransitionRejected, nil
	}
	if witness.sessionID != attempt.SessionID || witness.recoveryEpoch != attempt.RecoveryEpoch {
		return networkSessionTerminalTransitionRejected, nil
	}

	lock := s.mutationLock()
	lock.Lock()
	defer lock.Unlock()

	state, exists, err := s.loadLocked()
	if err != nil {
		return networkSessionTerminalTransitionRejected, err
	}
	if !exists || state.Intent != networkSessionIntentResume || state.Protection == nil {
		return networkSessionTerminalTransitionRejected, nil
	}
	if state.SessionID != attempt.SessionID || state.RecoveryEpoch != attempt.RecoveryEpoch {
		return networkSessionTerminalTransitionRejected, nil
	}

	state = cloneNetworkSessionState(state)
	state.Intent = networkSessionIntentTerminal
	if err := s.save(state); err != nil {
		return networkSessionTerminalTransitionRejected, err
	}
	return networkSessionTerminalTransitionCommitted, nil
}

func networkSessionTerminalAttemptCleanupSafe(attempt networkSessionReplayAttempt) bool {
	if attempt.ReplayDisposition != networkSessionReplayDispositionTerminal {
		return false
	}
	switch attempt.CandidateMutation {
	case networkSessionCandidateMutationNotOpened:
		return attempt.RollbackStatus == "not-started" && !attempt.TransactionPresent
	case networkSessionCandidateMutationRolledBack:
		return attempt.RollbackStatus == "completed"
	default:
		return false
	}
}
