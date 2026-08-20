package daemon

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type issue262ReconcileLifecycle struct {
	store   networkSessionStateStore
	wantID  string
	connect int
}

func (l *issue262ReconcileLifecycle) Connect(_ context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	l.connect++
	state, exists, err := l.store.Load()
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	if !exists || state.SessionID != l.wantID || state.Replacement == nil {
		return api.LifecycleResponse{}, errIssue262MissingReplacementAuthority
	}
	if api.NormalizeHandoffPolicy(request.Handoff) != api.HandoffReplacePodlaz || !reflect.DeepEqual(state.Request, request) {
		return api.LifecycleResponse{}, errIssue262MissingReplacementAuthority
	}
	return api.LifecycleResponse{Connection: "active", Mode: planner.ModeTun, TUN: "active"}, nil
}

func (l *issue262ReconcileLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}

var errIssue262MissingReplacementAuthority = &issue262TestError{"replacement authority was not durable before connect"}

type issue262TestError struct{ message string }

func (e *issue262TestError) Error() string { return e.message }

func TestDegradedProtectedRebuildPersistsReplacementBeforeUnwrappedConnect(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	store := continuation.stateStore()
	request := testContinuationRequest()
	state, err := store.BeginOrResume(request)
	if err != nil {
		t.Fatal(err)
	}
	protection := networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}
	if err := store.SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	inner := &issue262ReconcileLifecycle{store: store, wantID: state.SessionID}
	lifecycle := newNetworkSessionLifecycle(inner, continuation)

	if err := lifecycle.ReconcileProtectedTun(context.Background(), state.SessionID); err != nil {
		t.Fatalf("reconcile protected TUN: %v", err)
	}
	if inner.connect != 1 {
		t.Fatalf("internal reconcile connect calls=%d, want 1", inner.connect)
	}
}

func TestDegradedProtectedRebuildWidensEnvelopeBeforeOldGenerationCleanup(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	original := testContinuationRequest()
	state, err := store.BeginOrResume(original)
	if err != nil {
		t.Fatal(err)
	}
	oldProtection := networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}
	if err := store.SetProtection(&oldProtection); err != nil {
		t.Fatal(err)
	}

	configPath := runtimeDir + "/generated/xray.json"
	tx := txstate.NewTransaction("tx-degraded-rebuild", original.Profile.ID, planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, ProcessLabel: "xray", Owner: txstate.TransactionOwner}
	tx.DesiredPlan.TUN = txstate.TUNDesiredState{InterfaceName: "podlaz0", Owner: xrayTunInboundOwner}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}

	target := original
	target.Profile.Server = "replacement.example.test"
	target.Handoff = api.HandoffReplacePodlaz
	if _, err := store.BeginOrResume(target); err != nil {
		t.Fatal(err)
	}
	manager := &XrayManager{RuntimeDir: runtimeDir}
	manager.state = xrayState{
		Connection:        "error (core exited)",
		Mode:              planner.ModeTun,
		ProfileID:         original.Profile.ID,
		ProfileName:       original.Profile.Name,
		RuntimeConfigPath: configPath,
		TransactionID:     tx.ID,
	}

	source, protected, err := manager.loadProtectedTunReplacementForRequest(store, target)
	if err != nil {
		t.Fatalf("load degraded protected replacement: %v", err)
	}
	if !protected || source.Kind != protectedTunReplacementDegraded || source.SessionID != state.SessionID {
		t.Fatalf("degraded source=%#v protected=%v", source, protected)
	}

	executor := &replacementRecoveryExecutor{exists: true, live: []string{"192.0.2.10"}}
	targetPlan := planner.TunPlan{
		Mode: planner.ModeTun,
		TunDevice: planner.TunDevicePlan{Name: "podlaz0"},
		ServerBypass: planner.ServerBypassPlan{Destination: "192.0.2.20/32"},
	}
	lifecycle, err := prepareProtectedTunReplacement(context.Background(), store, source, targetPlan, executor)
	if err != nil {
		t.Fatalf("prepare degraded protected replacement: %v", err)
	}
	if lifecycle == nil {
		t.Fatal("degraded protected replacement did not retain lifecycle authority")
	}
	if !reflect.DeepEqual(executor.live, []string{"192.0.2.10", "192.0.2.20"}) {
		t.Fatalf("live Privacy Envelope=%#v, want old+new bootstrap before cleanup", executor.live)
	}
	persisted, exists, err := store.Load()
	if err != nil || !exists || persisted.Protection == nil {
		t.Fatalf("load widened protection: exists=%v err=%v", exists, err)
	}
	if !reflect.DeepEqual(persisted.Protection.BootstrapIPv4, []string{"192.0.2.10", "192.0.2.20"}) {
		t.Fatalf("persisted widened bootstrap=%#v", persisted.Protection.BootstrapIPv4)
	}
}
