package snapshot

import "context"

// EnsureTunAllocationEvidence replaces routing evidence used for collision-free
// TUN session allocation with canonical numeric table identities. The normal
// snapshot remains useful for human diagnostics, where iproute2 table names are
// readable, but allocation must reason about kernel numeric identities rather
// than aliases from rt_tables.
func EnsureTunAllocationEvidence(ctx context.Context, s Snapshot) Snapshot {
	return ensureTunAllocationEvidenceWithRunner(ctx, OSRunner{}, s)
}

func ensureTunAllocationEvidenceWithRunner(ctx context.Context, runner CommandRunner, s Snapshot) Snapshot {
	if s.OS != "linux" {
		return s
	}
	ipPath, err := runner.LookPath("ip")
	if err != nil {
		s.IPv4Routes = RouteInventory{Inspection: findingWithDetail(StatusUnknown, "numeric IPv4 route inventory unavailable", "ip command is unavailable")}
		s.IPv4PolicyRules = PolicyRuleInventory{Inspection: findingWithDetail(StatusUnknown, "numeric IPv4 policy-rule inventory unavailable", "ip command is unavailable")}
		return s
	}

	s.IPv4Routes = numericIPv4Routes(ctx, runner, ipPath)
	s.IPv4PolicyRules = numericIPv4PolicyRules(ctx, runner, ipPath)
	return s
}

func numericIPv4Routes(ctx context.Context, runner CommandRunner, ipPath string) RouteInventory {
	result, err := runCommand(ctx, runner, ipPath, "-N", "-4", "-o", "route", "show", "table", "all")
	if !commandSucceeded(result, err) {
		return RouteInventory{Inspection: findingWithDetail(StatusUnknown, "numeric IPv4 route inventory unavailable", commandFailureMessage(result, err))}
	}
	routes, parseErr := ParseIPv4Routes(result.Stdout)
	if parseErr != nil {
		return RouteInventory{Inspection: findingWithDetail(StatusUnknown, "numeric IPv4 route inventory malformed", parseErr.Error())}
	}
	return RouteInventory{Inspection: finding(StatusDetected, "numeric IPv4 route inventory available"), Routes: routes}
}

func numericIPv4PolicyRules(ctx context.Context, runner CommandRunner, ipPath string) PolicyRuleInventory {
	result, err := runCommand(ctx, runner, ipPath, "-N", "-4", "rule", "show")
	if !commandSucceeded(result, err) {
		return PolicyRuleInventory{Inspection: findingWithDetail(StatusUnknown, "numeric IPv4 policy-rule inventory unavailable", commandFailureMessage(result, err))}
	}
	rules, parseErr := ParseIPv4PolicyRules(result.Stdout)
	if parseErr != nil {
		return PolicyRuleInventory{Inspection: findingWithDetail(StatusUnknown, "numeric IPv4 policy-rule inventory malformed", parseErr.Error())}
	}
	return PolicyRuleInventory{Inspection: finding(StatusDetected, "numeric IPv4 policy-rule inventory available"), Rules: rules}
}
