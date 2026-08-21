package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestIssue262OneSoftProviderFailureDoesNotDisconnect(t *testing.T) {
	supervisor := newTunReconciliationSupervisorWithPolicy(time.Now, time.Minute, 3)
	decision := supervisor.RunRound(tunReconciliationRound{
		NetworkSessionID: "session-example",
		Evidence: tunEvidenceSet{
			Mandatory: issue262ProvenMandatoryEvidence(),
			Probes: []tunProbeEvidence{
				{Group: "dns-udp", Provider: "session-resolver", Success: true},
				{Group: "tls", Provider: "cloudflare", Success: false, Cause: errors.New("temporary TLS failure")},
				{Group: "https", Provider: "google", Success: true},
			},
		},
	})
	if decision.Kind != tunDecisionVerified {
		t.Fatalf("one soft provider failure decision = %q, want verified", decision.Kind)
	}
}

func TestIssue262MandatoryLocalUnknownCannotBeOutvotedByExternalSuccess(t *testing.T) {
	mandatory := issue262ProvenMandatoryEvidence()
	mandatory.ResolvedDNS = tunLocalProofUnknown
	decision := newTunReconciliationSupervisor(nil).RunRound(tunReconciliationRound{
		NetworkSessionID: "session-example",
		Evidence: tunEvidenceSet{
			Mandatory: mandatory,
			Probes: []tunProbeEvidence{
				{Group: "dns-udp", Provider: "session-resolver", Success: true},
				{Group: "https", Provider: "cloudflare", Success: true},
				{Group: "https", Provider: "google", Success: true},
			},
		},
	})
	if decision.Kind != tunDecisionRetry || decision.Classification != api.TunHealthNetworkConverging {
		t.Fatalf("mandatory local unknown decision = %#v, want retry/network_converging", decision)
	}
}

func TestIssue262RepairableOwnedDriftRequestsReconcileNotImmediateTerminal(t *testing.T) {
	mandatory := issue262ProvenMandatoryEvidence()
	mandatory.OwnedComposition = tunLocalProofViolated
	decision := newTunReconciliationSupervisor(nil).RunRound(tunReconciliationRound{
		NetworkSessionID: "session-example",
		TransactionID:    "transaction-example",
		NeedsReconcile:   true,
		Evidence:         tunEvidenceSet{Mandatory: mandatory},
	})
	if decision.Kind != tunDecisionReconcile || decision.Disposition == nil {
		t.Fatalf("repairable drift decision = %#v, want reconcile", decision)
	}
	if decision.Disposition.NetworkSessionID != "session-example" || decision.Disposition.TransactionID != "transaction-example" {
		t.Fatalf("reconcile lost exact lifecycle identity: %#v", decision.Disposition)
	}
}

func TestIssue262ConfirmedPrivacyBoundaryFailureIsTerminal(t *testing.T) {
	mandatory := issue262ProvenMandatoryEvidence()
	mandatory.PrivacyEnvelope = tunLocalProofViolated
	decision := newTunReconciliationSupervisor(nil).RunRound(tunReconciliationRound{
		NetworkSessionID: "session-example",
		HardUnsafe:       true,
		Evidence:         tunEvidenceSet{Mandatory: mandatory},
		Cause:            errors.New("confirmed exact privacy barrier absence"),
	})
	if decision.Kind != tunDecisionTerminal || decision.Disposition == nil {
		t.Fatalf("hard privacy failure decision = %#v, want terminal", decision)
	}
}
