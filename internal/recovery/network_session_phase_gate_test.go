package recovery

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestNetworkSessionRecoveryStopsBeforeRoutingWhenExactFirewallRemovalFails(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := phaseGatedRecoveryTransaction(t)
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatal(err)
	}

	runner := newPhaseGateRecoveryRunner()
	runner.removeErr = errors.New("synthetic guarded nftables removal blocker")

	results := (NetworkSessionCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir}).CleanupMany(
		context.Background(), transactionCandidate(path, tx),
	)

	assertCleanupResult(t, results, "nftables-table", "failed", "synthetic guarded nftables removal blocker")
	if len(runner.ipCommands) != 0 {
		t.Fatalf("firewall blocker must stop dependent routing cleanup, got ip mutations %#v", runner.ipCommands)
	}
	if _, _, err := store.Load(tx.ID); err != nil {
		t.Fatalf("firewall blocker must preserve transaction authority: %v", err)
	}
}

func TestNetworkSessionRecoveryContinuesAfterCrashWithFirewallAlreadyAbsent(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := phaseGatedRecoveryTransaction(t)
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatal(err)
	}

	runner := newPhaseGateRecoveryRunner()
	runner.nft.tablePresent = false

	results := (NetworkSessionCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir}).CleanupMany(
		context.Background(), transactionCandidate(path, tx),
	)

	assertCleanupResult(t, results, "nftables-table", "recovered", "")
	assertCleanupResult(t, results, "policy-rule", "recovered", "")
	assertCleanupResult(t, results, "route", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	if len(runner.ipCommands) == 0 {
		t.Fatal("already-absent firewall phase did not continue remaining exact routing cleanup")
	}
	if runner.nft.guardedRemoves != 0 {
		t.Fatalf("already-absent firewall was mutated again: guarded removals=%d", runner.nft.guardedRemoves)
	}
	if _, _, err := store.Load(tx.ID); err == nil {
		t.Fatal("converged crash-retry cleanup left transaction authority")
	}
}

func phaseGatedRecoveryTransaction(t *testing.T) txstate.Transaction {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	tx := txstate.NewTransaction("terminal-phase-gate", "profile-example", planner.ModeTun, now)
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family:        "ipv4",
		InterfaceName: managedInterface,
		CIDR:          "198.18.0.2/32",
		Scope:         "global",
		Owner:         netexecutor.OwnerTunAddress,
	}
	tx.DesiredPlan.Routes = []txstate.RoutePlan{
		{Kind: "route", Table: "51821", CIDR: planner.IPv4DefaultRoute, Dev: managedInterface, Owner: netexecutor.OwnerRoute, Operation: "add"},
		{Kind: "route", Table: planner.MainRoutingTable, CIDR: "203.0.113.10/32", Dev: "eth0", Owner: netexecutor.OwnerRoute, Operation: "add"},
	}
	tx.DesiredPlan.Steps = []txstate.PlannedStep{
		{Kind: "policy-rule", Target: "priority 98 to 203.0.113.10/32 lookup main", Owner: netexecutor.OwnerPolicyRule},
		{Kind: "policy-rule", Target: "priority 99 from all lookup 51821", Owner: netexecutor.OwnerPolicyRule},
	}
	tx.DesiredPlan.NFT = txstate.NFTPlan{
		Family: "inet",
		Table:  "podlaz",
		Owner:  netexecutor.OwnerFirewall,
		Chains: []txstate.NFTChainPlan{{
			Name:     planner.FirewallOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Owner:    netexecutor.OwnerFirewall,
			Rules:    []string{`oifname "lo" accept owner podlaz:firewall:loopback`},
		}},
	}
	tx.Rollback.Routes = []txstate.RouteRollback{
		{Table: "51821", CIDR: planner.IPv4DefaultRoute, Dev: managedInterface, Owner: netexecutor.OwnerRoute},
		{Table: planner.MainRoutingTable, CIDR: "203.0.113.10/32", Dev: "eth0", Owner: netexecutor.OwnerRoute},
	}
	tx.Rollback.PolicyRules = []txstate.PolicyRuleRollback{
		{Priority: 98, To: "203.0.113.10/32", Table: planner.MainRoutingTable, Owner: netexecutor.OwnerPolicyRule},
		{Priority: 99, From: "all", Table: "51821", Owner: netexecutor.OwnerPolicyRule},
	}
	tx.Rollback.NFTables = []txstate.NFTablesRollback{{Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall}}
	for _, route := range tx.Rollback.Routes {
		tx.AppliedSteps = append(tx.AppliedSteps, txstate.AppliedStep{
			Kind: "route", Target: routeRollbackTarget(route), Description: "synthetic applied route proof", Owner: netexecutor.OwnerRoute, AppliedAt: now,
		})
	}
	for _, rule := range tx.Rollback.PolicyRules {
		tx.AppliedSteps = append(tx.AppliedSteps, txstate.AppliedStep{
			Kind: "policy-rule", Target: policyRuleRollbackTarget(rule), Description: "synthetic applied rule proof", Owner: netexecutor.OwnerPolicyRule, AppliedAt: now,
		})
	}
	tx.AppliedSteps = append(tx.AppliedSteps, txstate.AppliedStep{
		Kind: "nftables", Target: "inet podlaz", Description: "synthetic applied firewall proof", Owner: netexecutor.OwnerFirewall, AppliedAt: now,
	})
	return tx
}

type phaseGateRecoveryRunner struct {
	nft        *nftablesAuthorityRunner
	removeErr  error
	ipCommands []string
}

func newPhaseGateRecoveryRunner() *phaseGateRecoveryRunner {
	return &phaseGateRecoveryRunner{nft: newNftablesAuthorityRunner()}
}

func (r *phaseGateRecoveryRunner) LookPath(file string) (string, error) {
	switch file {
	case "nft":
		return "/usr/bin/nft", nil
	case "ip":
		return "/usr/sbin/ip", nil
	default:
		return "", fmt.Errorf("command not found: %s", file)
	}
}

func (r *phaseGateRecoveryRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	switch filepath.Base(name) {
	case "nft":
		return r.nft.Run(ctx, name, args...)
	case "ip":
		command := "ip " + strings.Join(args, " ")
		r.ipCommands = append(r.ipCommands, command)
		return CommandResult{}, nil
	default:
		return CommandResult{ExitCode: -1}, fmt.Errorf("unexpected command %s", name)
	}
}

func (r *phaseGateRecoveryRunner) NftablesGeneration(ctx context.Context) (uint32, error) {
	return r.nft.NftablesGeneration(ctx)
}

func (r *phaseGateRecoveryRunner) NftablesRemoveTable(ctx context.Context, family, table string, handle uint64, generation uint32) error {
	if r.removeErr != nil {
		return r.removeErr
	}
	return r.nft.NftablesRemoveTable(ctx, family, table, handle, generation)
}

func (r *phaseGateRecoveryRunner) NftablesReplaceTable(ctx context.Context, family, table string, handle uint64, generation uint32, plan planner.TunFirewallPlan) error {
	return r.nft.NftablesReplaceTable(ctx, family, table, handle, generation, plan)
}
