package daemon

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestPrivacyEnvelopePrepareReplacementAddsNewEndpointBeforeDataPlaneReplacement(t *testing.T) {
	store, replacementPlan := seededPrivacyReplacementState(t)
	executor := newPrivacyEnvelopeReplacementExecutorStub()
	lifecycle := privacyEnvelopeLifecycle{store: store, executor: executor}

	if err := lifecycle.PrepareReplacement(context.Background(), replacementPlan); err != nil {
		t.Fatalf("prepare privacy replacement: %v", err)
	}
	if len(executor.replacements) != 1 {
		t.Fatalf("expected one atomic envelope replacement, got %d", len(executor.replacements))
	}
	got := executor.replacements[0]
	if !reflect.DeepEqual(privacyEnvelopeBootstrapRules(got.from), []string{"192.0.2.10"}) {
		t.Fatalf("replacement source endpoints = %#v", privacyEnvelopeBootstrapRules(got.from))
	}
	if !reflect.DeepEqual(privacyEnvelopeBootstrapRules(got.to), []string{"192.0.2.10", "192.0.2.20"}) {
		t.Fatalf("replacement union endpoints = %#v", privacyEnvelopeBootstrapRules(got.to))
	}
	state, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Protection == nil || state.Protection.State != networkSessionProtectionArmed {
		t.Fatalf("prepared replacement not armed: %#v", state.Protection)
	}
	if !reflect.DeepEqual(state.Protection.BootstrapIPv4, []string{"192.0.2.10", "192.0.2.20"}) {
		t.Fatalf("durable union endpoints = %#v", state.Protection.BootstrapIPv4)
	}
	if len(state.Protection.PreviousBootstrapIPv4) != 0 {
		t.Fatalf("verified union retained transient source: %#v", state.Protection.PreviousBootstrapIPv4)
	}
	if state.Replacement == nil {
		t.Fatal("generation replacement rollback authority cleared before new data plane commit")
	}
}

func TestPrivacyEnvelopeArmNarrowsPreparedReplacementAfterNewDataPlaneProof(t *testing.T) {
	store, replacementPlan := seededPrivacyReplacementState(t)
	executor := newPrivacyEnvelopeReplacementExecutorStub()
	lifecycle := privacyEnvelopeLifecycle{store: store, executor: executor}
	if err := lifecycle.PrepareReplacement(context.Background(), replacementPlan); err != nil {
		t.Fatal(err)
	}

	if err := lifecycle.Arm(context.Background(), replacementPlan); err != nil {
		t.Fatalf("narrow privacy envelope after initial new data-plane proof: %v", err)
	}
	state, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Protection == nil || state.Protection.State != networkSessionProtectionArming {
		t.Fatalf("narrowing transition must be durable before exact verify: %#v", state.Protection)
	}
	if !reflect.DeepEqual(state.Protection.BootstrapIPv4, []string{"192.0.2.20"}) ||
		!reflect.DeepEqual(state.Protection.PreviousBootstrapIPv4, []string{"192.0.2.10", "192.0.2.20"}) {
		t.Fatalf("narrowing authority = %#v", state.Protection)
	}
	if err := lifecycle.Verify(context.Background()); err != nil {
		t.Fatalf("verify narrowed privacy envelope: %v", err)
	}
	state, _, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Protection.State != networkSessionProtectionArmed || !reflect.DeepEqual(state.Protection.BootstrapIPv4, []string{"192.0.2.20"}) || len(state.Protection.PreviousBootstrapIPv4) != 0 {
		t.Fatalf("narrowed privacy envelope not committed: %#v", state.Protection)
	}
}

func TestPrivacyEnvelopeFailedReplacementRestoresPreviousBarrierAndSessionRequest(t *testing.T) {
	store, replacementPlan := seededPrivacyReplacementState(t)
	executor := newPrivacyEnvelopeReplacementExecutorStub()
	lifecycle := privacyEnvelopeLifecycle{store: store, executor: executor}
	if err := lifecycle.PrepareReplacement(context.Background(), replacementPlan); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Arm(context.Background(), replacementPlan); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := lifecycle.CleanupAfterFailedDataPlane(context.Background()); err != nil {
		t.Fatalf("rollback failed protected replacement: %v", err)
	}
	state, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load restored session: exists=%v err=%v", exists, err)
	}
	if state.Replacement != nil {
		t.Fatalf("failed replacement retained transition metadata: %#v", state.Replacement)
	}
	if api.NormalizeHandoffPolicy(state.Request.Handoff) != api.HandoffBlock || state.Request.Profile.ID != "profile-example" {
		t.Fatalf("previous request not restored: %#v", state.Request)
	}
	if state.Protection == nil || state.Protection.State != networkSessionProtectionArmed || !reflect.DeepEqual(state.Protection.BootstrapIPv4, []string{"192.0.2.10"}) {
		t.Fatalf("previous barrier not restored: %#v", state.Protection)
	}
	last := executor.replacements[len(executor.replacements)-1]
	if !reflect.DeepEqual(privacyEnvelopeBootstrapRules(last.to), []string{"192.0.2.10"}) {
		t.Fatalf("rollback replacement target = %#v", privacyEnvelopeBootstrapRules(last.to))
	}
}

func seededPrivacyReplacementState(t *testing.T) (networkSessionStateStore, planner.TunPlan) {
	t.Helper()
	store := newNetworkSessionStateStore(t.TempDir(), fixedBootID("boot-a"))
	original := testContinuationRequest()
	if _, err := store.BeginOrResume(original); err != nil {
		t.Fatal(err)
	}
	protection := testArmedPrivacyProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	replacement := original
	replacement.Handoff = api.HandoffReplacePodlaz
	replacement.Profile.ID = "profile-replacement"
	replacement.Profile.Name = "Replacement profile"
	replacement.Profile.Server = "replacement.example.test"
	if _, err := store.BeginOrResume(replacement); err != nil {
		t.Fatal(err)
	}
	plan := privacyLifecycleTunPlanForTest()
	plan.ProfileID = replacement.Profile.ID
	plan.ProfileName = replacement.Profile.Name
	plan.ServerBypass.Destination = "192.0.2.20/32"
	return store, plan
}

func privacyEnvelopeBootstrapRules(plan netexecutor.PrivacyEnvelopePlan) []string {
	var endpoints []string
	for _, rule := range plan.Rules {
		const prefix = "ip daddr "
		if strings.HasPrefix(rule.Expr, prefix) {
			endpoints = append(endpoints, strings.TrimPrefix(rule.Expr, prefix))
		}
	}
	return endpoints
}

type privacyEnvelopeReplacementExecutorStub struct {
	privacyEnvelopeExecutorStub
	replacements []privacyEnvelopeReplacementCall
}

type privacyEnvelopeReplacementCall struct {
	from netexecutor.PrivacyEnvelopePlan
	to   netexecutor.PrivacyEnvelopePlan
}

func newPrivacyEnvelopeReplacementExecutorStub() *privacyEnvelopeReplacementExecutorStub {
	return &privacyEnvelopeReplacementExecutorStub{
		privacyEnvelopeExecutorStub: privacyEnvelopeExecutorStub{exists: true},
	}
}

func (e *privacyEnvelopeReplacementExecutorStub) Replace(_ context.Context, from, to netexecutor.PrivacyEnvelopePlan) error {
	e.replacements = append(e.replacements, privacyEnvelopeReplacementCall{from: from, to: to})
	e.exists = true
	return nil
}

func (e *privacyEnvelopeExecutorStub) Replace(_ context.Context, _, _ netexecutor.PrivacyEnvelopePlan) error {
	e.events = append(e.events, "replace")
	return nil
}
