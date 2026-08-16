package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestDoctorLifecycleChecksUseOneCapturedSnapshot(t *testing.T) {
	manager := NewXrayManager(t.TempDir())
	snapshot := doctorLifecycleSnapshot{state: inactiveXrayState(), coreRunning: false}

	manager.mu.Lock()
	manager.state = xrayState{Connection: "active", Mode: planner.ModeTun, TransactionID: "tx-new"}
	manager.mu.Unlock()

	checks := manager.lifecycleDoctorChecksFromSnapshot(context.Background(), snapshot)
	if len(checks) != 1 {
		t.Fatalf("inactive snapshot should produce only the core check, got %#v", checks)
	}
	if checks[0].Name != "core" || checks[0].Severity != doctor.SeverityOK || checks[0].Message != "inactive" {
		t.Fatalf("lifecycle checks re-read manager state instead of using the snapshot: %#v", checks)
	}
}

func TestDoctorPublicationGuardRejectsMutationBetweenStages(t *testing.T) {
	manager := NewXrayManager(t.TempDir())
	lock := newLifecycleOperationLock()

	manager.mu.Lock()
	manager.state = inactiveXrayState()
	manager.mu.Unlock()
	initial := manager.captureDoctorLifecycleSnapshot()
	before := lock.doctorMutationSnapshot()

	finishMutation := lock.beginMutation()
	manager.mu.Lock()
	manager.state = xrayState{Connection: "active", Mode: planner.ModeTun, TransactionID: "tx-new"}
	manager.mu.Unlock()
	finishMutation()

	current := manager.captureDoctorLifecycleSnapshot()
	after := lock.doctorMutationSnapshot()
	status := api.StatusResponse{Connection: "active", Mode: planner.ModeTun, ActiveTransactionID: "tx-new"}
	if doctorPublicationLifecycleStable(before, after, initial, current, status) {
		t.Fatal("doctor publication treated a lifecycle mutation between stages as stable")
	}

	assertIncompleteDoctorLifecycle(t, withIncompleteDoctorLifecycle(api.DoctorResponse{}, recovery.PlanResult{}))
}

func TestDoctorPublicationGuardRejectsAsynchronousLifecycleChange(t *testing.T) {
	manager := NewXrayManager(t.TempDir())
	lock := newLifecycleOperationLock()

	manager.mu.Lock()
	manager.state = inactiveXrayState()
	manager.mu.Unlock()
	initial := manager.captureDoctorLifecycleSnapshot()
	before := lock.doctorMutationSnapshot()

	// Model an asynchronous core/state transition that does not pass through the
	// connect/disconnect/recovery operation lock.
	manager.mu.Lock()
	manager.state = xrayState{Connection: "error (core exited)", Mode: planner.ModeTun, TransactionID: "tx-active"}
	manager.mu.Unlock()

	current := manager.captureDoctorLifecycleSnapshot()
	after := lock.doctorMutationSnapshot()
	status := api.StatusResponse{
		Connection: "inactive",
		Proxy:      "inactive",
		TUN:        "disabled",
		Routes:     "not modified",
		DNS:        "not modified",
		Firewall:   "not modified",
	}
	if doctorPublicationLifecycleStable(before, after, initial, current, status) {
		t.Fatal("doctor publication ignored an asynchronous lifecycle change during inspection")
	}
}

func assertIncompleteDoctorLifecycle(t *testing.T, response api.DoctorResponse) {
	t.Helper()
	if len(response.Checks) < 2 {
		t.Fatalf("expected lifecycle and startup-scan incomplete checks, got %#v", response.Checks)
	}
	var lifecycleCheck, startupCheck *api.DoctorCheck
	for i := range response.Checks {
		check := &response.Checks[i]
		switch check.Name {
		case "lifecycle-consistency":
			lifecycleCheck = check
		case "startup-recovery-scan":
			startupCheck = check
		}
	}
	if lifecycleCheck == nil || lifecycleCheck.Severity != string(doctor.SeverityWarning) || !strings.Contains(lifecycleCheck.Message, "lifecycle changed during diagnostic inspection") {
		t.Fatalf("missing lifecycle consistency warning: %#v", response.Checks)
	}
	if startupCheck == nil || startupCheck.Severity != string(doctor.SeverityWarning) || !strings.Contains(startupCheck.Message, "inspection incomplete") {
		t.Fatalf("startup scan was not forced incomplete: %#v", response.Checks)
	}
	if strings.Contains(startupCheck.Message, "clean for active connection") || strings.Contains(startupCheck.Message, "clean inactive state") {
		t.Fatalf("incomplete startup wording retained a contradictory lifecycle claim: %#v", startupCheck)
	}
}
