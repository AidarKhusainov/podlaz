package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func issue262ProvenMandatoryEvidence() tunMandatoryEvidence {
	return tunMandatoryEvidence{
		SessionOwnership: tunLocalProofProven,
		CoreTUN:          tunLocalProofProven,
		OwnedComposition: tunLocalProofProven,
		PrivacyEnvelope:  tunLocalProofProven,
		UplinkPath:       tunLocalProofProven,
		NetworkManager:   tunLocalProofProven,
		ResolvedDNS:      tunLocalProofProven,
	}
}

func TestTunSupervisorInitialSoftFailureRetriesInsteadOfTerminal(t *testing.T) {
	now := time.Unix(100, 0)
	supervisor := newTunReconciliationSupervisor(func() time.Time { return now })
	decision := supervisor.RunRound(tunReconciliationRound{
		NetworkSessionID: "0123456789abcdef0123456789abcdef",
		TransactionID:    "tun-1",
		Generation:       1,
		Evidence: tunEvidenceSet{
			Mandatory: issue262ProvenMandatoryEvidence(),
			Probes:    []tunProbeEvidence{{Group: "dns-udp", Provider: "session-resolver", Success: false, Cause: errors.New("timeout")}},
		},
	})
	if decision.Kind != tunDecisionRetry {
		t.Fatalf("decision=%q, want retry", decision.Kind)
	}
	if decision.Kind == tunDecisionTerminal {
		t.Fatal("one soft generation-one failure must not be terminal")
	}
}

func TestTunSupervisorInitialMandatoryUnknownCannotVerify(t *testing.T) {
	supervisor := newTunReconciliationSupervisor(time.Now)
	mandatory := issue262ProvenMandatoryEvidence()
	mandatory.ResolvedDNS = tunLocalProofUnknown
	decision := supervisor.RunRound(tunReconciliationRound{
		NetworkSessionID: "0123456789abcdef0123456789abcdef",
		TransactionID:    "tun-1",
		Generation:       1,
		Evidence:         tunEvidenceSet{Mandatory: mandatory, Probes: issue262HealthyProbeEvidence()},
	})
	if decision.Kind != tunDecisionRetry || decision.Classification != api.TunHealthNetworkConverging {
		t.Fatalf("decision=%#v, want retry/network_converging", decision)
	}
}

func TestTunSupervisorOneProviderFailureCanStillVerify(t *testing.T) {
	supervisor := newTunReconciliationSupervisor(time.Now)
	probes := issue262HealthyProbeEvidence()
	probes = append(probes, tunProbeEvidence{Group: "https", Provider: "google", Success: false, Cause: errors.New("provider unavailable")})
	decision := supervisor.RunRound(tunReconciliationRound{
		NetworkSessionID: "0123456789abcdef0123456789abcdef",
		TransactionID:    "tun-1",
		Generation:       2,
		Evidence:         tunEvidenceSet{Mandatory: issue262ProvenMandatoryEvidence(), Probes: probes},
	})
	if decision.Kind != tunDecisionVerified {
		t.Fatalf("decision=%#v, want verified", decision)
	}
}

func TestTunSupervisorRepeatedEquivalentFailureConsumesNoProgressBudget(t *testing.T) {
	now := time.Unix(100, 0)
	supervisor := newTunReconciliationSupervisorWithPolicy(func() time.Time { return now }, time.Minute, 2)
	round := tunReconciliationRound{
		NetworkSessionID: "0123456789abcdef0123456789abcdef",
		TransactionID:    "tun-1",
		Generation:       2,
		Evidence: tunEvidenceSet{
			Mandatory: issue262ProvenMandatoryEvidence(),
			Probes: []tunProbeEvidence{
				{Group: "https", Provider: "cloudflare", Success: false, Cause: errors.New("unavailable")},
				{Group: "https", Provider: "google", Success: false, Cause: errors.New("unavailable")},
			},
		},
	}
	if got := supervisor.RunRound(round); got.Kind != tunDecisionRetry {
		t.Fatalf("first decision=%q, want retry", got.Kind)
	}
	if got := supervisor.RunRound(round); got.Kind != tunDecisionRetry {
		t.Fatalf("second decision=%q, want retry", got.Kind)
	}
	if got := supervisor.RunRound(round); got.Kind != tunDecisionTerminal {
		t.Fatalf("third decision=%q, want terminal after no-progress budget", got.Kind)
	}
}

func TestTunSupervisorChangingProgressDoesNotExtendOverallDeadline(t *testing.T) {
	now := time.Unix(100, 0)
	supervisor := newTunReconciliationSupervisorWithPolicy(func() time.Time { return now }, 10*time.Second, 10)
	base := tunReconciliationRound{
		NetworkSessionID: "0123456789abcdef0123456789abcdef",
		TransactionID:    "tun-1",
		Generation:       2,
		Evidence: tunEvidenceSet{
			Mandatory: issue262ProvenMandatoryEvidence(),
			Probes: []tunProbeEvidence{
				{Group: "https", Provider: "cloudflare", Success: false, Cause: errors.New("unavailable")},
				{Group: "https", Provider: "google", Success: false, Cause: errors.New("unavailable")},
			},
		},
	}
	base.Fingerprint = tunUplinkFingerprint{Interface: "wlan0", Gateway: "192.0.2.1"}
	if got := supervisor.RunRound(base); got.Kind != tunDecisionRetry {
		t.Fatalf("first decision=%q, want retry", got.Kind)
	}
	now = now.Add(8 * time.Second)
	base.Fingerprint.Gateway = "192.0.2.2"
	if got := supervisor.RunRound(base); got.Kind != tunDecisionRetry {
		t.Fatalf("progress decision=%q, want retry", got.Kind)
	}
	now = now.Add(3 * time.Second)
	base.Fingerprint.Gateway = "192.0.2.3"
	if got := supervisor.RunRound(base); got.Kind != tunDecisionTerminal {
		t.Fatalf("post-deadline decision=%q, want terminal despite progress", got.Kind)
	}
}

func issue262HealthyProbeEvidence() []tunProbeEvidence {
	return []tunProbeEvidence{
		{Group: "dns-tcp", Provider: "session-resolver", Success: true},
		{Group: "https", Provider: "cloudflare", Success: true},
	}
}
