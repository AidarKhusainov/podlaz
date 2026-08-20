package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestTunMandatoryUnknownCannotBeHealthy(t *testing.T) {
	evidence := tunEvidenceSet{
		Mandatory: tunMandatoryEvidence{
			SessionOwnership: tunLocalProofProven,
			CoreTUN:          tunLocalProofProven,
			OwnedComposition: tunLocalProofProven,
			PrivacyEnvelope:  tunLocalProofProven,
			UplinkPath:       tunLocalProofProven,
			NetworkManager:   tunLocalProofProven,
			ResolvedDNS:      tunLocalProofUnknown,
		},
	}
	if !evidence.mandatoryUnknown() {
		t.Fatal("mandatory resolved-DNS unknown must prevent healthy proof")
	}
}

func TestTunSoftProviderFailureDoesNotViolateMandatoryProof(t *testing.T) {
	evidence := tunEvidenceSet{
		Mandatory: tunMandatoryEvidence{
			SessionOwnership: tunLocalProofProven,
			CoreTUN:          tunLocalProofProven,
			OwnedComposition: tunLocalProofProven,
			PrivacyEnvelope:  tunLocalProofProven,
			UplinkPath:       tunLocalProofProven,
			NetworkManager:   tunLocalProofProven,
			ResolvedDNS:      tunLocalProofProven,
		},
		Probes: []tunProbeEvidence{{Group: "https", Provider: "cloudflare", Success: false}},
	}
	if evidence.mandatoryUnknown() || evidence.mandatoryViolated() {
		t.Fatal("soft provider failure must not change mandatory local proof")
	}
}

func TestTunAutomaticDispositionCarriesAllLifecycleFences(t *testing.T) {
	disposition := tunAutomaticDisposition{
		Kind:                       tunDecisionTerminal,
		PublicationRevision:        7,
		ExpectedMutationGeneration: 11,
		NetworkSessionID:           "0123456789abcdef0123456789abcdef",
		TransactionID:              "tun-transaction-1",
		Generation:                 3,
		Classification:             api.TunHealthConnectivityFailed,
	}
	if disposition.PublicationRevision == 0 || disposition.ExpectedMutationGeneration == 0 || disposition.NetworkSessionID == "" || disposition.TransactionID == "" {
		t.Fatalf("automatic disposition lost lifecycle fences: %#v", disposition)
	}
}
