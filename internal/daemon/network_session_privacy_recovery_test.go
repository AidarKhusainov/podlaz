package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestReconcileNetworkSessionProtectionStateMatrix(t *testing.T) {
	tests := []struct {
		name          string
		state         networkSessionProtectionState
		tableExists   bool
		wantEvents    []string
		wantFinal     networkSessionProtectionState
		wantApplyCall bool
	}{
		{name: "arming missing recreates then verifies", state: networkSessionProtectionArming, tableExists: false, wantEvents: []string{"exists", "apply", "verify"}, wantFinal: networkSessionProtectionArmed, wantApplyCall: true},
		{name: "arming present verifies then marks armed", state: networkSessionProtectionArming, tableExists: true, wantEvents: []string{"exists", "verify"}, wantFinal: networkSessionProtectionArmed},
		{name: "armed present verifies idempotently", state: networkSessionProtectionArmed, tableExists: true, wantEvents: []string{"exists", "verify"}, wantFinal: networkSessionProtectionArmed},
		{name: "armed missing recreates exact composition", state: networkSessionProtectionArmed, tableExists: false, wantEvents: []string{"exists", "apply", "verify"}, wantFinal: networkSessionProtectionArmed, wantApplyCall: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
			if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
				t.Fatalf("begin session: %v", err)
			}
			protection := testArmedPrivacyProtection()
			protection.State = tt.state
			if err := store.SetProtection(&protection); err != nil {
				t.Fatalf("persist protection: %v", err)
			}
			executor := &privacyEnvelopeExecutorStub{exists: tt.tableExists}
			executor.onApply = func(netexecutor.PrivacyEnvelopePlan) { executor.exists = true }

			state, protected, err := reconcileNetworkSessionProtection(context.Background(), store, executor)
			if err != nil {
				t.Fatalf("reconcile protection: %v", err)
			}
			if !protected || state.Protection == nil || state.Protection.State != tt.wantFinal {
				t.Fatalf("reconciled state = %#v protected=%v, want %s", state.Protection, protected, tt.wantFinal)
			}
			if !reflect.DeepEqual(executor.events, tt.wantEvents) {
				t.Fatalf("reconcile events = %#v, want %#v", executor.events, tt.wantEvents)
			}
		})
	}
}

func TestReconcileNetworkSessionProtectionFailsClosedOnUnexpectedLiveComposition(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin session: %v", err)
	}
	protection := testArmedPrivacyProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist protection: %v", err)
	}
	executor := &privacyEnvelopeExecutorStub{exists: true, verifyErr: errors.New("unexpected live composition")}

	_, protected, err := reconcileNetworkSessionProtection(context.Background(), store, executor)
	if err == nil || !protected {
		t.Fatalf("expected fail-closed reconciliation, protected=%v err=%v", protected, err)
	}
	if executor.removeCalls != 0 || reflect.DeepEqual(executor.events, []string{"exists", "verify", "apply"}) {
		t.Fatalf("ambiguous live table must not be overwritten or removed: %#v", executor.events)
	}
	if !reflect.DeepEqual(executor.events, []string{"exists", "verify"}) {
		t.Fatalf("unexpected fail-closed events: %#v", executor.events)
	}
	state, exists, loadErr := store.Load()
	if loadErr != nil || !exists || state.Protection == nil {
		t.Fatalf("fail-closed reconciliation lost authority: exists=%v state=%#v err=%v", exists, state, loadErr)
	}
}

func TestReconcileNetworkSessionProtectionRetainsAuthorityWhenRecreateFails(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin session: %v", err)
	}
	protection := testArmedPrivacyProtection()
	protection.State = networkSessionProtectionArming
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist protection: %v", err)
	}
	executor := &privacyEnvelopeExecutorStub{exists: false, applyErr: errors.New("nft apply failed")}

	_, protected, err := reconcileNetworkSessionProtection(context.Background(), store, executor)
	if err == nil || !protected {
		t.Fatalf("expected recreate failure with retained protection, protected=%v err=%v", protected, err)
	}
	state, exists, loadErr := store.Load()
	if loadErr != nil || !exists || state.Protection == nil || state.Protection.State != networkSessionProtectionArming {
		t.Fatalf("recreate failure lost arming authority: exists=%v state=%#v err=%v", exists, state, loadErr)
	}
}

func TestNetworkSessionBootstrapServerUsesPersistedExactEndpointOnlyForMatchingResumeSession(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin session: %v", err)
	}
	protection := testArmedPrivacyProtection()
	protection.BootstrapIPv4 = []string{"192.0.2.44"}
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist protection: %v", err)
	}

	server, ok, err := networkSessionBootstrapServer(store, "profile-example")
	if err != nil || !ok || server != "192.0.2.44" {
		t.Fatalf("bootstrap override = %q ok=%v err=%v, want exact persisted endpoint", server, ok, err)
	}
	if server, ok, err := networkSessionBootstrapServer(store, "different-profile"); err != nil || ok || server != "" {
		t.Fatalf("mismatched profile received bootstrap override: %q ok=%v err=%v", server, ok, err)
	}
	if err := store.SetIntent(networkSessionIntentTerminal); err != nil {
		t.Fatalf("set terminal intent: %v", err)
	}
	if server, ok, err := networkSessionBootstrapServer(store, "profile-example"); err != nil || ok || server != "" {
		t.Fatalf("terminal session received resume bootstrap override: %q ok=%v err=%v", server, ok, err)
	}
}
