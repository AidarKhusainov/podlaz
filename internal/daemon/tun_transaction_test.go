package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunTransactionWaitsForExplicitCommitAfterApplyAndVerify(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &recordingTunExecutor{}
	result, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err != nil {
		t.Fatalf("run TUN transaction failed: %v", err)
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if tx.State != txstate.TransactionVerifying {
		t.Fatalf("expected verifying before core verification, got %s", tx.State)
	}
	if strings.Join(executor.calls, ",") != "apply,verify" {
		t.Fatalf("unexpected executor calls: %#v", executor.calls)
	}
	if err := commitTunTransaction(result.Store, result.TransactionID); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	tx, _, err = (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(result.TransactionID)
	if err != nil {
		t.Fatalf("reload transaction: %v", err)
	}
	if tx.State != txstate.TransactionCommitted {
		t.Fatalf("expected committed transaction after explicit commit, got %s", tx.State)
	}
}

func TestTunTransactionDoesNotPersistPreApplyRollbackOwnership(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &preApplyInspectingTunExecutor{t: t, runtimeDir: runtimeDir}
	_, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanWithServerBypassForTest(), executor, fixedClock())
	if err != nil {
		t.Fatalf("run TUN transaction failed: %v", err)
	}
	if !executor.inspected {
		t.Fatal("expected executor to inspect the crash-window transaction state")
	}
}

func TestTunTransactionRecordsGeneratedConfigRollbackBeforePreflight(t *testing.T) {
	runtimeDir := t.TempDir()
	clock := fixedClock()
	result, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), clock)
	if err != nil {
		t.Fatalf("begin TUN transaction: %v", err)
	}
	runtimeConfigPath := "/run/podlaz/generated/xray.json"
	if err := saveGeneratedConfigRollbackMetadata(result.Store, result.TransactionID, runtimeConfigPath, clock()); err != nil {
		t.Fatalf("save generated config rollback metadata: %v", err)
	}
	tx, _, err := result.Store.Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if tx.DesiredPlan.Core.RuntimeConfigPath != runtimeConfigPath || tx.DesiredPlan.Core.ProcessLabel != "xray" {
		t.Fatalf("expected core desired plan before preflight, got %#v", tx.DesiredPlan.Core)
	}
	if len(tx.Rollback.GeneratedConfigs) != 1 || tx.Rollback.GeneratedConfigs[0].Path != runtimeConfigPath {
		t.Fatalf("expected generated config rollback metadata before preflight, got %#v", tx.Rollback.GeneratedConfigs)
	}
	if tx.Health.Status != "core-preflight-planned" {
		t.Fatalf("expected preflight health marker, got %#v", tx.Health)
	}
}

func TestRollbackTunTransactionStopsChildProcessesAfterExecutorRollback(t *testing.T) {
	runtimeDir := t.TempDir()
	clock := fixedClock()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: clock}
	tx := txstate.NewTransaction("tun-order-test", "test-profile", planner.ModeTun, clock())
	tx.Rollback.ChildProcesses = []txstate.ChildProcessRollback{{PID: 12345, Label: "xray", Owner: txstate.TransactionOwner}}
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save transaction: %v", err)
	}

	var order []string
	oldStop := stopRollbackChildProcesses
	stopRollbackChildProcesses = func(txstate.Transaction) error {
		order = append(order, "stop-child")
		return nil
	}
	defer func() { stopRollbackChildProcesses = oldStop }()

	executor := &rollbackOrderExecutor{order: &order}
	if err := rollbackTunTransaction(context.Background(), store, &tx, transactionPlanForTest(), executor); err != nil {
		t.Fatalf("rollback TUN transaction: %v", err)
	}
	if strings.Join(order, ",") != "executor-rollback,stop-child" {
		t.Fatalf("expected host rollback before child stop, got %#v", order)
	}
}

func TestTunTransactionRollsBackOnlyAppliedStepsAfterPartialApplyFailure(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &recordingTunExecutor{applyErr: errors.New("route apply failed")}
	_, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err == nil || !strings.Contains(err.Error(), "rolled back applied") {
		t.Fatalf("expected rolled back apply failure, got %v", err)
	}
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) > 0 || len(summaries) != 0 {
		t.Fatalf("successful failed-connect rollback must remove or neutralize transaction file, summaries=%#v warnings=%#v", summaries, warnings)
	}
	if strings.Join(executor.calls, ",") != "apply,rollback" {
		t.Fatalf("unexpected executor calls: %#v", executor.calls)
	}
}

func TestTunTransactionRollsBackVerifyFailure(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &recordingTunExecutor{verifyErr: errors.New("route missing")}
	_, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err == nil {
		t.Fatal("expected verify failure")
	}
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) > 0 || len(summaries) != 0 {
		t.Fatalf("successful failed-connect rollback must remove or neutralize transaction file, summaries=%#v warnings=%#v", summaries, warnings)
	}
	if strings.Join(executor.calls, ",") != "apply,verify,rollback" {
		t.Fatalf("unexpected executor calls: %#v", executor.calls)
	}
}

type preApplyInspectingTunExecutor struct {
	t          *testing.T
	runtimeDir string
	inspected  bool
}

func (e *preApplyInspectingTunExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	e.t.Helper()
	e.inspected = true
	summaries, warnings := txstate.ScanTransactions(e.runtimeDir)
	if len(warnings) > 0 || len(summaries) != 1 {
		e.t.Fatalf("unexpected transaction scan during apply: summaries=%#v warnings=%#v", summaries, warnings)
	}
	if summaries[0].State != txstate.TransactionApplying || !summaries[0].RequiresCleanup {
		e.t.Fatalf("expected applying transaction during apply, got %#v", summaries[0])
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: e.runtimeDir}).Load(summaries[0].ID)
	if err != nil {
		e.t.Fatalf("load transaction during apply: %v", err)
	}
	if len(tx.Rollback.Routes) != 0 || len(tx.Rollback.PolicyRules) != 0 || len(tx.Rollback.TUN) != 0 || len(tx.Rollback.DNS) != 0 || len(tx.Rollback.NFTables) != 0 {
		e.t.Fatalf("pre-apply transaction must not claim rollback ownership, got %#v", tx.Rollback)
	}
	return nil, nil
}

func (e *preApplyInspectingTunExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *preApplyInspectingTunExecutor) Rollback(context.Context, planner.TunPlan) error { return nil }

type recordingTunExecutor struct {
	applyErr  error
	verifyErr error
	calls     []string
}

func (e *recordingTunExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	e.calls = append(e.calls, "apply")
	if e.applyErr != nil {
		return []netexecutor.Step{{Kind: "tun-device", Target: "podlaz0", Owner: netexecutor.OwnerTunDevice}}, e.applyErr
	}
	return []netexecutor.Step{
		{Kind: "tun-device", Target: "podlaz0", Owner: netexecutor.OwnerTunDevice},
		{Kind: "route", Target: "podlaz default", Owner: netexecutor.OwnerRoute},
		{Kind: "policy-rule", Target: "priority 51820 from all lookup podlaz", Owner: netexecutor.OwnerPolicyRule},
	}, nil
}

func (e *recordingTunExecutor) Verify(context.Context, planner.TunPlan) error {
	e.calls = append(e.calls, "verify")
	return e.verifyErr
}

func (e *recordingTunExecutor) Rollback(context.Context, planner.TunPlan) error {
	e.calls = append(e.calls, "rollback")
	return nil
}

type rollbackOrderExecutor struct {
	order *[]string
}

func (e *rollbackOrderExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (e *rollbackOrderExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *rollbackOrderExecutor) Rollback(context.Context, planner.TunPlan) error {
	*e.order = append(*e.order, "executor-rollback")
	return nil
}

func transactionPlanForTest() planner.TunPlan {
	return planner.TunPlan{
		ProfileID: "test-profile",
		Mode:      planner.ModeTun,
		TunDevice: planner.TunDevicePlan{Name: "podlaz0", MTU: 1500, Action: "create"},
		Routes: []planner.TunRoutePlan{{
			Family:      "ipv4",
			Destination: "default",
			Table:       planner.TunRoutingTable,
			Interface:   "podlaz0",
			Action:      "add",
		}},
		PolicyRules: []planner.TunPolicyRulePlan{{
			Family:   "ipv4",
			Priority: planner.TunRulePriority,
			Selector: planner.IPv4DefaultSelector,
			Table:    planner.TunRoutingTable,
			Action:   "add",
		}},
		Steps: []string{"Plan TUN interface podlaz0"},
	}
}

func transactionPlanWithServerBypassForTest() planner.TunPlan {
	plan := transactionPlanForTest()
	plan.Routes = append(plan.Routes, planner.TunRoutePlan{
		Family:      "ipv4",
		Destination: "203.0.113.10/32",
		Table:       planner.MainRoutingTable,
		Gateway:     "192.0.2.1",
		Interface:   "eth0",
		Action:      "add",
	})
	plan.PolicyRules = append(plan.PolicyRules, planner.TunPolicyRulePlan{
		Family:   "ipv4",
		Priority: planner.ServerRulePriority,
		Selector: "to 203.0.113.10/32",
		Table:    planner.MainRoutingTable,
		Action:   "add",
	})
	return plan
}

func fixedClock() func() time.Time {
	current := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		current = current.Add(time.Millisecond)
		return current
	}
}
