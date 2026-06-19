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

func TestTunTransactionRollsBackOnlyAppliedStepsAfterPartialApplyFailure(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &recordingTunExecutor{applyErr: errors.New("route apply failed")}
	_, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err == nil || !strings.Contains(err.Error(), "rolled back applied") {
		t.Fatalf("expected rolled back apply failure, got %v", err)
	}
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) > 0 || len(summaries) != 1 {
		t.Fatalf("unexpected transaction scan: summaries=%#v warnings=%#v", summaries, warnings)
	}
	if summaries[0].State != txstate.TransactionRolledBack || summaries[0].RequiresCleanup {
		t.Fatalf("expected clean rolled-back transaction, got %#v", summaries[0])
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(summaries[0].ID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if len(tx.Rollback.Routes) != 0 || len(tx.Rollback.PolicyRules) != 0 || len(tx.Rollback.TUN) != 1 {
		t.Fatalf("expected rollback metadata to contain only applied TUN state, got %#v", tx.Rollback)
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
	if len(warnings) > 0 || len(summaries) != 1 {
		t.Fatalf("unexpected transaction scan: summaries=%#v warnings=%#v", summaries, warnings)
	}
	if summaries[0].State != txstate.TransactionRolledBack || summaries[0].RequiresCleanup {
		t.Fatalf("expected clean rolled-back transaction, got %#v", summaries[0])
	}
	if strings.Join(executor.calls, ",") != "apply,verify,rollback" {
		t.Fatalf("unexpected executor calls: %#v", executor.calls)
	}
}

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

func fixedClock() func() time.Time {
	current := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		current = current.Add(time.Millisecond)
		return current
	}
}
