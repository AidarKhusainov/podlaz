package daemon

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFullTunnelRunnerPublishesOnlyAfterEnvelopeAndPostArmConnectivityVerification(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	var events []string
	h.onNetworkApplied = func() { events = append(events, "network-apply-verify") }
	runner := h.runner()
	runner.requirePrivacyEnvelope = true
	runner.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
		h.connectivityVerified++
		events = append(events, fmt.Sprintf("connectivity-%d", h.connectivityVerified))
		return nil
	}
	runner.armPrivacyEnvelope = func(context.Context, planner.TunPlan) error {
		events = append(events, "envelope-arm")
		return nil
	}
	runner.verifyPrivacyEnvelope = func(context.Context) error {
		events = append(events, "envelope-verify")
		return nil
	}
	runner.cleanupPrivacyEnvelope = func(context.Context) error { return nil }
	originalCommit := runner.commitActiveState
	runner.commitActiveState = func(store txstate.TransactionStore, transactionID string, core fullTunnelCoreHandle, active xrayState) error {
		events = append(events, "commit")
		return originalCommit(store, transactionID, core, active)
	}

	if _, err := runner.run(context.Background()); err != nil {
		t.Fatalf("run protected full-tunnel transaction: %v", err)
	}
	want := []string{"network-apply-verify", "connectivity-1", "envelope-arm", "envelope-verify", "connectivity-2", "commit"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("protected publication ordering = %#v, want %#v", events, want)
	}
}

func TestFullTunnelRunnerPostEnvelopeFailureRollsBackDataPlaneBeforeEnvelopeCleanup(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	var events []string
	h.onRollback = func() { events = append(events, "data-plane-rollback") }
	runner := h.runner()
	runner.requirePrivacyEnvelope = true
	verification := 0
	runner.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
		verification++
		if verification == 2 {
			return errRunnerConnectivityFailed
		}
		return nil
	}
	runner.armPrivacyEnvelope = func(context.Context, planner.TunPlan) error { return nil }
	runner.verifyPrivacyEnvelope = func(context.Context) error { return nil }
	runner.cleanupPrivacyEnvelope = func(context.Context) error {
		events = append(events, "envelope-cleanup")
		return nil
	}

	_, err := runner.run(context.Background())
	if !errors.Is(err, errRunnerConnectivityFailed) {
		t.Fatalf("expected post-envelope connectivity failure, got %v", err)
	}
	want := []string{"data-plane-rollback", "envelope-cleanup"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("failure cleanup ordering = %#v, want %#v", events, want)
	}
	if h.commitCalled != 0 {
		t.Fatalf("failed post-envelope verification must not publish active state, commit calls=%d", h.commitCalled)
	}
}

func TestFullTunnelRunnerKeepsEnvelopeWhenDataPlaneRollbackFails(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	runner := h.runner()
	runner.requirePrivacyEnvelope = true
	verification := 0
	runner.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
		verification++
		if verification == 2 {
			return errRunnerConnectivityFailed
		}
		return nil
	}
	runner.armPrivacyEnvelope = func(context.Context, planner.TunPlan) error { return nil }
	runner.verifyPrivacyEnvelope = func(context.Context) error { return nil }
	cleanupCalls := 0
	runner.cleanupPrivacyEnvelope = func(context.Context) error {
		cleanupCalls++
		return nil
	}
	runner.rollbackTransaction = func(context.Context, string, planner.TunPlan, tunPlanExecutor, tunRollbackChildStopper) error {
		return errors.New("injected data-plane rollback failure")
	}

	_, err := runner.run(context.Background())
	if err == nil || !errors.Is(err, errRunnerConnectivityFailed) {
		t.Fatalf("expected protected failure with rollback error, got %v", err)
	}
	if cleanupCalls != 0 {
		t.Fatalf("privacy envelope must remain armed while data-plane cleanup is incomplete, cleanup calls=%d", cleanupCalls)
	}
}

func TestFullTunnelRunnerEnvelopeVerificationFailureCleansAfterDataPlaneRollback(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	var events []string
	h.onRollback = func() { events = append(events, "data-plane-rollback") }
	runner := h.runner()
	runner.requirePrivacyEnvelope = true
	envelopeVerifyErr := errors.New("envelope verify failed")
	runner.armPrivacyEnvelope = func(context.Context, planner.TunPlan) error { return nil }
	runner.verifyPrivacyEnvelope = func(context.Context) error { return envelopeVerifyErr }
	runner.cleanupPrivacyEnvelope = func(context.Context) error {
		events = append(events, "envelope-cleanup")
		return nil
	}

	_, err := runner.run(context.Background())
	if !errors.Is(err, envelopeVerifyErr) {
		t.Fatalf("expected envelope verification failure, got %v", err)
	}
	want := []string{"data-plane-rollback", "envelope-cleanup"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("envelope verification cleanup ordering = %#v, want %#v", events, want)
	}
}

func TestFullTunnelRunnerProtectedModeRejectsPartialEnvelopeHooksBeforeMutation(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	runner := h.runner()
	runner.requirePrivacyEnvelope = true
	runner.armPrivacyEnvelope = func(context.Context, planner.TunPlan) error { return nil }

	_, err := runner.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "complete Privacy Envelope lifecycle hooks") {
		t.Fatalf("expected privacy lifecycle preflight failure, got %v", err)
	}
	if h.coreStarted != 0 || len(h.executor.calls) != 0 {
		t.Fatalf("incomplete privacy lifecycle must fail before mutation: core=%d executor=%#v", h.coreStarted, h.executor.calls)
	}
}
