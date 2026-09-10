package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestFullTunnelRunnerPublishesTerminalReplaySemanticsAfterCompletedNetworkRollback(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.executor.applyErr = errRunnerApplyFailed

	_, err := h.runner().run(context.Background())
	if err == nil {
		t.Fatal("expected full-tunnel apply failure")
	}
	disposition, mutation := classifyNetworkSessionReplayFailure(context.Background(), err)
	if disposition != networkSessionReplayDispositionTerminal || mutation != networkSessionCandidateMutationRolledBack {
		t.Fatalf("production replay semantics=(%q,%q), want=(terminal,rolled-back)", disposition, mutation)
	}
}

func TestFullTunnelRunnerKeepsConnectivityFailureIncompleteAfterCompletedRollback(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.verifyConnectivityErr = newTunVerificationError(
		"dns",
		"DNS through the tunnel did not resolve example.com before timeout",
		errRunnerConnectivityFailed,
	)

	_, err := h.runner().run(context.Background())
	if err == nil {
		t.Fatal("expected connectivity verification failure")
	}
	disposition, mutation := classifyNetworkSessionReplayFailure(context.Background(), err)
	if disposition != networkSessionReplayDispositionIncomplete || mutation != networkSessionCandidateMutationUnresolved {
		t.Fatalf("connectivity replay semantics=(%q,%q), want=(incomplete,unresolved)", disposition, mutation)
	}
}

func TestFullTunnelRunnerKeepsFailedRollbackIncomplete(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.executor.applyErr = errRunnerApplyFailed
	runner := h.runner()
	runner.rollbackTransaction = func(context.Context, string, planner.TunPlan, tunPlanExecutor, tunRollbackChildStopper) error {
		return errors.New("rollback remains incomplete")
	}

	_, err := runner.run(context.Background())
	if err == nil {
		t.Fatal("expected full-tunnel rollback failure")
	}
	disposition, mutation := classifyNetworkSessionReplayFailure(context.Background(), err)
	if disposition != networkSessionReplayDispositionIncomplete || mutation != networkSessionCandidateMutationUnresolved {
		t.Fatalf("failed rollback replay semantics=(%q,%q), want=(incomplete,unresolved)", disposition, mutation)
	}
}

func TestNetworkSessionReplayClassificationTreatsParentDeadlineAsInterrupted(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	cause := withNetworkSessionReplaySemantics(
		networkSessionReplayDispositionTerminal,
		networkSessionCandidateMutationRolledBack,
		errRunnerApplyFailed,
	)

	disposition, mutation := classifyNetworkSessionReplayFailure(ctx, cause)
	if disposition != networkSessionReplayDispositionInterrupted || mutation != networkSessionCandidateMutationUnresolved {
		t.Fatalf("deadline replay semantics=(%q,%q), want=(interrupted,unresolved)", disposition, mutation)
	}
}
