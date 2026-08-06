package planner

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestPlanTunPinsCurrentDefaultPolicyShape(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeResolvedDesktop())
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}
	if plan.Mode != ModeTun || plan.TunnelMode != TunTunnelMode {
		t.Fatalf("unexpected TUN policy mode: mode=%q tunnel=%q", plan.Mode, plan.TunnelMode)
	}
	if plan.TunDevice.Name != snapshot.DefaultTunName || plan.TunDevice.Action != "verify" || plan.TunDevice.MTU != DefaultTunMTU {
		t.Fatalf("unexpected TUN device policy: %#v", plan.TunDevice)
	}
	if !containsRoute(plan.Routes, TunRoutingTable, IPv4DefaultRoute, snapshot.DefaultTunName) {
		t.Fatalf("missing full-tunnel default route: %#v", plan.Routes)
	}
	if !containsPolicyRule(plan.PolicyRules, ServerRulePriority, "to 203.0.113.10/32", MainRoutingTable) {
		t.Fatalf("missing server-bypass policy rule: %#v", plan.PolicyRules)
	}
	if !containsPolicyRule(plan.PolicyRules, TunRulePriority, IPv4DefaultSelector, TunRoutingTable) {
		t.Fatalf("missing full-tunnel policy rule: %#v", plan.PolicyRules)
	}
	if plan.DNS.Action != DNSActionConfigure || len(plan.DNS.Servers) != 1 || plan.DNS.Servers[0] != DefaultTunDNSServer {
		t.Fatalf("unexpected DNS policy: %#v", plan.DNS)
	}
	if plan.Firewall.KillSwitch.Policy != KillSwitchPolicySoft {
		t.Fatalf("default kill-switch policy = %#v, want %q", plan.Firewall.KillSwitch, KillSwitchPolicySoft)
	}
}

func TestPlanTunPinsWarningBehaviorForRiskyInputs(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeDesktopMissingDefaultRoute())
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}
	for _, warning := range []string{
		"IPv4 default route is missing",
		"route to VPN server candidate is unknown",
		"VPN server bypass target is unknown",
	} {
		if !containsWarning(plan.Warnings, warning) {
			t.Fatalf("expected warning containing %q, got %#v", warning, plan.Warnings)
		}
	}

	strict, err := PlanTunWithOptions(testVLESSProfile(), snapshot.FakeResolvedDesktop(), TunOptions{KillSwitchPolicy: KillSwitchPolicyStrict})
	if err != nil {
		t.Fatalf("plan strict tun: %v", err)
	}
	for _, warning := range []string{
		"strict kill-switch policy selected",
		"strict kill-switch may intentionally keep direct connectivity blocked",
		"strict kill-switch cannot be claimed as leak protection",
	} {
		if !containsWarning(strict.Warnings, warning) {
			t.Fatalf("expected warning containing %q, got %#v", warning, strict.Warnings)
		}
	}
}

func TestPlanTunPinsRollbackStepOrderingAndDomains(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeResolvedDesktop())
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}
	wantPrefix := []string{
		"Remove nftables table inet podlaz if created by this transaction",
		"Restore previous systemd-resolved per-link DNS state for podlaz0 where possible",
		"Flush podlaz-owned DNS server/default-route/domain settings from podlaz0 if created by this transaction",
		"Delete policy rule priority 10000 from all lookup podlaz if created by this transaction",
		"Delete policy rule priority 9999 to 203.0.113.10/32 lookup main if created by this transaction",
	}
	if len(plan.RollbackSteps) < len(wantPrefix) {
		t.Fatalf("rollback step count = %d, want at least %d: %#v", len(plan.RollbackSteps), len(wantPrefix), plan.RollbackSteps)
	}
	for i, want := range wantPrefix {
		if plan.RollbackSteps[i] != want {
			t.Fatalf("rollback step %d = %q, want %q; all steps: %#v", i, plan.RollbackSteps[i], want, plan.RollbackSteps)
		}
	}
	for _, want := range []string{
		"Delete route 203.0.113.10/32 from table main via 192.0.2.1 dev wlp0s20f3 if created by this transaction",
		"Delete route default from table podlaz dev podlaz0 if created by this transaction",
		"Remove exact daemon-owned TUN address 198.18.0.1/32 from podlaz0 only when transaction and link identity match",
	} {
		if !containsString(plan.RollbackSteps, want) {
			t.Fatalf("expected rollback step %q, got %#v", want, plan.RollbackSteps)
		}
	}
	if rollbackStepIndex(plan.RollbackSteps, "Delete TUN interface") >= 0 {
		t.Fatalf("rollback must not delete Xray-owned TUN link: %#v", plan.RollbackSteps)
	}
}
