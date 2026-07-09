package daemon

import (
	"errors"
	"strings"
)

const noTunTransactionID = "none"

// tunFailurePhaseError adds daemon-log metadata without changing user-facing
// error text or errors.Is/errors.As behavior.
type tunFailurePhaseError struct {
	phase          string
	transactionID  string
	rollbackStatus string
	err            error
}

func (e tunFailurePhaseError) Error() string {
	if e.err == nil {
		return "podlaz: TUN connect failed"
	}
	return e.err.Error()
}

func (e tunFailurePhaseError) Unwrap() error {
	return e.err
}

func withTunFailurePhase(phase, transactionID, rollbackStatus string, err error) error {
	if err == nil {
		return nil
	}
	phase = strings.TrimSpace(phase)
	if phase == "" {
		phase = "unknown"
	}
	transactionID = strings.TrimSpace(transactionID)
	if transactionID == "" {
		transactionID = noTunTransactionID
	}
	rollbackStatus = strings.TrimSpace(rollbackStatus)
	if rollbackStatus == "" {
		rollbackStatus = "not-started"
	}
	return tunFailurePhaseError{phase: phase, transactionID: transactionID, rollbackStatus: rollbackStatus, err: err}
}

func tunFailureLogFields(err error) (phase, transactionID, rollbackStatus string) {
	phase = "unknown"
	transactionID = noTunTransactionID
	rollbackStatus = "unknown"
	var phased tunFailurePhaseError
	if errors.As(err, &phased) {
		if strings.TrimSpace(phased.phase) != "" {
			phase = phased.phase
		}
		if strings.TrimSpace(phased.transactionID) != "" {
			transactionID = phased.transactionID
		}
		if strings.TrimSpace(phased.rollbackStatus) != "" {
			rollbackStatus = phased.rollbackStatus
		}
	}
	if varificationPhase := tunVerificationPhase(err); varificationPhase != "" {
		phase = varificationPhase + "-verify"
	}
	return phase, transactionID, rollbackStatus
}

func tunVerificationPhase(err error) string {
	var verification *TunVerificationError
	if !errors.As(err, &verification) {
		return ""
	}
	phase := strings.TrimSpace(verification.Phase)
	if phase == "" {
		return "connectivity"
	}
	return phase
}
