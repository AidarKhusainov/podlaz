package daemon

import (
	"strings"
	"testing"
)

func TestIssue256OrphanPolicyRoutingDoesNotPromiseUnauthoritativeRecovery(t *testing.T) {
	err := withTunFailurePhase("handoff", "", "not-started", &tunStalePodlazStateBlocker{Resources: []string{
		"policy-rule priority-9999",
		"policy-rule priority-10000",
	}})
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
