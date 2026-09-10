package daemon

import (
	"context"
	"testing"
	"time"
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
