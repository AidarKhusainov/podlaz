package executor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const privacyEnvelopeOwnerPrefix = "podlaz:privacy-envelope:"

var privacyEnvelopeTableNamePattern = regexp.MustCompile(`^podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?$`)

// PrivacyEnvelopePlan is the exact nftables composition owned by one Network
// Session. The plan is intentionally separate from TunFirewallPlan so generic
// TUN transaction rollback never acquires authority over this longer-lived
// resource.
type PrivacyEnvelopePlan struct {
	Family string
	Table  string
	Chains []planner.TunFirewallChainPlan
	Rules  []planner.TunFirewallRulePlan
	Reason string
}

// PrivacyEnvelopeExecutor mutates only the exact dynamic table carried by a
// validated session-backed PrivacyEnvelopePlan. A matching table name alone is
// never cleanup authority; callers must reconstruct this plan from durable
// Network Session state before invoking mutation methods.
type PrivacyEnvelopeExecutor struct {
	Runner    CommandRunner
	ScriptDir string
}

func (e PrivacyEnvelopeExecutor) Exists(ctx context.Context, plan PrivacyEnvelopePlan) (bool, error) {
	if err := validatePrivacyEnvelopePlan(plan); err != nil {
		return false, err
	}
	result, err := observeCommand(ctx, e.Runner, "nft", "-y", "list", "table", plan.Family, plan.Table)
	if err != nil {
		if resourceMissing(err) {
			return false, nil
		}
		return false, fmt.Errorf("observe privacy envelope %s %s: %w", plan.Family, plan.Table, err)
	}
	if _, err := parseOwnedNftTable(result.Stdout, plan.Family, plan.Table); err != nil {
		return false, fmt.Errorf("observe privacy envelope %s %s: %w", plan.Family, plan.Table, err)
	}
	return true, nil
}

func (e PrivacyEnvelopeExecutor) Apply(ctx context.Context, plan PrivacyEnvelopePlan) error {
	if err := validatePrivacyEnvelopePlan(plan); err != nil {
		return err
	}
	script, err := privacyEnvelopeApplyScript(plan)
	if err != nil {
		return err
	}
	return e.runBatch(ctx, plan, script, "apply")
}

// Replace swaps one exact composition for another in one nft transaction. The
// family/table identity must remain stable for the Network Session. If the batch
// fails, nftables transaction semantics leave the previous kernel generation in
// place; no userspace compensating delete is attempted.
func (e PrivacyEnvelopeExecutor) Replace(ctx context.Context, oldPlan, newPlan PrivacyEnvelopePlan) error {
	if err := validatePrivacyEnvelopePlan(oldPlan); err != nil {
		return fmt.Errorf("validate old privacy envelope: %w", err)
	}
	if err := validatePrivacyEnvelopePlan(newPlan); err != nil {
		return fmt.Errorf("validate new privacy envelope: %w", err)
	}
	if oldPlan.Family != newPlan.Family || oldPlan.Table != newPlan.Table {
		return errors.New("privacy envelope replacement cannot change exact table identity")
	}
	applyScript, err := privacyEnvelopeApplyScript(newPlan)
	if err != nil {
		return err
	}
	script := fmt.Sprintf("delete table %s %s\n%s", oldPlan.Family, oldPlan.Table, applyScript)
	return e.runBatch(ctx, newPlan, script, "replace")
}

func (e PrivacyEnvelopeExecutor) Verify(ctx context.Context, plan PrivacyEnvelopePlan) error {
	if err := validatePrivacyEnvelopePlan(plan); err != nil {
		return err
	}
	result, err := observeCommand(ctx, e.Runner, "nft", "-y", "list", "table", plan.Family, plan.Table)
	if err != nil {
		return fmt.Errorf("verify privacy envelope %s %s: %w", plan.Family, plan.Table, err)
	}
	observed, err := parseOwnedNftTable(result.Stdout, plan.Family, plan.Table)
	if err != nil {
		return fmt.Errorf("verify privacy envelope %s %s: %w", plan.Family, plan.Table, err)
	}
	firewallPlan := planner.TunFirewallPlan{Chains: plan.Chains, Rules: plan.Rules}
	if err := verifyExactNftChains(observed, firewallPlan); err != nil {
		return fmt.Errorf("verify privacy envelope %s %s: %w", plan.Family, plan.Table, err)
	}
	return nil
}

func (e PrivacyEnvelopeExecutor) Remove(ctx context.Context, plan PrivacyEnvelopePlan) error {
	if err := validatePrivacyEnvelopePlan(plan); err != nil {
		return err
	}
	if err := runCommand(ctx, e.Runner, "nft", "delete", "table", plan.Family, plan.Table); err != nil && !resourceMissing(err) {
		return fmt.Errorf("remove privacy envelope %s %s: %w", plan.Family, plan.Table, err)
	}
	return nil
}

func (e PrivacyEnvelopeExecutor) runBatch(ctx context.Context, plan PrivacyEnvelopePlan, script, operation string) error {
	scriptPath, cleanup, err := writeNFTBatchScript(e.ScriptDir, script)
	if err != nil {
		return fmt.Errorf("prepare privacy envelope %s batch for %s %s: %w", operation, plan.Family, plan.Table, err)
	}
	defer cleanup()
	if err := runCommand(ctx, e.Runner, "nft", "-f", scriptPath); err != nil {
		return fmt.Errorf("%s privacy envelope %s %s: %w", operation, plan.Family, plan.Table, err)
	}
	return nil
}

func privacyEnvelopeApplyScript(plan PrivacyEnvelopePlan) (string, error) {
	if err := validatePrivacyEnvelopePlan(plan); err != nil {
		return "", err
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "add table %s %s\n", plan.Family, plan.Table)
	for _, chain := range plan.Chains {
		fmt.Fprintf(
			&builder,
			"add chain %s %s %s { type %s hook %s priority %d; policy %s; }\n",
			plan.Family,
			plan.Table,
			chain.Name,
			chain.Type,
			chain.Hook,
			chain.Priority,
			chain.Policy,
		)
	}
	for _, rule := range plan.Rules {
		expr := strings.TrimSpace(rule.Expr)
		if expr == "" {
			fmt.Fprintf(
				&builder,
				"add rule %s %s %s counter %s comment %s\n",
				plan.Family,
				plan.Table,
				rule.Chain,
				rule.Verdict,
				nftStringLiteral(rule.Ownership),
			)
			continue
		}
		fmt.Fprintf(
			&builder,
			"add rule %s %s %s %s counter %s comment %s\n",
			plan.Family,
			plan.Table,
			rule.Chain,
			expr,
			rule.Verdict,
			nftStringLiteral(rule.Ownership),
		)
	}
	return builder.String(), nil
}

func validatePrivacyEnvelopePlan(plan PrivacyEnvelopePlan) error {
	if plan.Family != ownedNFTFamily {
		return fmt.Errorf("privacy envelope has unsupported nftables family %q", plan.Family)
	}
	if !privacyEnvelopeTableNamePattern.MatchString(plan.Table) {
		return errors.New("privacy envelope has invalid exact table identity")
	}
	if len(plan.Chains) == 0 {
		return errors.New("privacy envelope has no chains")
	}
	seenChains := make(map[string]struct{}, len(plan.Chains))
	for _, chain := range plan.Chains {
		if strings.TrimSpace(chain.Name) == "" || strings.TrimSpace(chain.Type) == "" || strings.TrimSpace(chain.Hook) == "" || strings.TrimSpace(chain.Policy) == "" {
			return errors.New("privacy envelope has incomplete chain metadata")
		}
		if chain.Action != planner.FirewallActionAdd && chain.Action != planner.FirewallTableAction {
			return fmt.Errorf("privacy envelope chain %q has unsupported action %q", chain.Name, chain.Action)
		}
		if _, exists := seenChains[chain.Name]; exists {
			return fmt.Errorf("privacy envelope has duplicate chain %q", chain.Name)
		}
		seenChains[chain.Name] = struct{}{}
	}
	if len(plan.Rules) == 0 {
		return errors.New("privacy envelope has no rules")
	}
	for _, rule := range plan.Rules {
		if _, exists := seenChains[rule.Chain]; !exists {
			return fmt.Errorf("privacy envelope rule references unknown chain %q", rule.Chain)
		}
		if rule.Action != planner.FirewallActionAdd {
			return fmt.Errorf("privacy envelope rule has unsupported action %q", rule.Action)
		}
		if rule.Verdict != planner.FirewallVerdictAccept && rule.Verdict != planner.FirewallVerdictReject && rule.Verdict != planner.FirewallVerdictDrop {
			return fmt.Errorf("privacy envelope rule has unsupported verdict %q", rule.Verdict)
		}
		if !strings.HasPrefix(rule.Ownership, privacyEnvelopeOwnerPrefix) || strings.TrimPrefix(rule.Ownership, privacyEnvelopeOwnerPrefix) == "" {
			return fmt.Errorf("privacy envelope rule has invalid owner %q", rule.Ownership)
		}
	}
	return nil
}
