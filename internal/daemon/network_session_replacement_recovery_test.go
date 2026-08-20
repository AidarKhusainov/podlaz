package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
)

func TestReconcileProtectedReplacementCrashRestoresPreviousSession(t *testing.T) {
	tests := []struct {
		name             string
		persistedCurrent []string
		persistedPrev    []string
		state            networkSessionProtectionState
		live             []string
	}{
		{name: "before envelope widen", persistedCurrent: []string{"192.0.2.10"}, state: networkSessionProtectionArmed, live: []string{"192.0.2.10"}},
		{name: "widen committed", persistedCurrent: []string{"192.0.2.10", "192.0.2.20"}, state: networkSessionProtectionArmed, live: []string{"192.0.2.10", "192.0.2.20"}},
		{name: "narrow intent before nft replace", persistedCurrent: []string{"192.0.2.20"}, persistedPrev: []string{"192.0.2.10", "192.0.2.20"}, state: networkSessionProtectionArming, live: []string{"192.0.2.10", "192.0.2.20"}},
		{name: "narrow nft replace committed", persistedCurrent: []string{"192.0.2.20"}, persistedPrev: []string{"192.0.2.10", "192.0.2.20"}, state: networkSessionProtectionArming, live: []string{"192.0.2.20"}},
		{name: "target protection verified before session commit", persistedCurrent: []string{"192.0.2.20"}, state: networkSessionProtectionArmed, live: []string{"192.0.2.20"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := seededPrivacyReplacementState(t)
			state, _, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			protection := cloneNetworkSessionProtection(*state.Protection)
			protection.State = tt.state
			protection.BootstrapIPv4 = append([]string(nil), tt.persistedCurrent...)
			protection.PreviousBootstrapIPv4 = append([]string(nil), tt.persistedPrev...)
			if err := store.SetProtection(&protection); err != nil {
				t.Fatal(err)
			}
			executor := &replacementRecoveryExecutor{exists: true, live: append([]string(nil), tt.live...)}

			reconciled, protected, err := reconcileNetworkSessionProtection(context.Background(), store, executor)
			if err != nil {
				t.Fatalf("reconcile replacement crash: %v", err)
			}
			if !protected || reconciled.Replacement != nil {
				t.Fatalf("replacement crash did not converge to ordinary protected session: %#v", reconciled)
			}
			if reconciled.Request.Profile.ID != "profile-example" || api.NormalizeHandoffPolicy(reconciled.Request.Handoff) != api.HandoffBlock {
				t.Fatalf("previous request not restored: %#v", reconciled.Request)
			}
			if reconciled.Protection == nil || reconciled.Protection.State != networkSessionProtectionArmed || !reflect.DeepEqual(reconciled.Protection.BootstrapIPv4, []string{"192.0.2.10"}) {
				t.Fatalf("previous privacy authority not restored: %#v", reconciled.Protection)
			}
			if !reflect.DeepEqual(executor.live, []string{"192.0.2.10"}) {
				t.Fatalf("live barrier endpoints=%#v, want previous endpoint", executor.live)
			}
		})
	}
}

func TestFailedProtectedReplacementNarrowingRestoresUnionThenPreviousBarrier(t *testing.T) {
	store, replacementPlan := seededPrivacyReplacementState(t)
	executor := &replacementRecoveryExecutor{exists: true, live: []string{"192.0.2.10"}}
	lifecycle := privacyEnvelopeLifecycle{store: store, executor: executor}
	if err := lifecycle.PrepareReplacement(context.Background(), replacementPlan); err != nil {
		t.Fatalf("prepare replacement: %v", err)
	}
	if !reflect.DeepEqual(executor.live, []string{"192.0.2.10", "192.0.2.20"}) {
		t.Fatalf("prepared live endpoints=%#v", executor.live)
	}

	state, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	narrowing := cloneNetworkSessionProtection(*state.Protection)
	narrowing.State = networkSessionProtectionArming
	narrowing.PreviousBootstrapIPv4 = append([]string(nil), state.Protection.BootstrapIPv4...)
	narrowing.BootstrapIPv4 = []string{"192.0.2.20"}
	if err := store.SetProtection(&narrowing); err != nil {
		t.Fatal(err)
	}

	// This is the atomic Replace failure boundary: durable narrowing intent is
	// written, but nft keeps the previously verified union composition.
	if err := lifecycle.CleanupAfterFailedDataPlane(context.Background()); err != nil {
		t.Fatalf("cleanup failed replacement after narrow Replace failure: %v", err)
	}
	restored, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load restored session: exists=%v err=%v", exists, err)
	}
	if restored.Replacement != nil || restored.Request.Profile.ID != "profile-example" {
		t.Fatalf("previous session metadata not restored: %#v", restored)
	}
	if restored.Protection == nil || restored.Protection.State != networkSessionProtectionArmed || !reflect.DeepEqual(restored.Protection.BootstrapIPv4, []string{"192.0.2.10"}) {
		t.Fatalf("previous protection not restored: %#v", restored.Protection)
	}
	if !reflect.DeepEqual(executor.live, []string{"192.0.2.10"}) {
		t.Fatalf("live endpoints=%#v, want previous endpoint", executor.live)
	}
}

func TestReconcileProtectedReplacementCrashFailsClosedOnAmbiguousEnvelope(t *testing.T) {
	store, _ := seededPrivacyReplacementState(t)
	state, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	protection := cloneNetworkSessionProtection(*state.Protection)
	protection.State = networkSessionProtectionArmed
	protection.BootstrapIPv4 = []string{"192.0.2.10", "192.0.2.20"}
	if err := store.SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	executor := &replacementRecoveryExecutor{exists: true, live: []string{"203.0.113.77"}}

	_, protected, err := reconcileNetworkSessionProtection(context.Background(), store, executor)
	if err == nil || !protected {
		t.Fatalf("ambiguous replacement envelope must fail closed: protected=%v err=%v", protected, err)
	}
	if executor.replaceCalls != 0 || executor.applyCalls != 0 {
		t.Fatalf("ambiguous replacement envelope was mutated: replace=%d apply=%d", executor.replaceCalls, executor.applyCalls)
	}
	persisted, exists, loadErr := store.Load()
	if loadErr != nil || !exists || persisted.Replacement == nil {
		t.Fatalf("ambiguous replacement lost durable rollback authority: exists=%v state=%#v err=%v", exists, persisted, loadErr)
	}
}

type replacementRecoveryExecutor struct {
	exists       bool
	live         []string
	replaceCalls int
	applyCalls   int
}

func (e *replacementRecoveryExecutor) PrivacyEnvelopeTableExists(context.Context, string, string) (bool, error) {
	return e.exists, nil
}

func (e *replacementRecoveryExecutor) Exists(context.Context, netexecutor.PrivacyEnvelopePlan) (bool, error) {
	return e.exists, nil
}

func (e *replacementRecoveryExecutor) Apply(_ context.Context, plan netexecutor.PrivacyEnvelopePlan) error {
	e.applyCalls++
	e.exists = true
	e.live = privacyEnvelopeBootstrapRules(plan)
	return nil
}

func (e *replacementRecoveryExecutor) Verify(_ context.Context, plan netexecutor.PrivacyEnvelopePlan) error {
	want := privacyEnvelopeBootstrapRules(plan)
	if !e.exists || !reflect.DeepEqual(e.live, want) {
		return errors.New("synthetic exact composition mismatch")
	}
	return nil
}

func (e *replacementRecoveryExecutor) Replace(_ context.Context, from, to netexecutor.PrivacyEnvelopePlan) error {
	if err := e.Verify(context.Background(), from); err != nil {
		return err
	}
	e.replaceCalls++
	e.live = privacyEnvelopeBootstrapRules(to)
	return nil
}

func (e *replacementRecoveryExecutor) Remove(context.Context, netexecutor.PrivacyEnvelopePlan) error {
	e.exists = false
	e.live = nil
	return nil
}
