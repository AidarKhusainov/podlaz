package daemon

import (
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestUnexpectedTunCoreExitSchedulesProtectedReconciliation(t *testing.T) {
	manager := &XrayManager{}
	manager.state = xrayState{Connection: "error (core exited)", Mode: planner.ModeTun}
	notified := make(chan tunRevalidationTrigger, 1)
	lifecycle := startupScanRefreshingLifecycle{
		lifecycle: manager,
		revalidate: func(trigger tunRevalidationTrigger) {
			notified <- trigger
		},
	}
	lifecycle.watchUnexpectedCoreExit()
	select {
	case trigger := <-notified:
		if trigger != tunRevalidationTriggerSourceResync {
			t.Fatalf("core-exit trigger = %q, want source-resync", trigger)
		}
	case <-time.After(time.Second):
		t.Fatal("unexpected TUN core exit did not schedule reconciliation")
	}
}

func TestDegradedCoreEvidenceRequestsRebuildBeforeTerminal(t *testing.T) {
	mandatory := issue262ProvenMandatoryEvidence()
	mandatory.CoreTUN = tunLocalProofViolated
	decision := newTunReconciliationSupervisor(nil).RunRound(tunReconciliationRound{
		NetworkSessionID: "session-example",
		TransactionID:    "transaction-example",
		NeedsReconcile:   true,
		Evidence:         tunEvidenceSet{Mandatory: mandatory},
	})
	if decision.Kind != tunDecisionReconcile {
		t.Fatalf("degraded core decision = %q, want reconcile", decision.Kind)
	}
	if decision.Disposition == nil || decision.Disposition.NetworkSessionID != "session-example" {
		t.Fatalf("degraded core rebuild lost session fence: %#v", decision.Disposition)
	}
}
