package daemon

import (
	"strings"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestIssue256OrphanPolicyRoutingDoesNotPromiseUnauthoritativeRecovery(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.Snapshot{StaleResources: []netsnapshot.StaleResource{
		{Kind: "policy-rule", Name: "priority-9999", Status: netsnapshot.StatusDetected},
		{Kind: "policy-rule", Name: "priority-10000", Status: netsnapshot.StatusDetected},
	}}, "block")
	if err == nil {
		t.Fatal("expected orphan routing preflight blocker")
	}
	message := err.Error()
	if strings.Contains(message, "recover --execute") {
		t.Fatalf("orphan routing without ownership evidence must not promise recovery: %s", message)
	}
	if !strings.Contains(message, "ownership evidence is unavailable") {
		t.Fatalf("expected explicit ownership-evidence guidance, got: %s", message)
	}
	if !strings.Contains(message, "blocks TUN connect before network mutation") {
		t.Fatalf("expected fail-closed preflight wording, got: %s", message)
	}
}

func TestIssue256RecoverableStaleStateStillKeepsCanonicalRecoveryGuidance(t *testing.T) {
	err := withTunFailurePhase("handoff", "", "not-started", &tunStalePodlazStateBlocker{Resources: []string{
		"tun-device podlaz0",
	}})
	if !strings.Contains(err.Error(), "plz recover --execute --yes") {
		t.Fatalf("recoverable stale state should retain canonical recovery guidance: %s", err)
	}
}

func TestIssue256TransactionBackedRoutingKeepsRecoveryGuidance(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.Snapshot{StaleResources: []netsnapshot.StaleResource{
		{Kind: "transaction-file", Name: "tx-recoverable.json", Status: netsnapshot.StatusDetected, Detail: "state=committed requires daemon-owned recovery"},
		{Kind: "route", Name: "51820", Status: netsnapshot.StatusDetected},
		{Kind: "policy-rule", Name: "10000", Status: netsnapshot.StatusDetected},
	}}, "block")
	if err == nil {
		t.Fatal("expected transaction-backed stale preflight blocker")
	}
	message := err.Error()
	if !strings.Contains(message, "plz recover --execute --yes") {
		t.Fatalf("transaction-backed routing must retain authoritative recovery guidance: %s", message)
	}
	if strings.Contains(message, "ownership evidence is unavailable") {
		t.Fatalf("transaction-backed routing must not be classified as orphan ownership: %s", message)
	}
	if strings.Contains(message, "Remove them manually") {
		t.Fatalf("transaction-backed routing must not recommend manual cleanup: %s", message)
	}
}
