package daemon

import (
	"context"
	"os/exec"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestPersistNetworkSessionReplayFailureStoresPrivateBoundedApplyCause(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	state, exists, err := continuation.stateStore().BeginRecoveryAttempt()
	if err != nil || !exists {
		t.Fatalf("begin recovery attempt: exists=%v err=%v", exists, err)
	}

	_, applyErr := (netexecutor.ResolvedDNSExecutor{
		Runner:        unavailableReplayApplyRunner{},
		ApplyAttempts: 1,
	}).Apply(context.Background(), planner.TunDNSPlan{
		Backend:    planner.DNSBackendSystemdResolved,
		TargetLink: "podlaz0",
		Servers:    []string{"192.0.2.53"},
		Action:     planner.DNSActionConfigure,
		Rollback:   planner.DNSRollbackRestore,
	})
	if applyErr == nil {
		t.Fatal("expected command-unavailable DNS apply failure")
	}
	applyErr = withTunFailurePhase("network-apply", "tun-cause", "completed", applyErr)

	if err := persistNetworkSessionReplayFailure(context.Background(), continuation, state, false, applyErr); err == nil {
		t.Fatal("expected persisted replay failure")
	}
	record := loadReplayEvidenceDiagnostic(t, continuation)
	if record.NetworkApplyFailureCause != netexecutor.ApplyFailureCauseCommandUnavailable {
		t.Fatalf("top-level apply failure cause=%q want=%q", record.NetworkApplyFailureCause, netexecutor.ApplyFailureCauseCommandUnavailable)
	}
	if record.Current == nil || record.Current.NetworkApplyFailureCause != netexecutor.ApplyFailureCauseCommandUnavailable {
		t.Fatalf("current private apply failure cause missing: %#v", record.Current)
	}
	if record.Current.ReplayDisposition != networkSessionReplayDispositionIncomplete {
		t.Fatalf("bounded cause must not create terminality: replay disposition=%q", record.Current.ReplayDisposition)
	}
}

type unavailableReplayApplyRunner struct{}

func (unavailableReplayApplyRunner) Run(_ context.Context, name string, _ ...string) (netexecutor.CommandResult, error) {
	return netexecutor.CommandResult{ExitCode: -1}, &exec.Error{Name: name, Err: exec.ErrNotFound}
}
