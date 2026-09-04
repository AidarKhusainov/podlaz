package api

import (
	"errors"
	"fmt"
)

const RecoverPath = "/v1/recover"

type RecoveryResponse struct {
	Mode           string                       `json:"mode"`
	Results        []RecoveryCleanupResult      `json:"results,omitempty"`
	Warnings       []RecoveryWarning            `json:"warnings,omitempty"`
	NetworkSession *NetworkSessionRecoveryState `json:"network_session,omitempty"`
}

type RecoveryCleanupResult struct {
	Candidate RecoveryCandidate `json:"candidate"`
	Status    string            `json:"status"`
	Message   string            `json:"message,omitempty"`
}

type RecoveryCandidate struct {
	Kind        string                        `json:"kind"`
	Description string                        `json:"description"`
	Target      string                        `json:"target"`
	Transaction *RecoveryTransactionCandidate `json:"transaction,omitempty"`
}

type RecoveryTransactionCandidate struct {
	ID                string `json:"id"`
	State             string `json:"state"`
	Status            string `json:"status"`
	RollbackAvailable bool   `json:"rollback_available"`
	RequiresCleanup   bool   `json:"requires_cleanup"`
	Path              string `json:"path"`
}

type RecoveryWarning struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

func ValidateRecoveryResponse(r RecoveryResponse) error {
	if r.Mode != "execute" {
		return fmt.Errorf("invalid recovery mode %q", r.Mode)
	}
	for _, result := range r.Results {
		if err := ValidateRecoveryCleanupResult(result); err != nil {
			return err
		}
	}
	if r.NetworkSession != nil {
		if err := ValidateNetworkSessionRecoveryState(*r.NetworkSession); err != nil {
			return err
		}
	}
	return nil
}

func ValidateRecoveryCleanupResult(result RecoveryCleanupResult) error {
	if err := ValidateRecoveryCandidate(result.Candidate); err != nil {
		return err
	}
	switch result.Status {
	case "recovered", "skipped", "failed":
		return nil
	case "":
		return errors.New("missing recovery result status")
	default:
		return fmt.Errorf("invalid recovery result status %q", result.Status)
	}
}

func ValidateRecoveryCandidate(candidate RecoveryCandidate) error {
	switch {
	case candidate.Kind == "":
		return errors.New("missing recovery candidate kind")
	case candidate.Description == "":
		return errors.New("missing recovery candidate description")
	case candidate.Target == "":
		return errors.New("missing recovery candidate target")
	}
	if candidate.Transaction != nil && candidate.Transaction.ID == "" {
		return errors.New("missing recovery transaction id")
	}
	return nil
}
