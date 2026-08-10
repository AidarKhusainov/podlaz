package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
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

// Verify proves the complete podlaz-owned nftables composition. Because rule
// order and base-chain metadata affect leak protection, expected-state subset
// matching is insufficient: extra chains/rules or hook/priority/policy drift are
// treated as ambiguous owned state and fail closed.
func (e NftablesExecutor) Verify(ctx context.Context, plan planner.TunFirewallPlan) error {
	if err := validateFirewallPlan(plan); err != nil {
		return err
	}
	family, table := firewallFamilyTable(plan)
	// Numeric priority avoids aliases such as "filter" for priority 0 and keeps
	// the verifier deterministic across nft output formatting.
	result, err := observeCommand(ctx, e.Runner, "nft", "-y", "list", "table", family, table)
	if err != nil {
		return fmt.Errorf("verify nftables table %s %s: %w", family, table, err)
	}
	observed, err := parseOwnedNftTable(result.Stdout, family, table)
	if err != nil {
		return fmt.Errorf("verify nftables table %s %s: %w", family, table, err)
	}
	if err := verifyExactNftChains(observed, plan); err != nil {
		return fmt.Errorf("verify nftables table %s %s: %w", family, table, err)
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

type observedNftChain struct {
	name     string
	typeName string
	hook     string
	priority int
	policy   string
	rules    [][]string
}

func parseOwnedNftTable(output, family, table string) ([]observedNftChain, error) {
	lines := nonEmptyTrimmedLines(output)
	if len(lines) < 2 {
		return nil, errors.New("nftables table output is incomplete")
	}
	if lines[0] != fmt.Sprintf("table %s %s {", family, table) {
		return nil, fmt.Errorf("unexpected nftables table header %q", lines[0])
	}

	var chains []observedNftChain
	var current *observedNftChain
	tableClosed := false
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if tableClosed {
			return nil, fmt.Errorf("unexpected content after nftables table close: %q", line)
		}
		if current == nil {
			if line == "}" {
				tableClosed = true
				continue
			}
			if !strings.HasPrefix(line, "chain ") || !strings.HasSuffix(line, " {") {
				return nil, fmt.Errorf("unexpected nftables table member %q", line)
			}
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "chain "), " {"))
			if name == "" {
				return nil, errors.New("nftables chain has empty name")
			}
			chains = append(chains, observedNftChain{name: name})
			current = &chains[len(chains)-1]
			continue
		}

		if line == "}" {
			if current.typeName == "" || current.hook == "" || current.policy == "" {
				return nil, fmt.Errorf("nftables chain %s has incomplete base-chain metadata", current.name)
			}
			current = nil
			continue
		}
		if strings.HasPrefix(line, "type ") {
			if current.typeName != "" {
				return nil, fmt.Errorf("nftables chain %s has duplicate base-chain metadata", current.name)
			}
			typeName, hook, priority, policy, err := parseNftBaseChainMetadata(line)
			if err != nil {
				return nil, fmt.Errorf("nftables chain %s: %w", current.name, err)
			}
			current.typeName = typeName
			current.hook = hook
			current.priority = priority
			current.policy = policy
			continue
		}
		current.rules = append(current.rules, normalizeObservedNftRule(line))
	}
	if current != nil {
		return nil, fmt.Errorf("nftables chain %s is not closed", current.name)
	}
	if !tableClosed {
		return nil, errors.New("nftables table is not closed")
	}
	return chains, nil
}

func nonEmptyTrimmedLines(output string) []string {
	var lines []string
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseNftBaseChainMetadata(line string) (string, string, int, string, error) {
	parts := strings.Split(line, ";")
	if len(parts) < 2 {
		return "", "", 0, "", fmt.Errorf("invalid base-chain metadata %q", line)
	}
	fields := strings.Fields(strings.TrimSpace(parts[0]))
	if len(fields) != 6 || fields[0] != "type" || fields[2] != "hook" || fields[4] != "priority" {
		return "", "", 0, "", fmt.Errorf("invalid base-chain metadata %q", line)
	}
	priority, err := strconv.Atoi(fields[5])
	if err != nil {
		return "", "", 0, "", fmt.Errorf("non-numeric base-chain priority %q", fields[5])
	}
	policyFields := strings.Fields(strings.TrimSpace(parts[1]))
	if len(policyFields) != 2 || policyFields[0] != "policy" {
		return "", "", 0, "", fmt.Errorf("invalid base-chain policy %q", strings.TrimSpace(parts[1]))
	}
	return fields[1], fields[3], priority, policyFields[1], nil
}

func normalizeObservedNftRule(line string) []string {
	fields := nftExpressionFields(line)
	out := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		if fields[i] == "counter" && i+4 < len(fields) && fields[i+1] == "packets" && fields[i+3] == "bytes" {
			if _, errPackets := strconv.ParseUint(fields[i+2], 10, 64); errPackets == nil {
				if _, errBytes := strconv.ParseUint(fields[i+4], 10, 64); errBytes == nil {
					out = append(out, "counter")
					i += 4
					continue
				}
			}
		}
		out = append(out, fields[i])
	}
	return out
}

func verifyExactNftChains(observed []observedNftChain, plan planner.TunFirewallPlan) error {
	var expectedChains []planner.TunFirewallChainPlan
	for _, chain := range plan.Chains {
		if chain.Action == planner.FirewallTableAction || chain.Action == planner.FirewallActionAdd {
			expectedChains = append(expectedChains, chain)
		}
	}
	if len(observed) != len(expectedChains) {
		return fmt.Errorf("chain cardinality=%d, want %d", len(observed), len(expectedChains))
	}

	for i, expected := range expectedChains {
		got := observed[i]
		if got.name != expected.Name || got.typeName != expected.Type || got.hook != expected.Hook || got.priority != expected.Priority || got.policy != expected.Policy {
			return fmt.Errorf("chain[%d] metadata mismatch: got name=%s type=%s hook=%s priority=%d policy=%s", i, got.name, got.typeName, got.hook, got.priority, got.policy)
		}
		var expectedRules [][]string
		for _, rule := range plan.Rules {
			if rule.Action == planner.FirewallActionAdd && rule.Chain == expected.Name {
				expectedRules = append(expectedRules, nftRuleFields(rule))
			}
		}
		if len(got.rules) != len(expectedRules) {
			return fmt.Errorf("chain %s rule cardinality=%d, want %d", expected.Name, len(got.rules), len(expectedRules))
		}
		for ruleIndex := range expectedRules {
			if !equalStringFields(got.rules[ruleIndex], expectedRules[ruleIndex]) {
				return fmt.Errorf("chain %s rule[%d] mismatch", expected.Name, ruleIndex)
			}
		}
	}
	return nil
}

func equalStringFields(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
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
