package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFullTunnelTransactionRunnerPostApplyRollbackUsesPersistedAppliedSteps(t *testing.T) {
	errConnectivity := errors.New("connectivity probe failed")
	errCommit := errors.New("commit failed after apply")

	tests := []struct {
		name      string
		configure func(*fullTunnelTransactionRunner)
		wantErr   error
	}{
		{
			name: "connectivity verification failure",
			configure: func(r *fullTunnelTransactionRunner) {
				r.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
					return errConnectivity
				}
			},
			wantErr: errConnectivity,
		},
		{
			name: "core exited before commit",
			configure: func(r *fullTunnelTransactionRunner) {
				r.commitActiveState = func(txstate.TransactionStore, string, fullTunnelCoreHandle, xrayState) error {
					return errFullTunnelCoreExitedBeforeCommit
				}
			},
			wantErr: errFullTunnelCoreExitedBeforeCommit,
		},
		{
			name: "commit failure",
			configure: func(r *fullTunnelTransactionRunner) {
				r.commitActiveState = func(txstate.TransactionStore, string, fullTunnelCoreHandle, xrayState) error {
					return errCommit
				}
			},
			wantErr: errCommit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			executor := &postApplyRollbackPlanExecutor{}
			runner := postApplyRollbackRunnerForTest(runtimeDir, executor)
			tt.configure(runner)

			_, err := runner.run(context.Background())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
			if len(executor.rollbackPlans) != 1 {
				t.Fatalf("expected one rollback plan, got %#v", executor.rollbackPlans)
			}
			requirePostApplyRollbackPlanUsesPersistedSteps(t, executor.rollbackPlans[0])
		})
	}
}

func postApplyRollbackRunnerForTest(runtimeDir string, executor *postApplyRollbackPlanExecutor) *fullTunnelTransactionRunner {
	return &fullTunnelTransactionRunner{
		runtimeDir: runtimeDir,
		profile:    profile.Profile{ID: "test-profile", Name: "Test Profile"},
		plan:       transactionPlanWithServerBypassForTest(),
		corePlan: tunCoreRuntimePlan{
			RuntimeConfigPath: filepath.Join(runtimeDir, generatedDirName, generatedXrayName),
			Status:            "test TUN core runtime",
		},
		executor: executor,
		now:      fixedClock(),
		startCore: func(context.Context) (fullTunnelCoreHandle, error) {
			return fullTunnelCoreHandle{done: make(chan struct{})}, nil
		},
		verifyCoreStarted: func(<-chan struct{}) error { return nil },
		verifyConnectivity: func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
			return nil
		},
	}
}

type postApplyRollbackPlanExecutor struct {
	rollbackPlans []planner.TunPlan
}

func (e *postApplyRollbackPlanExecutor) Apply(_ context.Context, plan planner.TunPlan) ([]netexecutor.Step, error) {
	return []netexecutor.Step{
		{Kind: "tun-device", Target: plan.TunDevice.Name, Owner: netexecutor.OwnerTunDevice},
		{Kind: "route", Target: routeTarget(plan.Routes[0]), Owner: netexecutor.OwnerRoute},
		{Kind: "policy-rule", Target: policyRuleTarget(plan.PolicyRules[0]), Owner: netexecutor.OwnerPolicyRule},
	}, nil
}

func (e *postApplyRollbackPlanExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *postApplyRollbackPlanExecutor) Rollback(_ context.Context, plan planner.TunPlan) error {
	e.rollbackPlans = append(e.rollbackPlans, plan)
	return nil
}

func requirePostApplyRollbackPlanUsesPersistedSteps(t *testing.T, plan planner.TunPlan) {
	t.Helper()

	if plan.TunDevice.Name != "podlaz0" {
		t.Fatalf("expected persisted TUN rollback target, got %#v", plan.TunDevice)
	}
	if !containsRollbackRoute(plan.Routes, planner.TunRoutingTable, "default") {
		t.Fatalf("expected persisted default route rollback target, got %#v", plan.Routes)
	}
	if !containsRollbackPolicyRule(plan.PolicyRules, planner.TunRulePriority, planner.TunRoutingTable, planner.IPv4DefaultSelector) {
		t.Fatalf("expected persisted default policy-rule rollback target, got %#v", plan.PolicyRules)
	}
	if containsRollbackRoute(plan.Routes, planner.MainRoutingTable, "203.0.113.10/32") {
		t.Fatalf("pre-existing server-bypass route must not be rolled back: %#v", plan.Routes)
	}
	if containsRollbackPolicyRule(plan.PolicyRules, planner.ServerRulePriority, planner.MainRoutingTable, "to 203.0.113.10/32") {
		t.Fatalf("pre-existing server-bypass policy rule must not be rolled back: %#v", plan.PolicyRules)
	}
}

func containsRollbackRoute(routes []planner.TunRoutePlan, table, destination string) bool {
	for _, route := range routes {
		if route.Table == table && route.Destination == destination {
			return true
		}
	}
	return false
}

func containsRollbackPolicyRule(rules []planner.TunPolicyRulePlan, priority int, table, selector string) bool {
	for _, rule := range rules {
		if rule.Priority == priority && rule.Table == table && rule.Selector == selector {
			return true
		}
	}
	return false
}
