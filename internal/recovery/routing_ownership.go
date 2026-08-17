package recovery

import (
	"net/netip"
	"strconv"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// TransactionOwnsObservedRoutingResource reports whether a validated transaction
// contains rollback metadata whose exact deletion identity matches one observed
// route or policy rule. It deliberately does not infer ownership from a reserved
// table or priority alone.
func TransactionOwnsObservedRoutingResource(tx txstate.Transaction, kind, target, raw string) bool {
	if !tx.RequiresRecovery() {
		return false
	}
	switch strings.TrimSpace(kind) {
	case "route":
		observed, ok := parseObservedRoute(target, raw)
		if !ok {
			return false
		}
		for _, route := range tx.Rollback.Routes {
			if recoverableRouteRollback(route) && routeRollbackMatchesObserved(route, observed) {
				return true
			}
		}
	case "policy-rule":
		observed, ok := parseObservedPolicyRule(target, raw)
		if !ok {
			return false
		}
		for _, rule := range tx.Rollback.PolicyRules {
			if recoverablePolicyRuleRollback(rule) && policyRuleRollbackMatchesObserved(rule, observed) {
				return true
			}
		}
	}
	return false
}

type observedRoute struct {
	CIDR  string
	Via   string
	Dev   string
	Table string
}

type observedPolicyRule struct {
	Priority int
	From     string
	To       string
	Table    string
	Mark     string
}

func recoverableRouteRollback(route txstate.RouteRollback) bool {
	if !ownedRollbackMetadata(route.Owner, netexecutor.OwnerRoute) {
		return false
	}
	if safeMainServerBypassRoute(route) {
		return true
	}
	if strings.TrimSpace(route.Table) == planner.MainRoutingTable {
		return false
	}
	if _, ok := managedTableToken(route.Table); !ok {
		return false
	}
	dev := strings.TrimSpace(route.Dev)
	return dev == managedInterface
}

func recoverablePolicyRuleRollback(rule txstate.PolicyRuleRollback) bool {
	if !ownedRollbackMetadata(rule.Owner, netexecutor.OwnerPolicyRule) {
		return false
	}
	if safeMainServerBypassPolicyRule(rule) {
		return true
	}
	_, ok := managedTableToken(rule.Table)
	return ok
}

func routeRollbackMatchesObserved(route txstate.RouteRollback, observed observedRoute) bool {
	table, ok := normalizeObservedRoutingTable(route.Table)
	if !ok || table != observed.Table {
		return false
	}
	cidr, ok := normalizeIPv4RouteDestination(route.CIDR)
	if !ok || cidr != observed.CIDR {
		return false
	}
	return strings.TrimSpace(route.Via) == observed.Via && strings.TrimSpace(route.Dev) == observed.Dev
}

func policyRuleRollbackMatchesObserved(rule txstate.PolicyRuleRollback, observed observedPolicyRule) bool {
	table, ok := normalizeObservedRoutingTable(rule.Table)
	if !ok || table != observed.Table || rule.Priority != observed.Priority {
		return false
	}
	from, ok := normalizePolicySelector(rule.From, true)
	if !ok || from != observed.From {
		return false
	}
	to, ok := normalizePolicySelector(rule.To, false)
	if !ok || to != observed.To {
		return false
	}
	return strings.TrimSpace(rule.Mark) == observed.Mark
}

func parseObservedRoute(target, raw string) (observedRoute, bool) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return observedRoute{}, false
	}
	cidr, ok := normalizeIPv4RouteDestination(fields[0])
	if !ok {
		return observedRoute{}, false
	}
	table, ok := normalizeObservedRoutingTable(target)
	if !ok {
		return observedRoute{}, false
	}
	out := observedRoute{CIDR: cidr, Table: table}
	for i := 1; i < len(fields); {
		switch fields[i] {
		case "via":
			if i+1 >= len(fields) {
				return observedRoute{}, false
			}
			via, err := netip.ParseAddr(fields[i+1])
			if err != nil || !via.Is4() {
				return observedRoute{}, false
			}
			out.Via = via.String()
			i += 2
		case "dev":
			if i+1 >= len(fields) {
				return observedRoute{}, false
			}
			out.Dev = fields[i+1]
			i += 2
		case "table":
			if i+1 >= len(fields) {
				return observedRoute{}, false
			}
			explicit, ok := normalizeObservedRoutingTable(fields[i+1])
			if !ok || explicit != out.Table {
				return observedRoute{}, false
			}
			i += 2
		case "proto", "scope", "src", "metric", "mtu", "advmss", "pref":
			if i+1 >= len(fields) {
				return observedRoute{}, false
			}
			i += 2
		case "linkdown", "onlink":
			i++
		default:
			return observedRoute{}, false
		}
	}
	return out, out.Dev != ""
}

func parseObservedPolicyRule(target, raw string) (observedPolicyRule, bool) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) < 3 {
		return observedPolicyRule{}, false
	}
	priority, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":"))
	if err != nil || priority <= 0 || !policyRuleTargetMatchesPriority(target, priority) {
		return observedPolicyRule{}, false
	}
	out := observedPolicyRule{Priority: priority}
	for i := 1; i < len(fields); {
		if i+1 >= len(fields) {
			return observedPolicyRule{}, false
		}
		key, value := fields[i], fields[i+1]
		switch key {
		case "from":
			normalized, ok := normalizePolicySelector(value, true)
			if !ok || out.From != "" {
				return observedPolicyRule{}, false
			}
			out.From = normalized
		case "to":
			normalized, ok := normalizePolicySelector(value, false)
			if !ok || out.To != "" {
				return observedPolicyRule{}, false
			}
			out.To = normalized
		case "lookup", "table":
			normalized, ok := normalizeObservedRoutingTable(value)
			if !ok || out.Table != "" {
				return observedPolicyRule{}, false
			}
			out.Table = normalized
		case "fwmark":
			if out.Mark != "" {
				return observedPolicyRule{}, false
			}
			out.Mark = value
		default:
			return observedPolicyRule{}, false
		}
		i += 2
	}
	if out.From == "" {
		out.From = "all"
	}
	return out, out.Table != ""
}

func policyRuleTargetMatchesPriority(target string, priority int) bool {
	target = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(target), "priority-"))
	if target == "" {
		return false
	}
	value, err := strconv.Atoi(target)
	return err == nil && value == priority
}

func normalizeObservedRoutingTable(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == planner.MainRoutingTable {
		return planner.MainRoutingTable, true
	}
	if table, ok := managedTableToken(value); ok {
		return table, true
	}
	return "", false
}

func normalizeIPv4RouteDestination(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == planner.IPv4DefaultRoute {
		return "0.0.0.0/0", true
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() {
		return "", false
	}
	return prefix.Masked().String(), true
}

func normalizePolicySelector(value string, emptyAsAll bool) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		if emptyAsAll {
			return "all", true
		}
		return "", true
	}
	if value == "all" {
		return "all", true
	}
	if addr, err := netip.ParseAddr(value); err == nil && addr.Is4() {
		return netip.PrefixFrom(addr, 32).String(), true
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() {
		return "", false
	}
	prefix = prefix.Masked()
	if emptyAsAll && prefix.Bits() == 0 {
		return "all", true
	}
	return prefix.String(), true
}
