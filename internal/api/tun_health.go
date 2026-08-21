package api

import (
	"errors"
	"fmt"
)

type TunHealthState string

type TunHealthClassification string

const (
	TunHealthVerified        TunHealthState = "verified"
	TunHealthRevalidating    TunHealthState = "revalidating"
	TunHealthDegraded        TunHealthState = "degraded"
	TunHealthCleanupRequired TunHealthState = "cleanup-required"

	TunHealthUplinkRevalidating           TunHealthClassification = "uplink_revalidating"
	TunHealthUplinkChanged                TunHealthClassification = "uplink_changed"
	TunHealthNetworkConverging            TunHealthClassification = "network_converging"
	TunHealthOwnedStateReconciling        TunHealthClassification = "owned_state_reconciling"
	TunHealthUplinkFingerprintUnavailable TunHealthClassification = "uplink_fingerprint_unavailable"
	TunHealthOwnershipInvalid             TunHealthClassification = "ownership_invalid"
	TunHealthOwnedStateInvalid            TunHealthClassification = "owned_state_invalid"
	TunHealthConnectivityFailed           TunHealthClassification = "connectivity_failed"
	TunHealthRevalidationTimeout          TunHealthClassification = "revalidation_timeout"
	TunHealthRevalidationInterrupted      TunHealthClassification = "revalidation_interrupted"
)

// TunHealthStatus describes current evidence for an active committed TUN
// session. It is deliberately distinct from the durable transaction state:
// committed means the transaction finished successfully, while this value can
// become revalidating or degraded after the underlying network changes.
type TunHealthStatus struct {
	State             TunHealthState          `json:"state"`
	NetworkGeneration uint64                  `json:"network_generation"`
	Classification    TunHealthClassification `json:"classification,omitempty"`
}

func ValidateTunHealthStatus(health TunHealthStatus) error {
	if health.NetworkGeneration == 0 {
		return errors.New("TUN health requires a positive network generation")
	}
	switch health.State {
	case TunHealthVerified:
		if health.Classification != "" {
			return errors.New("verified TUN health must not carry a failure classification")
		}
	case TunHealthRevalidating:
		switch health.Classification {
		case TunHealthUplinkRevalidating,
			TunHealthUplinkChanged,
			TunHealthNetworkConverging,
			TunHealthOwnedStateReconciling:
			return nil
		default:
			return fmt.Errorf("revalidating TUN health has invalid classification %q", health.Classification)
		}
	case TunHealthDegraded, TunHealthCleanupRequired:
		if !validTunHealthFailureClassification(health.Classification) {
			return fmt.Errorf("%s TUN health requires a stable failure classification", health.State)
		}
	default:
		return fmt.Errorf("invalid TUN health state %q", health.State)
	}
	return nil
}

func validTunHealthFailureClassification(classification TunHealthClassification) bool {
	switch classification {
	case TunHealthUplinkFingerprintUnavailable,
		TunHealthOwnershipInvalid,
		TunHealthOwnedStateInvalid,
		TunHealthConnectivityFailed,
		TunHealthRevalidationTimeout,
		TunHealthRevalidationInterrupted:
		return true
	default:
		return false
	}
}
