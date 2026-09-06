package executor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type IPPolicyRuleExecutor struct {
	Runner CommandRunner
}

func (e IPPolicyRuleExecutor) Add(ctx context.Context, plan planner.TunPolicyRulePlan) (Step, error) {
	lines, err := e.policyRulePriorityLines(ctx, plan.Priority)
	if err != nil {
		return Step{}, fmt.Errorf("inspect existing policy rule priority %d: %w", plan.Priority, err)
	}
	if plan.Action == planner.TunActionAddExclusive {
		if len(lines) != 0 {
			return Step{}, fmt.Errorf("allocated policy rule priority %d became occupied before apply", plan.Priority)
		}
	} else if len(lines) != 0 {
		if _, err := matchingPolicyRuleLine(strings.Join(lines, "\n"), plan); err != nil {
			return Step{}, fmt.Errorf("inspect existing policy rule priority %d: %w", plan.Priority, err)
		}
		return Step{}, nil
	}

	args := ruleArgs("add", plan)
	if err := runCommand(ctx, e.Runner, "ip", args...); err != nil {
		return Step{}, fmt.Errorf("add policy rule priority %d: %w", plan.Priority, err)
	}
	step := Step{Kind: "policy-rule", Target: ruleTarget(plan), Description: plan.Reason, Owner: OwnerPolicyRule}
	if plan.Action == planner.TunActionAddExclusive {
		if err := e.verifyExclusivePolicyRulePriority(ctx, plan); err != nil {
			return step, fmt.Errorf("verify allocated policy rule priority %d after add: %w", plan.Priority, err)
		}
	}
	if err := flushIPv4RouteCache(ctx, e.Runner); err != nil {
		return step, fmt.Errorf("flush IPv4 route cache after add policy rule priority %d: %w", plan.Priority, err)
	}
	return step, nil
}

func (e IPPolicyRuleExecutor) Verify(ctx context.Context, plan planner.TunPolicyRulePlan) error {
	if plan.Action == planner.TunActionAddExclusive {
		if err := e.verifyExclusivePolicyRulePriority(ctx, plan); err != nil {
			return fmt.Errorf("verify policy rule priority %d: %w", plan.Priority, err)
		}
		return nil
	}

	line, err := e.existingPolicyRuleLine(ctx, plan)
	if err != nil {
		return fmt.Errorf("verify policy rule priority %d: %w", plan.Priority, err)
	}
	if line == "" {
		return fmt.Errorf("verify policy rule priority %d: rule not found", plan.Priority)
	}
	return nil
}

func (e IPPolicyRuleExecutor) Rollback(ctx context.Context, plan planner.TunPolicyRulePlan) error {
	if plan.Action != planner.TunActionAddExclusive {
		return e.rollbackLegacyPolicyRule(ctx, plan)
	}

	lines, err := e.policyRulePriorityLines(ctx, plan.Priority)
	if err != nil {
		return fmt.Errorf("inspect policy rule priority %d before rollback: %w", plan.Priority, err)
	}
	matches := matchingPolicyRuleCount(lines, plan)
	switch {
	case matches == 0:
		return nil
	case matches > 1:
		return fmt.Errorf("refuse policy rule rollback priority %d: exact tuple appears %d times and ownership is ambiguous", plan.Priority, matches)
	}

	if err := e.deletePolicyRule(ctx, plan); err != nil {
		return err
	}
	remaining, err := e.policyRulePriorityLines(ctx, plan.Priority)
	if err != nil {
		return fmt.Errorf("inspect policy rule priority %d after rollback: %w", plan.Priority, err)
	}
	if exact := matchingPolicyRuleCount(remaining, plan); exact != 0 {
		return fmt.Errorf("policy rule rollback priority %d left %d exact tuple(s)", plan.Priority, exact)
	}
	return nil
}

func (e IPPolicyRuleExecutor) rollbackLegacyPolicyRule(ctx context.Context, plan planner.TunPolicyRulePlan) error {
	return e.deletePolicyRule(ctx, plan)
}

func (e IPPolicyRuleExecutor) deletePolicyRule(ctx context.Context, plan planner.TunPolicyRulePlan) error {
	args := ruleArgs("del", plan)
	if err := runCommand(ctx, e.Runner, "ip", args...); err != nil && !resourceMissing(err) {
		return fmt.Errorf("delete policy rule priority %d: %w", plan.Priority, err)
	}
	if err := flushIPv4RouteCache(ctx, e.Runner); err != nil {
		return fmt.Errorf("flush IPv4 route cache after delete policy rule priority %d: %w", plan.Priority, err)
	}
	return nil
}

func (e IPPolicyRuleExecutor) existingPolicyRuleLine(ctx context.Context, plan planner.TunPolicyRulePlan) (string, error) {
	lines, err := e.policyRulePriorityLines(ctx, plan.Priority)
	if err != nil {
		return "", err
	}
	return matchingPolicyRuleLine(strings.Join(lines, "\n"), plan)
}

func (e IPPolicyRuleExecutor) policyRulePriorityLines(ctx context.Context, priority int) ([]string, error) {
	args := []string{"-4", "rule", "show", "priority", strconv.Itoa(priority)}
	result, err := observeCommand(ctx, e.Runner, "ip", args...)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(result.Stdout), nil
}

func (e IPPolicyRuleExecutor) verifyExclusivePolicyRulePriority(ctx context.Context, plan planner.TunPolicyRulePlan) error {
	lines, err := e.policyRulePriorityLines(ctx, plan.Priority)
	if err != nil {
		return err
	}
	if len(lines) != 1 {
		return fmt.Errorf("priority bucket must contain exactly one session rule, found %d", len(lines))
	}
	if err := verifyPolicyRuleLine(lines[0], plan); err != nil {
		return fmt.Errorf("exclusive session rule mismatch: %w", err)
	}
	return nil
}

func matchingPolicyRuleLine(output string, plan planner.TunPolicyRulePlan) (string, error) {
	lines := nonEmptyLines(output)
	if len(lines) == 0 {
		return "", nil
	}
	var firstErr error
	for _, line := range lines {
		if err := verifyPolicyRuleLine(line, plan); err == nil {
			return line, nil
		} else if firstErr == nil {
			firstErr = err
		}
	}
	return "", fmt.Errorf("no matching rule among %d rule(s) at priority %d: %w", len(lines), plan.Priority, firstErr)
}

func matchingPolicyRuleCount(lines []string, plan planner.TunPolicyRulePlan) int {
	matches := 0
	for _, line := range lines {
		if verifyPolicyRuleLine(line, plan) == nil {
			matches++
		}
	}
	return matches
}

func nonEmptyLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func ruleArgs(op string, plan planner.TunPolicyRulePlan) []string {
	args := []string{"-4", "rule", op, "priority", strconv.Itoa(plan.Priority)}
	selectorFields := strings.Fields(plan.Selector)
	args = append(args, selectorFields...)
	args = append(args, "lookup", routeTable(plan.Table))
	return args
}

func verifyPolicyRuleLine(line string, plan planner.TunPolicyRulePlan) error {
	fields := normalizeRuleFields(strings.Fields(line))
	if len(fields) == 0 || fields[0] != strconv.Itoa(plan.Priority) {
		return fmt.Errorf("priority mismatch: expected %d in %q", plan.Priority, line)
	}

	wantFrom, wantTo, err := policyRuleSelectorIdentity(plan.Selector)
	if err != nil {
		return err
	}
	if wantFrom == "" {
		wantFrom = "all"
	}

	i := 1
	if i < len(fields) && fields[i] == "from" {
		if i+1 >= len(fields) || !routeTokenMatches(fields[i+1], wantFrom) {
			return fmt.Errorf("source selector mismatch: expected from %s in %q", wantFrom, line)
		}
		i += 2
	} else if wantFrom != "all" {
		return fmt.Errorf("source selector mismatch: expected from %s in %q", wantFrom, line)
	}

	if wantTo != "" {
		if i+1 >= len(fields) || fields[i] != "to" || !routeTokenMatches(fields[i+1], wantTo) {
			return fmt.Errorf("destination selector mismatch: expected to %s in %q", wantTo, line)
		}
		i += 2
	} else if i < len(fields) && fields[i] == "to" {
		return fmt.Errorf("unexpected destination selector in %q", line)
	}

	if i+1 >= len(fields) || (fields[i] != "lookup" && fields[i] != "table") {
		return fmt.Errorf("lookup table missing in %q", line)
	}
	expectedTable := routeTable(plan.Table)
	if !samePolicyRuleTable(fields[i+1], expectedTable) {
		return fmt.Errorf("lookup table mismatch: expected %s in %q", expectedTable, line)
	}
	i += 2
	if i != len(fields) {
		return fmt.Errorf("unexpected policy-rule selectors or attributes in %q", line)
	}
	return nil
}

func policyRuleSelectorIdentity(selector string) (from, to string, err error) {
	fields := strings.Fields(strings.TrimSpace(selector))
	switch {
	case len(fields) == 2 && fields[0] == "from":
		return fields[1], "", nil
	case len(fields) == 2 && fields[0] == "to":
		return "all", fields[1], nil
	case len(fields) == 4 && fields[0] == "from" && fields[2] == "to":
		return fields[1], fields[3], nil
	default:
		return "", "", fmt.Errorf("unsupported policy-rule selector %q", selector)
	}
}

func samePolicyRuleTable(got, want string) bool {
	return policyRuleTableIdentity(got) == policyRuleTableIdentity(want)
}

func policyRuleTableIdentity(table string) string {
	table = routeTable(strings.TrimSpace(table))
	switch table {
	case "local", "255":
		return "255"
	case "main", "254":
		return "254"
	case "default", "253":
		return "253"
	default:
		return table
	}
}

func normalizeRuleFields(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, strings.TrimSuffix(field, ":"))
	}
	return out
}

func ruleTarget(plan planner.TunPolicyRulePlan) string {
	return fmt.Sprintf("priority %d %s lookup %s", plan.Priority, plan.Selector, plan.Table)
}
