package api

import "testing"

func TestTunHealthAcceptsNetworkConvergingRevalidation(t *testing.T) {
	health := TunHealthStatus{
		State:             TunHealthRevalidating,
		NetworkGeneration: 2,
		Classification:    TunHealthNetworkConverging,
	}
	if err := ValidateTunHealthStatus(health); err != nil {
		t.Fatalf("validate network-converging TUN health: %v", err)
	}
}

func TestTunHealthAcceptsOwnedStateReconcilingRevalidation(t *testing.T) {
	health := TunHealthStatus{
		State:             TunHealthRevalidating,
		NetworkGeneration: 2,
		Classification:    TunHealthOwnedStateReconciling,
	}
	if err := ValidateTunHealthStatus(health); err != nil {
		t.Fatalf("validate owned-state-reconciling TUN health: %v", err)
	}
}

func TestTunHealthRejectsReconciliationClassificationOutsideRevalidating(t *testing.T) {
	for _, classification := range []TunHealthClassification{
		TunHealthNetworkConverging,
		TunHealthOwnedStateReconciling,
	} {
		health := TunHealthStatus{
			State:             TunHealthDegraded,
			NetworkGeneration: 2,
			Classification:    classification,
		}
		if err := ValidateTunHealthStatus(health); err == nil {
			t.Fatalf("degraded TUN health unexpectedly accepted revalidation classification %q", classification)
		}
	}
}
