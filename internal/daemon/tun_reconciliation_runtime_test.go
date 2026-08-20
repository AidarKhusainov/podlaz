package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestEvidenceRuntimeGenerationOneSoftFailureDoesNotBecomeTerminal(t *testing.T) {
	runtime := newTunEvidenceRevalidationRuntime(func(context.Context) (tunReconciliationRound, error) {
		return tunReconciliationRound{
			NetworkSessionID: "0123456789abcdef0123456789abcdef",
			TransactionID:    "tx-a",
			Fingerprint:      tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 2, Gateway: "192.0.2.1"},
			Evidence: tunEvidenceSet{
				Mandatory: issue262ProvenMandatoryEvidence(),
				Probes: []tunProbeEvidence{
					{Group: "dns-udp", Provider: "session-resolver", Success: false, Cause: errors.New("timeout")},
				},
			},
		}, nil
	}, newTunReconciliationSupervisor(time.Now))
	runtime.PrepareInitialize()

	decision := runtime.RunEvidenceRound(context.Background(), tunRevalidationTriggerInitial, 7)
	if decision.Kind != tunDecisionRetry {
		t.Fatalf("generation-one decision=%#v, want retry", decision)
	}
	if decision.Disposition != nil {
		t.Fatalf("soft failure unexpectedly produced lifecycle disposition: %#v", decision.Disposition)
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthDegraded, 1, api.TunHealthConnectivityFailed)
	if !runtime.InitialPending() {
		t.Fatal("generation-one retry cleared pending proof")
	}
}

func TestEvidenceRuntimeMandatoryUnknownPublishesRevalidating(t *testing.T) {
	mandatory := issue262ProvenMandatoryEvidence()
	mandatory.ResolvedDNS = tunLocalProofUnknown
	runtime := newTunEvidenceRevalidationRuntime(func(context.Context) (tunReconciliationRound, error) {
		return tunReconciliationRound{
			NetworkSessionID: "0123456789abcdef0123456789abcdef",
			TransactionID:    "tx-a",
			Fingerprint:      tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 2, Gateway: "192.0.2.1"},
			Evidence:         tunEvidenceSet{Mandatory: mandatory, Probes: issue262HealthyProbeEvidence()},
		}, nil
	}, newTunReconciliationSupervisor(time.Now))
	runtime.PrepareInitialize()

	decision := runtime.RunEvidenceRound(context.Background(), tunRevalidationTriggerInitial, 3)
	if decision.Kind != tunDecisionRetry {
		t.Fatalf("mandatory unknown decision=%#v, want retry", decision)
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthRevalidating, 1, api.TunHealthNetworkConverging)
}

func TestEvidenceRuntimeReconcileDispositionCarriesSessionAndMutationGeneration(t *testing.T) {
	mandatory := issue262ProvenMandatoryEvidence()
	mandatory.CoreTUN = tunLocalProofViolated
	runtime := newTunEvidenceRevalidationRuntime(func(context.Context) (tunReconciliationRound, error) {
		return tunReconciliationRound{
			NetworkSessionID: "0123456789abcdef0123456789abcdef",
			TransactionID:    "tx-a",
			Fingerprint:      tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 2, Gateway: "192.0.2.1"},
			NeedsReconcile:   true,
			Evidence:         tunEvidenceSet{Mandatory: mandatory},
		}, nil
	}, newTunReconciliationSupervisor(time.Now))
	runtime.PrepareInitialize()

	decision := runtime.RunEvidenceRound(context.Background(), tunRevalidationTriggerInitial, 11)
	if decision.Kind != tunDecisionReconcile || decision.Disposition == nil {
		t.Fatalf("reconcile decision=%#v", decision)
	}
	if decision.Disposition.ExpectedMutationGeneration != 11 || decision.Disposition.NetworkSessionID != "0123456789abcdef0123456789abcdef" || decision.Disposition.TransactionID != "tx-a" {
		t.Fatalf("reconcile disposition lost fence identity: %#v", decision.Disposition)
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthRevalidating, 1, api.TunHealthOwnedStateReconciling)
}

func TestEvidenceRuntimeChangedFingerprintAdvancesGeneration(t *testing.T) {
	calls := 0
	runtime := newTunEvidenceRevalidationRuntime(func(context.Context) (tunReconciliationRound, error) {
		calls++
		gateway := "192.0.2.1"
		if calls > 1 {
			gateway = "192.0.2.254"
		}
		return tunReconciliationRound{
			NetworkSessionID: "0123456789abcdef0123456789abcdef",
			TransactionID:    "tx-a",
			Fingerprint:      tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 2, Gateway: gateway},
			Evidence:         tunEvidenceSet{Mandatory: issue262ProvenMandatoryEvidence(), Probes: issue262HealthyProbeEvidence()},
		}, nil
	}, newTunReconciliationSupervisor(time.Now))
	runtime.PrepareInitialize()
	if decision := runtime.RunEvidenceRound(context.Background(), tunRevalidationTriggerInitial, 1); decision.Kind != tunDecisionVerified {
		t.Fatalf("initial decision=%#v", decision)
	}
	if runtime.InitialPending() {
		t.Fatal("verified generation-one proof remained pending")
	}
	if decision := runtime.RunEvidenceRound(context.Background(), tunRevalidationTriggerRoute, 1); decision.Kind != tunDecisionVerified {
		t.Fatalf("changed uplink decision=%#v", decision)
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 2, "")
}
