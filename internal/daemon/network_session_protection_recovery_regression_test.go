package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestNetworkSessionRecoveryPlanIncludesProtectionOnlyAuthorityWithoutTransaction(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	if err := continuation.stateStore().SetProtection(&networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}); err != nil {
		t.Fatal(err)
	}

	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
	gate.Block()
	plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
	if err != nil {
		t.Fatalf("inspect protection-only Network Session recovery: %v", err)
	}
	if plan == nil {
		t.Fatal("retained Privacy Envelope authority must remain visible as recovery work")
	}
	if plan.Authority != api.NetworkSessionRecoveryAuthorityPresent {
		t.Fatalf("authority=%q, want present", plan.Authority)
	}
	if plan.CleanupAuthority != api.NetworkSessionCleanupAuthoritySessionProtection {
		t.Fatalf("cleanup authority=%q, want session protection", plan.CleanupAuthority)
	}
	if plan.TransactionPresent {
		t.Fatalf("protection-only recovery invented transaction authority: %#v", plan)
	}
	if plan.NextAction != api.NetworkSessionRecoveryActionRetryResume {
		t.Fatalf("next action=%q, want retry resume", plan.NextAction)
	}
}
