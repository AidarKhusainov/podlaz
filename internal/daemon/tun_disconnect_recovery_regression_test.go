package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunPlanFromTransactionReconstructsExactFirewallForActiveDisconnect(t *testing.T) {
	firewall := planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      "inet",
		Table:       "podlaz",
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     planner.FirewallOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   planner.FirewallTableAction,
		}},
		Rules: []planner.TunFirewallRulePlan{{
			Chain:       planner.FirewallOutputChain,
			Expr:        `oifname != "podlaz0"`,
			Verdict:     planner.FirewallVerdictReject,
			Action:      planner.FirewallActionAdd,
			Ownership:   planner.FirewallKillSwitchOwner,
			RollbackKey: planner.FirewallKillSwitchKey,
		}},
	}
	plan := planner.TunPlan{ProfileID: "example-profile", Mode: planner.ModeTun, Firewall: firewall}
	tx := txstate.NewTransaction("tun-disconnect", plan.ProfileID, plan.Mode, fixedClock()())
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	tx.Rollback = rollbackMetadataFromTunPlan(plan)

	got := tunPlanFromTransaction(tx).Firewall
	if len(got.Chains) != 1 || len(got.Rules) != 1 {
		t.Fatalf("active disconnect lost exact nftables composition: %#v", got)
	}
	if !reflect.DeepEqual(got.Chains, firewall.Chains) {
		t.Fatalf("reconstructed chains = %#v, want %#v", got.Chains, firewall.Chains)
	}
	if !reflect.DeepEqual(got.Rules, firewall.Rules) {
		t.Fatalf("reconstructed rules = %#v, want %#v", got.Rules, firewall.Rules)
	}
}

func TestTunPlanFromTransactionDoesNotGrantFirewallRollbackFromDesiredIntentAlone(t *testing.T) {
	plan := planner.TunPlan{
		ProfileID: "example-profile",
		Mode:      planner.ModeTun,
		Firewall: planner.TunFirewallPlan{
			Backend:     planner.FirewallBackendNftables,
			Family:      "inet",
			Table:       "podlaz",
			TableAction: planner.FirewallTableAction,
			Chains: []planner.TunFirewallChainPlan{{
				Name: planner.FirewallOutputChain, Type: planner.FirewallChainTypeFilter,
				Hook: planner.FirewallOutputHook, Priority: planner.FirewallOutputPriority,
				Policy: planner.FirewallDefaultChainPolicy, Action: planner.FirewallTableAction,
			}},
		},
	}
	tx := txstate.NewTransaction("tun-disconnect-no-authority", plan.ProfileID, plan.Mode, fixedClock()())
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)

	if got := tunPlanFromTransaction(tx).Firewall; !reflect.DeepEqual(got, planner.TunFirewallPlan{}) {
		t.Fatalf("desired nftables intent must not grant rollback authority: %#v", got)
	}
}

func TestInspectNetworkSessionRecoveryPlanExposesTerminalIntentWithOpenStartupGate(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatalf("save network session: %v", err)
	}
	if err := continuation.disarm(networkSessionIntentDisconnect); err != nil {
		t.Fatalf("persist disconnect intent: %v", err)
	}
	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})

	plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
	if err != nil {
		t.Fatalf("inspect terminal recovery plan: %v", err)
	}
	if plan == nil {
		t.Fatal("open startup gate must not hide persisted terminal Network Session recovery work")
	}
	if plan.Intent != string(networkSessionIntentDisconnect) || plan.StartupGate != api.NetworkSessionStartupGateOpen || plan.NextAction != api.NetworkSessionRecoveryActionContinueTeardown {
		t.Fatalf("unexpected terminal recovery plan: %#v", plan)
	}
}

func TestCleanupRequiredActiveStatusDoesNotSuppressExactRecovery(t *testing.T) {
	status := api.StatusResponse{
		Connection: "active",
		Mode:       planner.ModeTun,
		TunHealth: &api.TunHealthStatus{
			State:             api.TunHealthCleanupRequired,
			NetworkGeneration: 1,
			Classification:    api.TunHealthOwnershipInvalid,
		},
		Transactions: []api.TransactionStatus{{
			ID: "tx-example", State: "failed", RollbackAvailable: true, RequiresCleanup: true, Path: "/run/podlaz/transactions/tx-example.json",
		}},
	}
	if activeStatusMustKeepRecoveryMutationFree(status) {
		t.Fatal("cleanup-required active publication must allow exact recovery")
	}

	status.TunHealth = &api.TunHealthStatus{State: api.TunHealthVerified, NetworkGeneration: 1}
	status.Transactions = nil
	if !activeStatusMustKeepRecoveryMutationFree(status) {
		t.Fatal("healthy active publication must remain mutation-free")
	}
}

func TestRecoverExecuteContinuesOpenGateTerminalNetworkSession(t *testing.T) {
	installRecoveryObservationFakes(t)

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
	if err := continuation.disarm(networkSessionIntentDisconnect); err != nil {
		t.Fatal(err)
	}

	exactCalls := 0
	teardownCalls := 0
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		exactCalls++
		return api.RecoveryResponse{Mode: "execute"}
	}
	continuation.continueTeardown = func(context.Context, networkSessionStateStore) error {
		teardownCalls++
		return nil
	}

	events := []string{}
	sessionLifecycle := newNetworkSessionLifecycle(networkSessionRecordingLifecycle{events: &events}, continuation)
	gate := newNetworkSessionStartupMutationGate(sessionLifecycle)
	runtime := &daemonRuntime{
		runtimeDir: runtimeDir,
		authorizer: AllowAuthorizer{},
		currentStatus: func(context.Context) api.StatusResponse {
			return api.StatusResponse{
				Connection: "active",
				Mode:       planner.ModeTun,
				TunHealth: &api.TunHealthStatus{
					State: api.TunHealthCleanupRequired, NetworkGeneration: 1,
					Classification: api.TunHealthOwnershipInvalid,
				},
			}
		},
		operationLock:           newLifecycleOperationLock(),
		continuation:            continuation,
		sessionLifecycle:        sessionLifecycle,
		startupMutationGate:     gate,
		forceRefreshStartupScan: func(context.Context) {},
	}
	httpServer := (Server{}).newHTTPServer(runtime, newBootAutostartManifestStore(t.TempDir(), fixedBootID("boot-a")))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, api.RecoverPath, nil)
	httpServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("recover status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var response api.RecoveryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode recover response: %v", err)
	}
	if exactCalls != 1 || teardownCalls != 1 {
		t.Fatalf("terminal recovery stages: exact=%d teardown=%d, want 1/1", exactCalls, teardownCalls)
	}
	if response.NetworkSession == nil || response.NetworkSession.LastResumeOutcome != api.NetworkSessionResumeOutcomeSucceeded || response.NetworkSession.NextAction != api.NetworkSessionRecoveryActionNone {
		t.Fatalf("terminal recovery did not converge: %#v", response.NetworkSession)
	}
	if len(response.Warnings) != 0 {
		t.Fatalf("terminal recovery returned warnings: %#v", response.Warnings)
	}
}
