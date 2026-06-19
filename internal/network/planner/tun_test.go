package planner

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestPlanTunBuildsFullTunnelPlanFromFakeSnapshot(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeResolvedDesktop())
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}

	if plan.Mode != ModeTun || plan.TunnelMode != TunTunnelMode {
		t.Fatalf("unexpected TUN mode: mode=%q tunnel=%q", plan.Mode, plan.TunnelMode)
	}
	if plan.TunDevice.Name != snapshot.DefaultTunName || plan.TunDevice.MTU != DefaultTunMTU || plan.TunDevice.Action != "create" {
		t.Fatalf("unexpected TUN device plan: %#v", plan.TunDevice)
	}
	if !containsRoute(plan.Routes, TunRoutingTable, IPv4DefaultRoute, snapshot.DefaultTunName) {
		t.Fatalf("expected default route through %s table, got %#v", TunRoutingTable, plan.Routes)
	}
	if plan.ServerBypass.Destination != "203.0.113.10/32" || plan.ServerBypass.Gateway != "192.0.2.1" || plan.ServerBypass.Interface != "wlp0s20f3" || plan.ServerBypass.Table != MainRoutingTable {
		t.Fatalf("unexpected server bypass route: %#v", plan.ServerBypass)
	}
	if !containsPolicyRule(plan.PolicyRules, ServerRulePriority, "to 203.0.113.10/32", MainRoutingTable) {
		t.Fatalf("expected server bypass policy rule, got %#v", plan.PolicyRules)
	}
	if !containsPolicyRule(plan.PolicyRules, TunRulePriority, IPv4DefaultSelector, TunRoutingTable) {
		t.Fatalf("expected full-tunnel policy rule, got %#v", plan.PolicyRules)
	}
	if plan.DNS.Backend != DNSBackendSystemdResolved || plan.DNS.TargetLink != snapshot.DefaultTunName || plan.DNS.Action != DNSActionConfigure {
		t.Fatalf("unexpected DNS plan: %#v", plan.DNS)
	}
	assertDefaultFirewallPlan(t, plan.Firewall, FirewallActionAdd, FirewallVerdictReject)
	if len(plan.RollbackSteps) == 0 {
		t.Fatalf("expected rollback steps for planned route/TUN changes")
	}
	if !containsString(plan.RollbackSteps, "Restore previous systemd-resolved") || !containsString(plan.RollbackSteps, "Remove nftables table inet podlaz") {
		t.Fatalf("expected DNS and nftables rollback steps, got %#v", plan.RollbackSteps)
	}
	if len(plan.LoopRisks) != 0 {
		t.Fatalf("expected clean fake snapshot to have no loop risk, got %#v", plan.LoopRisks)
	}
}

func TestPlanTunWarnsWhenDefaultRouteIsMissing(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeDesktopMissingDefaultRoute())
	if err != nil {
		t.Fatalf("plan tun with missing default route: %v", err)
	}
	if !containsWarning(plan.Warnings, "IPv4 default route is missing") {
		t.Fatalf("expected missing default route warning, got %#v", plan.Warnings)
	}
	if !containsWarning(plan.Warnings, "VPN server bypass target") {
		t.Fatalf("expected incomplete server bypass warning, got %#v", plan.Warnings)
	}
	if !containsFirewallRule(plan.Firewall.Rules, FirewallServerBypassOwner, FirewallActionBlocked, FirewallVerdictAccept, "ip daddr <server-ip>") {
		t.Fatalf("expected blocked firewall server-bypass rule, got %#v", plan.Firewall.Rules)
	}
}

func TestPlanTunWarnsAboutExistingpodlazTun(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeDesktopWithStalepodlazResources())
	if err != nil {
		t.Fatalf("plan tun with existing podlaz resources: %v", err)
	}
	if !containsWarning(plan.Warnings, "podlaz TUN device podlaz0 already exists") {
		t.Fatalf("expected existing TUN warning, got %#v", plan.Warnings)
	}
	if !containsWarning(plan.Warnings, "stale podlaz-owned") {
		t.Fatalf("expected stale-resource warning, got %#v", plan.Warnings)
	}
	if plan.Firewall.TableAction != FirewallActionValidate {
		t.Fatalf("expected existing nftables table to require validation/replacement, got %#v", plan.Firewall)
	}
	if !containsFirewallChain(plan.Firewall.Chains, FirewallOutputChain, FirewallOutputHook, FirewallActionValidate) {
		t.Fatalf("expected validate-or-replace output chain, got %#v", plan.Firewall.Chains)
	}
}

func TestPlanTunDetectsServerRouteLoopRisk(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeDesktopWithServerRouteLoop())
	if err != nil {
		t.Fatalf("plan tun with route loop: %v", err)
	}
	if len(plan.LoopRisks) == 0 {
		t.Fatalf("expected route loop risks")
	}
	if !containsWarning(plan.Warnings, "route to VPN server candidate points at podlaz0") {
		t.Fatalf("expected route loop warning, got %#v", plan.Warnings)
	}
}

func TestPlanTunPlansResolvedDNSWhenPresent(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeResolvedDesktop())
	if err != nil {
		t.Fatalf("plan tun with resolved present: %v", err)
	}
	if plan.DNS.Action != DNSActionConfigure || plan.DNS.Backend != DNSBackendSystemdResolved || plan.DNS.TargetLink != snapshot.DefaultTunName {
		t.Fatalf("expected systemd-resolved DNS desired state, got %#v", plan.DNS)
	}
}

func TestPlanTunBlocksDNSPlanWhenResolvedMissing(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), fakeDesktopWithoutResolved())
	if err != nil {
		t.Fatalf("plan tun with resolved missing: %v", err)
	}
	if plan.DNS.Action != DNSActionBlocked {
		t.Fatalf("expected DNS plan to be blocked, got %#v", plan.DNS)
	}
	if !containsWarning(plan.Warnings, "DNS desired state is blocked") {
		t.Fatalf("expected blocked DNS warning, got %#v", plan.Warnings)
	}
}

func TestPlanTunPlansNftablesChainsAndRulesWhenPresent(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeResolvedDesktop())
	if err != nil {
		t.Fatalf("plan tun with nftables present: %v", err)
	}
	assertDefaultFirewallPlan(t, plan.Firewall, FirewallActionAdd, FirewallVerdictReject)
}

func TestPlanTunBlocksFirewallPlanWhenNftablesMissing(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), fakeDesktopWithoutNftables())
	if err != nil {
		t.Fatalf("plan tun with nftables missing: %v", err)
	}
	if plan.Firewall.TableAction != FirewallActionBlocked {
		t.Fatalf("expected firewall plan to be blocked, got %#v", plan.Firewall)
	}
	if !containsFirewallChain(plan.Firewall.Chains, FirewallOutputChain, FirewallOutputHook, FirewallActionBlocked) {
		t.Fatalf("expected blocked output chain, got %#v", plan.Firewall.Chains)
	}
	if !containsFirewallRule(plan.Firewall.Rules, FirewallKillSwitchOwner, FirewallActionBlocked, FirewallVerdictReject, `oifname != "podlaz0"`) {
		t.Fatalf("expected blocked kill-switch rule, got %#v", plan.Firewall.Rules)
	}
	if !containsWarning(plan.Warnings, "firewall desired state is blocked") {
		t.Fatalf("expected blocked firewall warning, got %#v", plan.Warnings)
	}
}

func TestPlanTunWarnsAboutStrictKillSwitchPolicy(t *testing.T) {
	plan, err := PlanTunWithOptions(testVLESSProfile(), snapshot.FakeResolvedDesktop(), TunOptions{KillSwitchPolicy: KillSwitchPolicyStrict})
	if err != nil {
		t.Fatalf("plan tun with strict kill-switch: %v", err)
	}
	if plan.Firewall.KillSwitch.Policy != KillSwitchPolicyStrict {
		t.Fatalf("expected strict kill-switch policy, got %#v", plan.Firewall.KillSwitch)
	}
	if !containsFirewallRule(plan.Firewall.Rules, FirewallKillSwitchOwner, FirewallActionAdd, FirewallVerdictDrop, `oifname != "podlaz0"`) {
		t.Fatalf("expected strict drop kill-switch rule, got %#v", plan.Firewall.Rules)
	}
	if !containsWarning(plan.Warnings, "strict kill-switch") || !containsWarning(plan.Warnings, "recover") {
		t.Fatalf("expected strict kill-switch recovery warning, got %#v", plan.Warnings)
	}
}

func TestPlanTunPlansOffKillSwitchWithoutBlockingRule(t *testing.T) {
	plan, err := PlanTunWithOptions(testVLESSProfile(), snapshot.FakeResolvedDesktop(), TunOptions{KillSwitchPolicy: KillSwitchPolicyOff})
	if err != nil {
		t.Fatalf("plan tun with off kill-switch: %v", err)
	}
	if !containsFirewallRule(plan.Firewall.Rules, FirewallKillSwitchOwner, FirewallActionSkip, "", `oifname != "podlaz0"`) {
		t.Fatalf("expected skipped kill-switch rule, got %#v", plan.Firewall.Rules)
	}
}

func TestPlanTunWarnsAboutMissingOptionalTools(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeDesktopWithoutOptionalTools())
	if err != nil {
		t.Fatalf("plan tun without optional tools: %v", err)
	}
	for _, want := range []string{"systemd-resolved", "nftables"} {
		if !containsWarning(plan.Warnings, want) {
			t.Fatalf("expected warning containing %q, got %#v", want, plan.Warnings)
		}
	}
}

func assertDefaultFirewallPlan(t *testing.T, plan TunFirewallPlan, ruleAction, killSwitchVerdict string) {
	t.Helper()
	if plan.Backend != FirewallBackendNftables || plan.Family != snapshot.DefaultNFTFamily || plan.Table != snapshot.DefaultNFTTable || plan.TableAction != FirewallTableAction {
		t.Fatalf("unexpected firewall plan: %#v", plan)
	}
	if plan.KillSwitch.Policy != KillSwitchPolicySoft || !strings.Contains(plan.KillSwitch.Action, "block non-TUN traffic") {
		t.Fatalf("unexpected kill-switch plan: %#v", plan.KillSwitch)
	}
	if !containsFirewallChain(plan.Chains, FirewallOutputChain, FirewallOutputHook, FirewallTableAction) {
		t.Fatalf("expected output chain in firewall plan, got %#v", plan.Chains)
	}
	if !containsFirewallRule(plan.Rules, FirewallServerBypassOwner, ruleAction, FirewallVerdictAccept, "ip daddr 203.0.113.10") {
		t.Fatalf("expected server bypass firewall rule, got %#v", plan.Rules)
	}
	if !containsFirewallRule(plan.Rules, FirewallTunEgressOwner, ruleAction, FirewallVerdictAccept, `oifname "podlaz0"`) {
		t.Fatalf("expected TUN egress firewall rule, got %#v", plan.Rules)
	}
	if !containsFirewallRule(plan.Rules, FirewallKillSwitchOwner, ruleAction, killSwitchVerdict, `oifname != "podlaz0"`) {
		t.Fatalf("expected kill-switch firewall rule, got %#v", plan.Rules)
	}
}

func containsRoute(routes []TunRoutePlan, table, destination, iface string) bool {
	for _, route := range routes {
		if route.Table == table && route.Destination == destination && route.Interface == iface {
			return true
		}
	}
	return false
}

func containsPolicyRule(rules []TunPolicyRulePlan, priority int, selector, table string) bool {
	for _, rule := range rules {
		if rule.Priority == priority && rule.Selector == selector && rule.Table == table {
			return true
		}
	}
	return false
}

func containsFirewallChain(chains []TunFirewallChainPlan, name, hook, action string) bool {
	for _, chain := range chains {
		if chain.Name == name && chain.Hook == hook && chain.Action == action && chain.Type != "" && chain.Policy != "" {
			return true
		}
	}
	return false
}

func containsFirewallRule(rules []TunFirewallRulePlan, ownership, action, verdict, expr string) bool {
	for _, rule := range rules {
		if rule.Ownership == ownership && rule.Action == action && rule.Verdict == verdict && rule.Expr == expr && rule.Chain != "" && rule.RollbackKey != "" {
			return true
		}
	}
	return false
}

func containsWarning(warnings []string, want string) bool {
	return containsString(warnings, want)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func fakeDesktopWithoutResolved() snapshot.Snapshot {
	s := snapshot.FakeResolvedDesktop()
	s.DNS = snapshot.DNS{Mode: "unknown", Resolved: snapshot.Finding{Status: snapshot.StatusMissing, Summary: "resolvectl not found"}}
	return s
}

func fakeDesktopWithoutNftables() snapshot.Snapshot {
	s := snapshot.FakeResolvedDesktop()
	s.Nftables = snapshot.Nftables{
		Availability: snapshot.Finding{Status: snapshot.StatusMissing, Summary: "nft not found"},
		PodlazTable:  snapshot.Finding{Status: snapshot.StatusMissing, Summary: "podlaz nftables table not inspected because nft is unavailable"},
	}
	return s
}
