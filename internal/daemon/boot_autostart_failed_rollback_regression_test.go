package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type bootAutostartFailedRollbackExecutor struct {
	rollbackErr error
}

func (e *bootAutostartFailedRollbackExecutor) Apply(_ context.Context, plan planner.TunPlan) ([]netexecutor.Step, error) {
	return []netexecutor.Step{
		{Kind: "tun-device", Target: plan.TunDevice.Name, Owner: netexecutor.OwnerTunDevice},
		{Kind: "route", Target: routeTarget(plan.Routes[0]), Owner: netexecutor.OwnerRoute},
		{Kind: "policy-rule", Target: policyRuleTarget(plan.PolicyRules[0]), Owner: netexecutor.OwnerPolicyRule},
	}, nil
}

func (*bootAutostartFailedRollbackExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *bootAutostartFailedRollbackExecutor) Rollback(context.Context, planner.TunPlan) error {
	return e.rollbackErr
}

type bootAutostartFullTunnelFailureLifecycle struct {
	runner *fullTunnelTransactionRunner
}

func (l bootAutostartFullTunnelFailureLifecycle) Connect(ctx context.Context, _ api.ConnectRequest) (api.LifecycleResponse, error) {
	_, err := l.runner.run(ctx)
	return api.LifecycleResponse{}, err
}

func (bootAutostartFullTunnelFailureLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

func TestBootAutostartFailedTunRollbackBeforePrivacyEnvelopeStaysInProgress(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}

	rollbackErr := errors.New("synthetic exact rollback failure")
	executor := &bootAutostartFailedRollbackExecutor{rollbackErr: rollbackErr}
	runner := postApplyRollbackRunnerForTest(continuation.runtimeDir, &postApplyRollbackPlanExecutor{})
	runner.executor = executor
	runner.stopCore = func(fullTunnelCoreHandle) error { return nil }
	runner.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
		return errors.New("synthetic connectivity verification failure")
	}

	exactRecoveryObserved := false
	continuation.recoverExact = func(_ context.Context, runtimeDir string) api.RecoveryResponse {
		summaries, warnings := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Scan()
		if len(warnings) != 0 {
			t.Fatalf("scan exact transaction warnings = %v", warnings)
		}
		if len(summaries) == 0 {
			t.Fatal("failed TUN rollback left no exact transaction authority")
		}
		exactRecoveryObserved = true
		return api.RecoveryResponse{
			Mode: "execute",
			Warnings: []api.RecoveryWarning{{
				Target:  "exact TUN transaction",
				Message: "synthetic cleanup remains incomplete",
			}},
		}
	}

	result, err := runBootAutostartStartup(
		context.Background(),
		manifestStore,
		attemptStore,
		continuation,
		bootAutostartFullTunnelFailureLifecycle{runner: runner},
		func(context.Context) (bool, error) { return false, nil },
	)
	if err == nil || !exactRecoveryObserved {
		t.Fatalf("failed rollback startup = result %q err %v exactRecoveryObserved=%v", result, err, exactRecoveryObserved)
	}
	if result == bootAutostartStartupTerminal {
		t.Fatalf("failed rollback was consumed as terminal: %v", err)
	}
	attempt, exists, loadErr := attemptStore.LoadCurrent()
	if loadErr != nil || !exists || attempt.State != bootAutostartAttemptInProgress {
		t.Fatalf("attempt after failed rollback = %+v exists=%v err=%v", attempt, exists, loadErr)
	}
	state, exists, loadErr := continuation.stateStore().Load()
	if loadErr != nil || !exists || state.Intent != networkSessionIntentTerminal {
		t.Fatalf("terminal cleanup authority after failed rollback = %+v exists=%v err=%v", state, exists, loadErr)
	}
}
