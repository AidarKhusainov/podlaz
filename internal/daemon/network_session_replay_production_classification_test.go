package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestFullTunnelRunnerKeepsUntypedApplyFailureIncompleteAfterCompletedRollback(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.executor.applyErr = errRunnerApplyFailed

	_, err := h.runner().run(context.Background())
	if err == nil {
		t.Fatal("expected full-tunnel apply failure")
	}
	disposition, mutation := classifyNetworkSessionReplayFailure(context.Background(), err)
	if disposition != networkSessionReplayDispositionIncomplete || mutation != networkSessionCandidateMutationRolledBack {
		t.Fatalf("production replay semantics=(%q,%q), want=(incomplete,rolled-back)", disposition, mutation)
	}
}

func TestFullTunnelRunnerPreservesTypedTerminalDispositionAfterCompletedRollback(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.executor.applyErr = withNetworkSessionReplaySemantics(
		networkSessionReplayDispositionTerminal,
		networkSessionCandidateMutationUnresolved,
		errRunnerApplyFailed,
	)

	_, err := h.runner().run(context.Background())
	if err == nil {
		t.Fatal("expected typed full-tunnel apply failure")
	}
	disposition, mutation := classifyNetworkSessionReplayFailure(context.Background(), err)
	if disposition != networkSessionReplayDispositionTerminal || mutation != networkSessionCandidateMutationRolledBack {
		t.Fatalf("typed production replay semantics=(%q,%q), want=(terminal,rolled-back)", disposition, mutation)
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
	if disposition != networkSessionReplayDispositionIncomplete || mutation != networkSessionCandidateMutationRolledBack {
		t.Fatalf("connectivity replay semantics=(%q,%q), want=(incomplete,rolled-back)", disposition, mutation)
	}
}

func TestFullTunnelRunnerKeepsFailedRollbackIncomplete(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	runner := h.runner()
	runner.executor = rollbackFailingApplyExecutor{
		applyErr:    errRunnerApplyFailed,
		rollbackErr: errors.New("rollback remains incomplete"),
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

func TestNetworkSessionReplayClassificationTreatsRuntimeUnavailableAsTerminalNotOpened(t *testing.T) {
	err := newRuntimeUnavailableError("Xray", "packaged runtime is unavailable")
	disposition, mutation := classifyNetworkSessionReplayFailure(context.Background(), err)
	if disposition != networkSessionReplayDispositionTerminal || mutation != networkSessionCandidateMutationNotOpened {
		t.Fatalf("runtime-unavailable replay semantics=(%q,%q), want=(terminal,not-opened)", disposition, mutation)
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

type rollbackFailingApplyExecutor struct {
	applyErr    error
	rollbackErr error
}

func (e rollbackFailingApplyExecutor) Apply(_ context.Context, _ planner.TunPlan) ([]netexecutor.Step, error) {
	return []netexecutor.Step{{Kind: "route", Target: "podlaz default", Owner: netexecutor.OwnerRoute}}, e.applyErr
}

func (rollbackFailingApplyExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e rollbackFailingApplyExecutor) Rollback(context.Context, planner.TunPlan) error {
	return e.rollbackErr
}
