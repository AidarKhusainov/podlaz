package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/tunwarden/internal/network/executor"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
	txstate "github.com/AidarKhusainov/tunwarden/internal/state"
)

func TestTunTransactionCommitsAfterApplyAndVerify(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &recordingTunExecutor{}
	result, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err != nil {
		t.Fatalf("run TUN transaction failed: %v", err)
	}
	if result.TransactionID == "" || !strings.HasPrefix(result.TransactionID, "tun-") {
		t.Fatalf("expected generated transaction id, got %#v", result)
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if tx.State != txstate.TransactionCommitted {
		t.Fatalf("expected committed transaction, got %s", tx.State)
	}
	if len(tx.AppliedSteps) != 3 || !tx.Rollback.Available() {
		t.Fatalf("expected applied steps and rollback metadata, got %#v", tx)
	}
	if strings.Join(executor.calls, ",") != "apply,verify" {
		t.Fatalf("unexpected executor calls: %#v", executor.calls)
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

func TestTunTransactionMetadataIncludesFirewallRollback(t *testing.T) {
	plan := transactionPlanForTest()
	plan.Firewall = transactionFirewallPlanForTest()

	desired := desiredPlanFromTunPlan(plan)
	if desired.NFT.Family != "inet" || desired.NFT.Table != "tunwarden" || desired.NFT.Owner != netexecutor.OwnerFirewall {
		t.Fatalf("expected nftables desired state, got %#v", desired.NFT)
	}
	if len(desired.NFT.Chains) != 1 || desired.NFT.Chains[0].Name != planner.FirewallOutputChain || len(desired.NFT.Chains[0].Rules) != 1 {
		t.Fatalf("expected nftables chain and rule desired state, got %#v", desired.NFT.Chains)
	}

	rollback := rollbackMetadataFromTunPlan(plan)
	if len(rollback.NFTables) != 1 || rollback.NFTables[0].Family != "inet" || rollback.NFTables[0].Table != "tunwarden" || rollback.NFTables[0].Owner != netexecutor.OwnerFirewall {
		t.Fatalf("expected nftables rollback metadata, got %#v", rollback.NFTables)
	}

	partial := rollbackPlanFromAppliedSteps(plan, []netexecutor.Step{{Kind: "nftables", Target: "inet tunwarden", Owner: netexecutor.OwnerFirewall}})
	if partial.Firewall.Family != "inet" || partial.Firewall.Table != "tunwarden" {
		t.Fatalf("expected partial rollback plan to include applied firewall state, got %#v", partial.Firewall)
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
		return []netexecutor.Step{{Kind: "tun-device", Target: "tunwarden0", Owner: netexecutor.OwnerTunDevice}}, e.applyErr
	}
	return []netexecutor.Step{
		{Kind: "tun-device", Target: "tunwarden0", Owner: netexecutor.OwnerTunDevice},
		{Kind: "route", Target: "tunwarden default", Owner: netexecutor.OwnerRoute},
		{Kind: "policy-rule", Target: "priority 51820 from all lookup tunwarden", Owner: netexecutor.OwnerPolicyRule},
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
		TunDevice: planner.TunDevicePlan{Name: "tunwarden0", MTU: 1500, Action: "create"},
		Routes: []planner.TunRoutePlan{{
			Family:      "ipv4",
			Destination: "default",
			Table:       planner.TunRoutingTable,
			Interface:   "tunwarden0",
			Action:      "add",
		}},
		PolicyRules: []planner.TunPolicyRulePlan{{
			Family:   "ipv4",
			Priority: planner.TunRulePriority,
			Selector: planner.IPv4DefaultSelector,
			Table:    planner.TunRoutingTable,
			Action:   "add",
		}},
		Steps: []string{"Plan TUN interface tunwarden0"},
	}
}

func transactionFirewallPlanForTest() planner.TunFirewallPlan {
	return planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      "inet",
		Table:       "tunwarden",
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     planner.FirewallOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   planner.FirewallTableAction,
		}},
		Rules: []planner.TunFirewallRulePlan{{
			Chain:       planner.FirewallOutputChain,
			Expr:        "oifname != \"tunwarden0\"",
			Verdict:     planner.FirewallVerdictReject,
			Action:      planner.FirewallActionAdd,
			Ownership:   planner.FirewallKillSwitchOwner,
			RollbackKey: planner.FirewallKillSwitchKey,
		}},
		KillSwitch: planner.TunKillSwitchPlan{Policy: planner.KillSwitchPolicySoft},
		Reason:     "create a TunWarden-owned nftables table",
		Rollback:   planner.FirewallRollbackRemove,
	}
}

func fixedClock() func() time.Time {
	current := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		current = current.Add(time.Millisecond)
		return current
	}
}
