package recovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// inspectUnrecordedDesiredMainState protects pre-existing main-table state from
// desired-plan-only cleanup. If the exact namespace is absent, recovery may
// continue. If anything is present, ownership is ambiguous and the transaction
// is preserved for explicit inspection instead of deleting host state by guess.
func (e DaemonCleanupExecutor) inspectUnrecordedDesiredMainState(ctx context.Context, tx txstate.Transaction) []CleanupResult {
	var results []CleanupResult
	for _, route := range tx.DesiredPlan.Routes {
		if route.Operation != "add" || route.Table != planner.MainRoutingTable || recordedRouteRollback(tx.Rollback.Routes, route) {
			continue
		}
		results = append(results, e.inspectDesiredMainRoute(ctx, route))
	}
	for _, step := range tx.DesiredPlan.Steps {
		rule, ok := desiredPolicyRuleRollback(step)
		if !ok || !safeMainServerBypassPolicyRule(rule) || recordedPolicyRuleRollback(tx.Rollback.PolicyRules, rule) {
			continue
		}
		results = append(results, e.inspectDesiredMainPolicyRule(ctx, rule))
	}
	return results
}

func (e DaemonCleanupExecutor) inspectDesiredMainRoute(ctx context.Context, route txstate.RoutePlan) CleanupResult {
	candidate := Candidate{Kind: "route", Description: "main-table server bypass route", Target: fmt.Sprintf("%s table %s", route.CIDR, route.Table)}
	ipPath, err := e.Runner.LookPath("ip")
	if err != nil {
		return failed(candidate, fmt.Errorf("inspect unrecorded main-table route: ip command is unavailable"))
	}
	result, runErr := runCommand(ctx, e.Runner, ipPath, "-4", "route", "show", "table", planner.MainRoutingTable, route.CIDR)
	if !commandSucceeded(result, runErr) {
		return failed(candidate, fmt.Errorf("inspect unrecorded main-table route: %s", commandFailureMessage(result, runErr)))
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return recovered(candidate)
	}
	return skipped(candidate, "main-table route exists but the transaction did not durably record that podlaz created it")
}

func (e DaemonCleanupExecutor) inspectDesiredMainPolicyRule(ctx context.Context, rule txstate.PolicyRuleRollback) CleanupResult {
	candidate := Candidate{Kind: "policy-rule", Description: "main-table server bypass policy rule", Target: fmt.Sprintf("priority %d table %s", rule.Priority, rule.Table)}
	ipPath, err := e.Runner.LookPath("ip")
	if err != nil {
		return failed(candidate, fmt.Errorf("inspect unrecorded main-table policy rule: ip command is unavailable"))
	}
	result, runErr := runCommand(ctx, e.Runner, ipPath, "-4", "rule", "show", "priority", fmt.Sprint(rule.Priority))
	if !commandSucceeded(result, runErr) {
		return failed(candidate, fmt.Errorf("inspect unrecorded main-table policy rule: %s", commandFailureMessage(result, runErr)))
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return recovered(candidate)
	}
	return skipped(candidate, "main-table policy rule exists but the transaction did not durably record that podlaz created it")
}

func recordedRouteRollback(entries []txstate.RouteRollback, desired txstate.RoutePlan) bool {
	for _, entry := range entries {
		if entry.Table == desired.Table && entry.CIDR == desired.CIDR && entry.Via == desired.Via && entry.Dev == desired.Dev {
			return true
		}
	}
	return false
}

func recordedPolicyRuleRollback(entries []txstate.PolicyRuleRollback, desired txstate.PolicyRuleRollback) bool {
	for _, entry := range entries {
		if entry.Priority == desired.Priority && entry.Table == desired.Table && entry.From == desired.From && entry.To == desired.To && entry.Mark == desired.Mark {
			return true
		}
	}
	return false
}
