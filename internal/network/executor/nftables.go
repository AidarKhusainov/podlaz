package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	OwnerFirewall = "podlaz:nftables"

	ownedNFTFamily = "inet"
	ownedNFTTable  = "podlaz"
)

// FirewallExecutor owns podlaz-owned nftables apply, verification, and cleanup.
type FirewallExecutor interface {
	Apply(context.Context, planner.TunFirewallPlan) (Step, error)
	Verify(context.Context, planner.TunFirewallPlan) error
	Rollback(context.Context, planner.TunFirewallPlan) error
}

// NftablesExecutor applies only the table/chains/rules owned by podlaz.
type NftablesExecutor struct {
	Runner    CommandRunner
	ScriptDir string
}

// Apply creates a fresh podlaz-owned nftables table and installs planned chains/rules.
func (e NftablesExecutor) Apply(ctx context.Context, plan planner.TunFirewallPlan) (step Step, err error) {
	if err := validateFirewallPlan(plan); err != nil {
		return Step{}, err
	}
	family, table := firewallFamilyTable(plan)
	script, err := nftablesApplyScript(plan)
	if err != nil {
		return Step{}, err
	}
	scriptPath, cleanup, err := writeNFTBatchScript(e.ScriptDir, script)
	if err != nil {
		return Step{}, fmt.Errorf("prepare nftables batch for %s %s: %w", family, table, err)
	}
	defer cleanup()
	if err := runCommand(ctx, e.Runner, "nft", "-f", scriptPath); err != nil {
		return Step{}, fmt.Errorf("apply nftables batch %s %s: %w", family, table, err)
	}
	return Step{Kind: "nftables", Target: firewallTarget(plan), Description: plan.Reason, Owner: OwnerFirewall}, nil
}

// Verify checks that the podlaz-owned nftables table, chain, and rule state is visible.
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
		if !nftOutputContainsRule(result.Stdout, rule) {
			return fmt.Errorf("verify nftables table %s %s: rule %s not found", family, table, rule.RollbackKey)
		}
	}
	return nil
}

// Rollback deletes the whole podlaz-owned nftables table. Deleting the table
// is intentionally idempotent and never touches non-podlaz tables.
func (e NftablesExecutor) Rollback(ctx context.Context, plan planner.TunFirewallPlan) error {
	family, table := firewallFamilyTable(plan)
	if family == "" && table == "" {
		return nil
	}
	if err := validateOwnedFirewallTarget(family, table); err != nil {
		return err
	}
	if err := runCommand(ctx, e.Runner, "nft", "delete", "table", family, table); err != nil && !resourceMissing(err) {
		return fmt.Errorf("delete nftables table %s %s: %w", family, table, err)
	}
	return nil
}

func nftablesApplyScript(plan planner.TunFirewallPlan) (string, error) {
	if err := validateFirewallPlan(plan); err != nil {
		return "", err
	}
	family, table := firewallFamilyTable(plan)
	var builder strings.Builder
	fmt.Fprintf(&builder, "add table %s %s\n", family, table)
	for _, chain := range plan.Chains {
		if chain.Action != planner.FirewallTableAction && chain.Action != planner.FirewallActionAdd {
			continue
		}
		fmt.Fprintf(&builder, "add chain %s %s %s { type %s hook %s priority %d; policy %s; }\n", family, table, chain.Name, chain.Type, chain.Hook, chain.Priority, chain.Policy)
	}
	for _, rule := range plan.Rules {
		if rule.Action != planner.FirewallActionAdd {
			continue
		}
		expr := strings.TrimSpace(rule.Expr)
		if expr == "" {
			return "", fmt.Errorf("missing nftables rule expression for %s", rule.RollbackKey)
		}
		fmt.Fprintf(&builder, "add rule %s %s %s %s counter %s comment %s\n", family, table, rule.Chain, expr, rule.Verdict, nftStringLiteral(rule.Ownership))
	}
	return builder.String(), nil
}

func writeNFTBatchScript(dir, script string) (string, func(), error) {
	file, err := os.CreateTemp(dir, "podlaz-nft-*.nft")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.WriteString(script); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
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
	if err := validateOwnedFirewallTarget(family, table); err != nil {
		return err
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
		if !strings.HasPrefix(rule.Ownership, "podlaz:firewall:") {
			return fmt.Errorf("nftables rule %s has non-podlaz owner %q", rule.RollbackKey, rule.Ownership)
		}
	}
	return nil
}

func validateOwnedFirewallTarget(family, table string) error {
	if family != ownedNFTFamily || table != ownedNFTTable {
		return fmt.Errorf("refuse to mutate non-podlaz nftables target %s %s", family, table)
	}
	return nil
}

func shouldApplyFirewall(plan planner.TunFirewallPlan) bool {
	return plan.TableAction == planner.FirewallTableAction && strings.TrimSpace(plan.Table) != ""
}

func nftOutputContainsRule(output string, rule planner.TunFirewallRulePlan) bool {
	want := nftRuleFields(rule)
	for _, line := range strings.Split(output, "\n") {
		if containsOrderedFields(nftExpressionFields(line), want) {
			return true
		}
	}
	return false
}

func nftRuleFields(rule planner.TunFirewallRulePlan) []string {
	fields := nftExpressionFields(rule.Expr)
	fields = append(fields, "counter", rule.Verdict, "comment", rule.Ownership)
	return fields
}

func containsOrderedFields(fields, want []string) bool {
	if len(want) == 0 {
		return true
	}
	pos := 0
	for _, field := range fields {
		if field == want[pos] {
			pos++
			if pos == len(want) {
				return true
			}
		}
	}
	return false
}

func nftExpressionFields(expr string) []string {
	raw := strings.Fields(expr)
	fields := make([]string, 0, len(raw))
	for _, field := range raw {
		fields = append(fields, strings.Trim(field, "\""))
	}
	return fields
}

func nftStringLiteral(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + escaped + `"`
}

func firewallFamilyTable(plan planner.TunFirewallPlan) (string, string) {
	return strings.TrimSpace(plan.Family), strings.TrimSpace(plan.Table)
}

func firewallTarget(plan planner.TunFirewallPlan) string {
	family, table := firewallFamilyTable(plan)
	return family + " " + table
}
