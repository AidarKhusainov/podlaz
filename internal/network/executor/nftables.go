package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
)

const (
	OwnerFirewall = "tunwarden:nftables"
)

// FirewallExecutor owns TunWarden-owned nftables apply, verification, and cleanup.
type FirewallExecutor interface {
	Apply(context.Context, planner.TunFirewallPlan) (Step, error)
	Verify(context.Context, planner.TunFirewallPlan) error
	Rollback(context.Context, planner.TunFirewallPlan) error
}

// NftablesExecutor applies only the table/chains/rules owned by TunWarden.
type NftablesExecutor struct {
	Runner CommandRunner
}

// Apply creates a fresh TunWarden-owned nftables table and installs planned chains/rules.
func (e NftablesExecutor) Apply(ctx context.Context, plan planner.TunFirewallPlan) (Step, error) {
	if err := validateFirewallPlan(plan); err != nil {
		return Step{}, err
	}
	family, table := firewallFamilyTable(plan)
	if err := runCommand(ctx, e.Runner, "nft", "add", "table", family, table); err != nil {
		return Step{}, fmt.Errorf("create nftables table %s %s: %w", family, table, err)
	}
	for _, chain := range plan.Chains {
		if chain.Action != planner.FirewallTableAction && chain.Action != planner.FirewallActionAdd {
			continue
		}
		if err := e.addChain(ctx, family, table, chain); err != nil {
			return Step{}, err
		}
	}
	for _, rule := range plan.Rules {
		if rule.Action != planner.FirewallActionAdd {
			continue
		}
		if err := e.addRule(ctx, family, table, rule); err != nil {
			return Step{}, err
		}
	}
	return Step{Kind: "nftables", Target: firewallTarget(plan), Description: plan.Reason, Owner: OwnerFirewall}, nil
}

// Verify checks that the TunWarden-owned nftables table, chain, and rule state is visible.
func (e NftablesExecutor) Verify(ctx context.Context, plan planner.TunFirewallPlan) error {
	if err := validateFirewallPlan(plan); err != nil {
		return err
	}
	family, table := firewallFamilyTable(plan)
	result, err := observeCommand(ctx, e.Runner, "nft", "list", "table", family, table)
	if err != nil {
		return fmt.Errorf("verify nftables table %s %s: %w", family, table, err)
	}
	for _, chain := range plan.Chains {
		if chain.Action != planner.FirewallTableAction && chain.Action != planner.FirewallActionAdd {
			continue
		}
		if !strings.Contains(result.Stdout, "chain "+chain.Name) {
			return fmt.Errorf("verify nftables table %s %s: chain %s not found", family, table, chain.Name)
		}
	}
	for _, rule := range plan.Rules {
		if rule.Action != planner.FirewallActionAdd {
			continue
		}
		if !strings.Contains(result.Stdout, rule.Expr) || !strings.Contains(result.Stdout, rule.Verdict) || !strings.Contains(result.Stdout, rule.Ownership) {
			return fmt.Errorf("verify nftables table %s %s: rule %s not found", family, table, rule.RollbackKey)
		}
	}
	return nil
}

// Rollback deletes the whole TunWarden-owned nftables table. Deleting the table
// is intentionally idempotent and never touches non-TunWarden tables.
func (e NftablesExecutor) Rollback(ctx context.Context, plan planner.TunFirewallPlan) error {
	family, table := firewallFamilyTable(plan)
	if family == "" || table == "" {
		return nil
	}
	if err := runCommand(ctx, e.Runner, "nft", "delete", "table", family, table); err != nil && !resourceMissing(err) {
		return fmt.Errorf("delete nftables table %s %s: %w", family, table, err)
	}
	return nil
}

func (e NftablesExecutor) addChain(ctx context.Context, family, table string, chain planner.TunFirewallChainPlan) error {
	args := []string{"add", "chain", family, table, chain.Name, "{", "type", chain.Type, "hook", chain.Hook, "priority", fmt.Sprintf("%d", chain.Priority), ";", "policy", chain.Policy, ";", "}"}
	if err := runCommand(ctx, e.Runner, "nft", args...); err != nil {
		return fmt.Errorf("create nftables chain %s %s %s: %w", family, table, chain.Name, err)
	}
	return nil
}

func (e NftablesExecutor) addRule(ctx context.Context, family, table string, rule planner.TunFirewallRulePlan) error {
	fields := strings.Fields(rule.Expr)
	if len(fields) == 0 {
		return fmt.Errorf("missing nftables rule expression for %s", rule.RollbackKey)
	}
	args := []string{"add", "rule", family, table, rule.Chain}
	args = append(args, fields...)
	args = append(args, "counter", "comment", rule.Ownership, rule.Verdict)
	if err := runCommand(ctx, e.Runner, "nft", args...); err != nil {
		return fmt.Errorf("create nftables rule %s %s %s: %w", family, table, rule.RollbackKey, err)
	}
	return nil
}

func validateFirewallPlan(plan planner.TunFirewallPlan) error {
	if plan.TableAction == planner.FirewallActionBlocked {
		return fmt.Errorf("firewall desired state is blocked: %s", plan.Reason)
	}
	if plan.TableAction != "" && plan.TableAction != planner.FirewallTableAction {
		return fmt.Errorf("unsupported firewall table action %q", plan.TableAction)
	}
	if plan.Backend != "" && plan.Backend != planner.FirewallBackendNftables {
		return fmt.Errorf("unsupported firewall backend %q", plan.Backend)
	}
	family, table := firewallFamilyTable(plan)
	if family == "" {
		return errors.New("missing nftables family")
	}
	if table == "" {
		return errors.New("missing nftables table")
	}
	if len(plan.Chains) == 0 {
		return errors.New("missing nftables chains")
	}
	for _, chain := range plan.Chains {
		if strings.TrimSpace(chain.Name) == "" {
			return errors.New("missing nftables chain name")
		}
		if strings.TrimSpace(chain.Type) == "" || strings.TrimSpace(chain.Hook) == "" || strings.TrimSpace(chain.Policy) == "" {
			return fmt.Errorf("incomplete nftables chain %s", chain.Name)
		}
	}
	for _, rule := range plan.Rules {
		if rule.Action != planner.FirewallActionAdd {
			continue
		}
		if strings.TrimSpace(rule.Chain) == "" || strings.TrimSpace(rule.Expr) == "" || strings.TrimSpace(rule.Verdict) == "" {
			return fmt.Errorf("incomplete nftables rule %s", rule.RollbackKey)
		}
		if !strings.HasPrefix(rule.Ownership, "tunwarden:firewall:") {
			return fmt.Errorf("nftables rule %s has non-TunWarden owner %q", rule.RollbackKey, rule.Ownership)
		}
	}
	return nil
}

func shouldApplyFirewall(plan planner.TunFirewallPlan) bool {
	return plan.TableAction == planner.FirewallTableAction && strings.TrimSpace(plan.Table) != ""
}

func firewallFamilyTable(plan planner.TunFirewallPlan) (string, string) {
	return strings.TrimSpace(plan.Family), strings.TrimSpace(plan.Table)
}

func firewallTarget(plan planner.TunFirewallPlan) string {
	family, table := firewallFamilyTable(plan)
	return family + " " + table
}
