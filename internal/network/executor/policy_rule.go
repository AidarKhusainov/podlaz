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

	args := ruleArgs("del", plan)
	if err := runCommand(ctx, e.Runner, "ip", args...); err != nil && !resourceMissing(err) {
		return fmt.Errorf("delete policy rule priority %d: %w", plan.Priority, err)
	}
	if err := flushIPv4RouteCache(ctx, e.Runner); err != nil {
		return fmt.Errorf("flush IPv4 route cache after delete policy rule priority %d: %w", plan.Priority, err)
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
	for _, field := range strings.Fields(plan.Selector) {
		if !containsField(fields, field) {
			return fmt.Errorf("selector mismatch: expected %q in %q", plan.Selector, line)
		}
	}
	expectedTable := routeTable(plan.Table)
	if !containsLookupTable(fields, expectedTable) {
		return fmt.Errorf("lookup table mismatch: expected %s in %q", expectedTable, line)
	}
	return nil
}

func normalizeRuleFields(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, strings.TrimSuffix(field, ":"))
	}
	return out
}

func containsLookupTable(fields []string, table string) bool {
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "lookup" && (fields[i+1] == table || routeTable(fields[i+1]) == table) {
			return true
		}
	}
	return false
}

func ruleTarget(plan planner.TunPolicyRulePlan) string {
	return fmt.Sprintf("priority %d %s lookup %s", plan.Priority, plan.Selector, plan.Table)
}
