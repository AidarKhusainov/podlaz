package daemon

import (
	"errors"
	"fmt"
	"strings"
)

const noTunTransactionID = "none"

// tunFailurePhaseError adds daemon-log metadata without changing errors.Is/errors.As behavior.
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
	var stale *tunStalePodlazStateBlocker
	if errors.As(e.err, &stale) && orphanRoutingNeedsOwnershipEvidence(stale.Resources) {
		return fmt.Sprintf(`podlaz: ambiguous stale routing state blocks TUN connect before network mutation.

Detected:
  - %s

The remaining policy-rule/route shape matches Podlaz's reserved routing layout, but durable transaction ownership evidence is unavailable. Recovery cannot safely delete these kernel objects from reserved priorities/table numbers alone.

Next step: run plz doctor and inspect the reported rules/routes as administrator. Remove them manually only after independently proving ownership, then retry connect.`, strings.Join(stale.Resources, "\n  - "))
	}
	return e.err.Error()
}

func orphanRoutingNeedsOwnershipEvidence(resources []string) bool {
	for _, resource := range resources {
		resource = strings.TrimSpace(resource)
		if strings.HasPrefix(resource, "policy-rule ") || strings.HasPrefix(resource, "route ") {
			return true
		}
	}
	return false
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
	if verificationPhase := tunVerificationPhase(err); verificationPhase != "" {
		phase = verificationPhase + "-verify"
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
