package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestPrivacyEnvelopeLifecyclePersistsAuthorityBeforeApplyAndMarksArmedAfterVerify(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}

	executor := &privacyEnvelopeExecutorStub{}
	executor.onApply = func(plan netexecutor.PrivacyEnvelopePlan) {
		state, exists, err := store.Load()
		if err != nil || !exists {
			t.Fatalf("load persisted protection during apply: exists=%v err=%v", exists, err)
		}
		if state.Protection == nil || state.Protection.State != networkSessionProtectionArming {
			t.Fatalf("privacy authority must be durable as arming before nft mutation: %#v", state.Protection)
		}
		if state.Protection.Family != plan.Family || state.Protection.Table != plan.Table {
			t.Fatalf("persisted authority does not match exact apply target: authority=%#v plan=%#v", state.Protection, plan)
		}
	}

	lifecycle := privacyEnvelopeLifecycle{store: store, executor: executor}
	plan := privacyLifecycleTunPlanForTest()
	if err := lifecycle.Arm(context.Background(), plan); err != nil {
		t.Fatalf("arm privacy envelope: %v", err)
	}
	state, _, err := store.Load()
	if err != nil {
		t.Fatalf("load state after apply: %v", err)
	}
	if state.Protection == nil || state.Protection.State != networkSessionProtectionArming {
		t.Fatalf("apply alone must not publish armed protection: %#v", state.Protection)
	}

	if err := lifecycle.Verify(context.Background()); err != nil {
		t.Fatalf("verify privacy envelope: %v", err)
	}
	state, _, err = store.Load()
	if err != nil {
		t.Fatalf("load state after verify: %v", err)
	}
	if state.Protection == nil || state.Protection.State != networkSessionProtectionArmed {
		t.Fatalf("exact verification must persist armed protection: %#v", state.Protection)
	}
	if !reflect.DeepEqual(executor.events, []string{"observe", "apply", "verify"}) {
		t.Fatalf("unexpected envelope lifecycle events: %#v", executor.events)
	}
}

func TestPrivacyEnvelopeLifecycleVerifyFailureKeepsArmingAuthority(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	executor := &privacyEnvelopeExecutorStub{verifyErr: errors.New("unexpected envelope composition")}
	lifecycle := privacyEnvelopeLifecycle{store: store, executor: executor}
	if err := lifecycle.Arm(context.Background(), privacyLifecycleTunPlanForTest()); err != nil {
		t.Fatalf("arm privacy envelope: %v", err)
	}
	if err := lifecycle.Verify(context.Background()); err == nil {
		t.Fatal("expected exact verification failure")
	}
	state, _, err := store.Load()
	if err != nil {
		t.Fatalf("load state after failed verify: %v", err)
	}
	if state.Protection == nil || state.Protection.State != networkSessionProtectionArming {
		t.Fatalf("failed verification must retain arming recovery authority: %#v", state.Protection)
	}
}

func TestPrivacyEnvelopeLifecycleRefusesDeleteWhenExactCompositionCannotBeProved(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	protection := testArmedPrivacyProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist protection: %v", err)
	}
	executor := &privacyEnvelopeExecutorStub{exists: true, verifyErr: errors.New("composition mismatch")}
	lifecycle := privacyEnvelopeLifecycle{store: store, executor: executor}

	if err := lifecycle.RemoveAfterDataPlaneCleanup(context.Background()); err == nil {
		t.Fatal("expected ambiguous envelope cleanup to fail closed")
	}
	if executor.removeCalls != 0 {
		t.Fatalf("ambiguous envelope must not be deleted, remove calls=%d", executor.removeCalls)
	}
	state, _, err := store.Load()
	if err != nil {
		t.Fatalf("load state after refused cleanup: %v", err)
	}
	if state.Protection == nil || state.Protection.State != networkSessionProtectionArmed {
		t.Fatalf("failed cleanup must retain exact recovery authority: %#v", state.Protection)
	}
}

func TestPrivacyEnvelopeLifecycleRemovesOnlyVerifiedExactEnvelopeAndClearsAuthorityLast(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	protection := testArmedPrivacyProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist protection: %v", err)
	}
	executor := &privacyEnvelopeExecutorStub{exists: true}
	executor.onRemove = func(netexecutor.PrivacyEnvelopePlan) {
		state, exists, err := store.Load()
		if err != nil || !exists {
			t.Fatalf("load state during removal: exists=%v err=%v", exists, err)
		}
		if state.Protection == nil || state.Protection.State != networkSessionProtectionRemoving {
			t.Fatalf("removal intent must be durable before nft delete: %#v", state.Protection)
		}
	}
	lifecycle := privacyEnvelopeLifecycle{store: store, executor: executor}

	if err := lifecycle.RemoveAfterDataPlaneCleanup(context.Background()); err != nil {
		t.Fatalf("remove privacy envelope: %v", err)
	}
	state, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load network session after envelope removal: exists=%v err=%v", exists, err)
	}
	if state.Protection != nil {
		t.Fatalf("protection authority must clear only after verified absence: %#v", state.Protection)
	}
	want := []string{"exists", "verify", "remove", "exists"}
	if !reflect.DeepEqual(executor.events, want) {
		t.Fatalf("cleanup ordering = %#v, want %#v", executor.events, want)
	}
}

func TestPrivacyEnvelopeLifecycleMissingEnvelopeClearsAuthorityWithoutMutation(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	protection := testArmedPrivacyProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist protection: %v", err)
	}
	executor := &privacyEnvelopeExecutorStub{exists: false}
	lifecycle := privacyEnvelopeLifecycle{store: store, executor: executor}
	if err := lifecycle.RemoveAfterDataPlaneCleanup(context.Background()); err != nil {
		t.Fatalf("clear missing privacy envelope: %v", err)
	}
	if executor.removeCalls != 0 || executor.verifyCalls != 0 {
		t.Fatalf("missing table needs no mutation or composition proof: verify=%d remove=%d", executor.verifyCalls, executor.removeCalls)
	}
	state, _, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Protection != nil {
		t.Fatalf("missing exact envelope should clear stale protection authority: %#v", state.Protection)
	}
}

func privacyLifecycleTunPlanForTest() planner.TunPlan {
	plan := transactionPlanForTest()
	plan.ServerBypass = planner.TunRoutePlan{
		Family:      "ipv4",
		Destination: "192.0.2.10/32",
		Table:       planner.MainRoutingTable,
		Interface:   "eth0",
		Gateway:     "192.0.2.1",
		Action:      "add",
	}
	return plan
}

func testArmedPrivacyProtection() networkSessionProtection {
	return networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}
}

type privacyEnvelopeExecutorStub struct {
	occupied    map[string]bool
	exists      bool
	verifyErr   error
	applyErr    error
	removeErr   error
	events      []string
	verifyCalls int
	removeCalls int
	onApply     func(netexecutor.PrivacyEnvelopePlan)
	onRemove    func(netexecutor.PrivacyEnvelopePlan)
}

func (e *privacyEnvelopeExecutorStub) PrivacyEnvelopeTableExists(_ context.Context, family, table string) (bool, error) {
	e.events = append(e.events, "observe")
	return e.occupied[family+"/"+table], nil
}

func (e *privacyEnvelopeExecutorStub) Apply(_ context.Context, plan netexecutor.PrivacyEnvelopePlan) error {
	e.events = append(e.events, "apply")
	if e.onApply != nil {
		e.onApply(plan)
	}
	return e.applyErr
}

func (e *privacyEnvelopeExecutorStub) Verify(_ context.Context, _ netexecutor.PrivacyEnvelopePlan) error {
	e.events = append(e.events, "verify")
	e.verifyCalls++
	return e.verifyErr
}

func (e *privacyEnvelopeExecutorStub) Exists(_ context.Context, _ netexecutor.PrivacyEnvelopePlan) (bool, error) {
	e.events = append(e.events, "exists")
	return e.exists, nil
}

func (e *privacyEnvelopeExecutorStub) Remove(_ context.Context, plan netexecutor.PrivacyEnvelopePlan) error {
	e.events = append(e.events, "remove")
	e.removeCalls++
	if e.onRemove != nil {
		e.onRemove(plan)
	}
	if e.removeErr == nil {
		e.exists = false
	}
	return e.removeErr
}
