package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestNetworkSessionRecoveryPlanSurfacesBlockedResumeWithoutTransactions(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
	gate.Block()

	plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
	if err != nil {
		t.Fatalf("inspect retained Network Session: %v", err)
	}
	if plan == nil {
		t.Fatal("blocked current-boot resume authority must be a recovery plan")
	}
	if plan.Authority != api.NetworkSessionRecoveryAuthorityPresent || plan.Intent != string(networkSessionIntentResume) {
		t.Fatalf("unexpected authority plan: %#v", plan)
	}
	if plan.StartupGate != api.NetworkSessionStartupGateBlocked || plan.NextAction != api.NetworkSessionRecoveryActionRetryResume {
		t.Fatalf("unexpected blocked resume action: %#v", plan)
	}
	if plan.LastResumeOutcome != api.NetworkSessionResumeOutcomeNotAttempted {
		t.Fatalf("unexpected initial outcome: %#v", plan)
	}
	if plan.CleanupAuthority != api.NetworkSessionCleanupAuthorityNone {
		t.Fatalf("transaction cleanup authority must not be invented: %#v", plan)
	}
}

func TestNetworkSessionRecoveryInspectionIsMutationFreeForLegacyContinuation(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	record := networkSessionContinuation{
		SchemaVersion: networkSessionContinuationSchemaVersion,
		Owner:         networkSessionContinuationOwner,
		BootID:        "boot-a",
		Request:       testContinuationRequest(),
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWritePrivateFile(continuation.path(), append(data, '\n')); err != nil {
		t.Fatal(err)
	}
	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
	gate.Block()

	plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
	if err != nil || plan == nil {
		t.Fatalf("inspect legacy authority: plan=%#v err=%v", plan, err)
	}
	if !plan.LegacyMigration || plan.NextAction != api.NetworkSessionRecoveryActionRetryResume {
		t.Fatalf("legacy plan=%#v", plan)
	}

	after, err := os.ReadFile(continuation.path())
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(after, &header); err != nil {
		t.Fatal(err)
	}
	if header.SchemaVersion != networkSessionContinuationSchemaVersion {
		t.Fatalf("read-only recovery inspection migrated state to %q", header.SchemaVersion)
	}
}

func TestNetworkSessionRecoveryPlanUsesPersistedResumeFailureModel(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
	_, resumeErr := resumeNetworkSession(
		context.Background(),
		continuation,
		resumeFailingLifecycle{err: withTunFailurePhase("core-preflight", noTunTransactionID, "not-started", context.DeadlineExceeded)},
		inactiveNetworkSessionStatus,
		successfulNetworkSessionRecovery,
	)
	if resumeErr == nil {
		t.Fatal("expected resume failure")
	}
	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
	gate.Block()

	plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
	if err != nil {
		t.Fatal(err)
	}
	if plan.LastResumeOutcome != api.NetworkSessionResumeOutcomeFailed || plan.ResumeStage != api.NetworkSessionResumeStageConnectReplay {
		t.Fatalf("resume failure plan=%#v", plan)
	}
	if plan.LastTUNFailurePhase != "core-preflight" || plan.RollbackStatus != "not-started" {
		t.Fatalf("TUN failure metadata not reused: %#v", plan)
	}
}

func TestNetworkSessionRecoveryPlanRepresentsTerminalIntentWithoutResume(t *testing.T) {
	for _, intent := range []networkSessionIntent{networkSessionIntentDisconnect, networkSessionIntentTerminal} {
		t.Run(string(intent), func(t *testing.T) {
			runtimeDir := t.TempDir()
			continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
			if err := continuation.Save(testContinuationRequest()); err != nil {
				t.Fatal(err)
			}
			if err := continuation.stateStore().SetIntent(intent); err != nil {
				t.Fatal(err)
			}
			gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
			gate.Block()

			plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
			if err != nil || plan == nil {
				t.Fatalf("plan=%#v err=%v", plan, err)
			}
			if plan.NextAction != api.NetworkSessionRecoveryActionContinueTeardown {
				t.Fatalf("terminal intent became resume work: %#v", plan)
			}
		})
	}
}

func TestStartupRecoveryScanTreatsRetainedNetworkSessionAsRecoveryWork(t *testing.T) {
	scan := recovery.PlanResult{NetworkSession: &api.NetworkSessionRecoveryState{
		Authority:         api.NetworkSessionRecoveryAuthorityPresent,
		Intent:            string(networkSessionIntentResume),
		StartupGate:       api.NetworkSessionStartupGateBlocked,
		LastResumeOutcome: api.NetworkSessionResumeOutcomeFailed,
		NextAction:        api.NetworkSessionRecoveryActionRetryResume,
		CleanupAuthority:  api.NetworkSessionCleanupAuthorityNone,
	}}
	if status := startupScanStatus(scan); status == api.StartupScanStatusClean {
		t.Fatal("retained blocked Network Session must not publish a clean recovery scan")
	}
	if action := startupScanSuggestedAction(scan); action != "podlaz recover" {
		t.Fatalf("suggested action=%q", action)
	}
	published := startupScanToAPI(scan)
	if published.NetworkSession == nil || published.NetworkSession.NextAction != api.NetworkSessionRecoveryActionRetryResume {
		t.Fatalf("startup scan lost Network Session plan: %#v", published)
	}
}

func TestFailedRecoveryRetryKeepsGateBlockedAndTypedReason(t *testing.T) {
	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
	gate.Block()
	response := api.RecoveryResponse{
		Mode: "execute",
		NetworkSession: &api.NetworkSessionRecoveryState{
			Authority:         api.NetworkSessionRecoveryAuthorityPresent,
			Intent:            string(networkSessionIntentResume),
			StartupGate:       api.NetworkSessionStartupGateBlocked,
			LastResumeOutcome: api.NetworkSessionResumeOutcomeNotAttempted,
			NextAction:        api.NetworkSessionRecoveryActionRetryResume,
			CleanupAuthority:  api.NetworkSessionCleanupAuthorityNone,
		},
	}
	resumeErr := newNetworkSessionResumeError(api.NetworkSessionResumeStageConnectReplay, false, withTunFailurePhase("preflight", noTunTransactionID, "not-started", context.DeadlineExceeded))

	got := applyNetworkSessionResumeResult(response, gate, resumeErr)
	if !gate.Blocked() {
		t.Fatal("failed retry must remain fail-closed")
	}
	if got.NetworkSession == nil || got.NetworkSession.LastResumeOutcome != api.NetworkSessionResumeOutcomeFailed {
		t.Fatalf("failed retry lost semantic reason: %#v", got.NetworkSession)
	}
	if got.NetworkSession.ResumeStage != api.NetworkSessionResumeStageConnectReplay || got.NetworkSession.LastTUNFailurePhase != "preflight" {
		t.Fatalf("failed retry metadata=%#v", got.NetworkSession)
	}
	for _, warning := range got.Warnings {
		if strings.Contains(warning.Message, "DeadlineExceeded") {
			t.Fatalf("warning leaked nested error: %#v", warning)
		}
	}
}
