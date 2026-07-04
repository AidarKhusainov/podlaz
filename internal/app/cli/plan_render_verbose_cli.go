package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

func renderTunPlanVerbose(w io.Writer, p planner.TunPlan) {
	s := p.Snapshot
	fmt.Fprintln(w, "podlaz TUN plan")
	fmt.Fprintln(w, "TUN planning snapshot")
	fmt.Fprintf(w, "Profile: %s\nProfile ID: %s\nMode: %s\n", render.Redact(p.ProfileName), render.Redact(p.ProfileID), p.TunnelMode)
	fmt.Fprintln(w, "Read-only: will not create TUN devices, change routes, change policy rules, change DNS, change nftables, start Xray, or write runtime config.")
	fmt.Fprintf(w, "TUN: %s %s (MTU %d)\n", render.Redact(p.TunDevice.Action), render.Redact(p.TunDevice.Name), p.TunDevice.MTU)
	fmt.Fprintf(w, "Routing table: %s (%d)\n", planner.TunRoutingTable, planner.TunRoutingTableID)
	fmt.Fprintln(w, "Default traffic: route through podlaz table")
	fmt.Fprintf(w, "VPN server bypass: %s\n", routePlanLine(p.ServerBypass))
	fmt.Fprintln(w, "Policy rules:")
	for _, r := range p.PolicyRules {
		fmt.Fprintf(w, "- %s\n", ruleLine(r))
	}
	fmt.Fprintln(w, "Routes:")
	for _, r := range p.Routes {
		fmt.Fprintf(w, "- %s\n", routePlanLine(r))
	}
	renderDNSPlan(w, p.DNS)
	renderFirewallPlan(w, p.Firewall)
	fmt.Fprintf(w, "Default IPv4 route: %s\n", renderRoute(s.DefaultIPv4))
	fmt.Fprintf(w, "Default interface: %s\n", renderDefaultInterface(s.DefaultIPv4))
	fmt.Fprintf(w, "Default IPv6 route: %s\n", renderRoute(s.DefaultIPv6))
	fmt.Fprintf(w, "Route to VPN server candidate: %s\n", renderRoute(s.ServerRoute))
	fmt.Fprintf(w, "DNS mode: %s (%s)\n", render.Redact(s.DNS.Mode), renderFinding(s.DNS.Resolved))
	fmt.Fprintf(w, "NetworkManager: %s\n", renderNetworkManager(s.NetworkManager))
	fmt.Fprintf(w, "nftables: %s\n", renderFinding(s.Nftables.Availability))
	fmt.Fprintf(w, "podlaz nftables table: %s\n", renderFinding(s.Nftables.PodlazTable))
	fmt.Fprintf(w, "IPv4 assumption: %s\nIPv6 assumption: %s\n", renderFinding(s.IPv4), renderFinding(s.IPv6))
	fmt.Fprintln(w, "podlaz TUN devices:")
	for _, d := range s.TunDevices {
		fmt.Fprintf(w, "- %s: %s\n", render.Redact(d.Name), renderStatusDetail(d.Status, d.Detail, d.Raw))
	}
	if len(s.StaleResources) == 0 {
		fmt.Fprintln(w, "Stale podlaz-owned resources: none detected")
	} else {
		fmt.Fprintf(w, "Stale podlaz-owned resources: %d detected\n", len(s.StaleResources))
		for _, r := range s.StaleResources {
			fmt.Fprintf(w, "- %s %s: %s\n", render.Redact(r.Kind), render.Redact(r.Name), renderStatusDetail(r.Status, r.Detail, ""))
		}
	}
	if len(p.LoopRisks) > 0 {
		fmt.Fprintf(w, "Route-loop risks: %d\n", len(p.LoopRisks))
		for _, r := range p.LoopRisks {
			fmt.Fprintf(w, "- %s\n", render.Redact(r))
		}
	}
	printWarnings(w, p.Warnings)
	fmt.Fprintln(w, "Rollback steps:")
	for _, step := range p.RollbackSteps {
		fmt.Fprintf(w, "- %s\n", render.Redact(step))
	}
	fmt.Fprintln(w, "No changes were applied.")
}

func renderDNSPlan(w io.Writer, p planner.TunDNSPlan) {
	fmt.Fprintln(w, "DNS plan:")
	fmt.Fprintf(w, "- backend: %s\n", render.Redact(p.Backend))
	fmt.Fprintf(w, "- target link: %s\n", render.Redact(p.TargetLink))
	fmt.Fprintf(w, "- servers: %s\n", render.Redact(strings.Join(p.Servers, ", ")))
	fmt.Fprintln(w, "- route-only domain: ~.")
	fmt.Fprintln(w, "- default route: yes")
	fmt.Fprintf(w, "- action: %s\n", render.Redact(p.Action))
	fmt.Fprintf(w, "- reason: %s\n", render.Redact(p.Reason))
	fmt.Fprintf(w, "- rollback: %s\n", render.Redact(p.Rollback))
}

func renderFirewallPlan(w io.Writer, p planner.TunFirewallPlan) {
	fmt.Fprintln(w, "Firewall plan:")
	fmt.Fprintf(w, "- backend: %s\n", render.Redact(p.Backend))
	fmt.Fprintf(w, "- %s nftables table %s %s\n", render.Redact(p.TableAction), render.Redact(p.Family), render.Redact(p.Table))
	for _, rule := range p.Rules {
		switch rule.Ownership {
		case planner.FirewallServerBypassOwner:
			if rule.Action == planner.FirewallActionBlocked {
				fmt.Fprintln(w, "- blocked until VPN server bypass target resolves to a concrete IP")
			} else {
				fmt.Fprintln(w, "- allow VPN server bypass outside TUN")
			}
		case planner.FirewallTunEgressOwner:
			fmt.Fprintf(w, "- allow traffic through %s\n", render.Redact(pTunNameFromRule(rule)))
		case planner.FirewallKillSwitchOwner:
			fmt.Fprintf(w, "- %s\n", render.Redact(p.KillSwitch.Action))
		}
	}
	fmt.Fprintf(w, "- kill-switch policy: %s\n", render.Redact(p.KillSwitch.Policy))
	fmt.Fprintln(w, "Firewall chains:")
	for _, chain := range p.Chains {
		fmt.Fprintf(w, "- %s chain %s type %s hook %s priority %d policy %s: %s\n", render.Redact(chain.Action), render.Redact(chain.Name), render.Redact(chain.Type), render.Redact(chain.Hook), chain.Priority, render.Redact(chain.Policy), render.Redact(chain.Reason))
	}
	fmt.Fprintln(w, "Firewall rules:")
	for _, rule := range p.Rules {
		fmt.Fprintf(w, "- %s %s %s -> %s owner=%s rollback=%s: %s\n", render.Redact(rule.Action), render.Redact(rule.Chain), render.Redact(rule.Expr), render.Redact(rule.Verdict), render.Redact(rule.Ownership), render.Redact(rule.RollbackKey), render.Redact(rule.Reason))
	}
	fmt.Fprintf(w, "- recovery: %s\n", render.Redact(p.KillSwitch.Recovery))
	for _, limitation := range p.KillSwitch.Limitations {
		fmt.Fprintf(w, "- limitation: %s\n", render.Redact(limitation))
	}
	fmt.Fprintf(w, "- rollback: %s\n", render.Redact(p.Rollback))
}

func pTunNameFromRule(rule planner.TunFirewallRulePlan) string {
	const prefix = `oifname "`
	if strings.HasPrefix(rule.Expr, prefix) && strings.HasSuffix(rule.Expr, `"`) {
		return strings.TrimSuffix(strings.TrimPrefix(rule.Expr, prefix), `"`)
	}
	return "TUN"
}

func routePlanLine(r planner.TunRoutePlan) string {
	parts := []string{r.Action, r.Table, r.Destination}
	if r.Gateway != "" {
		parts = append(parts, "via", r.Gateway)
	}
	if r.Interface != "" {
		parts = append(parts, "dev", r.Interface)
	}
	return render.Redact(strings.Join(parts, " "))
}

func ruleLine(r planner.TunPolicyRulePlan) string {
	return render.Redact(fmt.Sprintf("%s priority %d %s lookup %s", r.Action, r.Priority, r.Selector, r.Table))
}

func renderRoute(r netsnapshot.Route) string {
	if r.Status == netsnapshot.StatusDetected {
		parts := []string{string(r.Status)}
		if r.Interface != "" {
			parts = append(parts, "dev "+r.Interface)
		}
		if r.Gateway != "" {
			parts = append(parts, "via "+r.Gateway)
		}
		if r.Raw != "" {
			parts = append(parts, "raw: "+r.Raw)
		}
		return render.Redact(strings.Join(parts, ", "))
	}
	return renderStatusDetail(r.Status, r.Detail, r.Raw)
}

func renderDefaultInterface(r netsnapshot.Route) string {
	if r.Status == netsnapshot.StatusDetected && r.Interface != "" {
		return render.Redact(r.Interface)
	}
	return renderStatusDetail(r.Status, r.Detail, r.Raw)
}

func renderNetworkManager(nm netsnapshot.NetworkManager) string {
	line := renderFinding(nm.Finding)
	if nm.State != "" {
		line += " state=" + nm.State
	}
	return render.Redact(line)
}

func renderFinding(f netsnapshot.Finding) string {
	return renderStatusDetail(f.Status, f.Summary, f.Detail)
}

func renderStatusDetail(status netsnapshot.Status, a, b string) string {
	parts := []string{string(status)}
	if a != "" {
		parts = append(parts, a)
	}
	if b != "" {
		parts = append(parts, b)
	}
	return render.Redact(strings.Join(parts, ": "))
}
