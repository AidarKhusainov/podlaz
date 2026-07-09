package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

func renderProxyOnlyPlan(w io.Writer, p planner.ProxyOnlyPlan) {
	fmt.Fprintln(w, "Proxy-only plan")
	fmt.Fprintf(w, "Profile: %s\nProfile ID: %s\nMode: %s\n", render.Redact(p.ProfileName), render.Redact(p.ProfileID), p.Mode)
	fmt.Fprintf(w, "Will generate runtime Xray config: %s\n", p.RuntimeConfigPath)
	for _, l := range p.Listeners {
		fmt.Fprintf(w, "Will listen on %s: %s\n", l.Protocol, l.Endpoint())
	}
	fmt.Fprintln(w, planner.NoSystemNetworkingPlan)
	fmt.Fprintln(w, "Will not start Xray or write the generated config in this dry-run.")
	printWarnings(w, p.Warnings)
}

func renderTunPlanSummary(w io.Writer, p planner.TunPlan, profileID string, plain bool) {
	marks := outputStatusMarks(plain)
	blockers := tunPlanBlockers(p)
	localDryRunLimitations := tunPlanLocalDryRunLimitations(p)
	warnings := tunPlanHumanWarnings(p)
	commandID := safeCommandProfileID(profileID)

	fmt.Fprintln(w, "podlaz plan")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Profile")
	renderAlignedField(w, "Name", p.ProfileName)
	renderAlignedField(w, "Mode", humanModeLabel(p.TunnelMode))
	renderAlignedField(w, "Backend", "Xray / "+humanProtocolLabel("VLESS"))
	renderAlignedField(w, "Status", tunPlanHumanStatus(blockers, localDryRunLimitations, warnings))

	fmt.Fprintln(w)
	fmt.Fprintln(w, "What will happen")
	renderPlanAction(w, marks.OK, "Create TUN interface", fmt.Sprintf("%s, MTU %d", p.TunDevice.Name, p.TunDevice.MTU))
	renderPlanAction(w, marks.OK, "Route traffic through VPN", "default IPv4 route via podlaz table")
	renderServerBypassSummary(w, marks, p.ServerBypass)
	renderDNSSummary(w, marks, p.DNS)
	renderFirewallSummary(w, marks, p.Firewall)

	if len(blockers) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Blockers")
		for _, blocker := range blockers {
			fmt.Fprintf(w, "  %s %s\n", marks.Blocked, render.Redact(blocker))
		}
	}
	if len(localDryRunLimitations) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Local dry-run limitations")
		for _, limitation := range localDryRunLimitations {
			fmt.Fprintf(w, "  %s %s\n", marks.Warn, render.Redact(limitation))
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Warnings")
		for _, warning := range warnings {
			fmt.Fprintf(w, "  %s %s\n", marks.Warn, render.Redact(warning))
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Safety")
	fmt.Fprintln(w, "  No changes were applied.")
	fmt.Fprintln(w, "  If connect fails, podlaz can roll back TUN, routes, DNS and nftables state.")

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Next steps")
	if len(blockers) > 0 {
		fmt.Fprintln(w, "  Run: plz doctor")
	} else {
		fmt.Fprintf(w, "  Run: plz connect --mode tun %s\n", commandID)
	}
	fmt.Fprintf(w, "  Details: plz plan --mode tun %s --verbose\n", commandID)
}

func renderPlanAction(w io.Writer, mark, label, detail string) {
	fmt.Fprintf(w, "  %s %-28s %s\n", mark, label, render.Redact(detail))
}

func renderServerBypassSummary(w io.Writer, marks humanStatusMarks, p planner.TunRoutePlan) {
	if p.Action == "add" && p.Destination != "" {
		detail := p.Destination
		if p.Gateway != "" {
			detail += " via " + p.Gateway
		}
		if p.Interface != "" {
			detail += " dev " + p.Interface
		}
		renderPlanAction(w, marks.OK, "Keep VPN server reachable", detail)
		return
	}
	renderPlanAction(w, marks.Blocked, "Keep VPN server reachable", "blocked: "+humanPlanDetail(p.Reason))
}

func renderDNSSummary(w io.Writer, marks humanStatusMarks, p planner.TunDNSPlan) {
	if p.Action == planner.DNSActionConfigure {
		renderPlanAction(w, marks.OK, "Configure DNS", fmt.Sprintf("systemd-resolved, %s", strings.Join(p.Servers, ", ")))
		return
	}
	renderPlanAction(w, marks.Blocked, "Configure DNS", "blocked: "+humanPlanDetail(p.Reason))
}

func renderFirewallSummary(w io.Writer, marks humanStatusMarks, p planner.TunFirewallPlan) {
	if p.TableAction == planner.FirewallActionBlocked {
		renderPlanAction(w, marks.Blocked, "Configure kill switch", "blocked: "+humanPlanDetail(p.Reason))
		return
	}
	if p.KillSwitch.Policy == planner.KillSwitchPolicyOff {
		renderPlanAction(w, marks.Skip, "Configure kill switch", "disabled by policy")
		return
	}
	renderPlanAction(w, marks.OK, "Configure kill switch", "reject non-VPN traffic during connection setup")
}

func tunPlanHumanStatus(blockers, localDryRunLimitations, warnings []string) string {
	if len(blockers) > 0 {
		return "Blocked"
	}
	if len(localDryRunLimitations) > 0 {
		return "Ready for daemon re-check"
	}
	if len(warnings) > 0 {
		return "Ready with warnings"
	}
	return "Ready"
}

func tunPlanBlockers(p planner.TunPlan) []string {
	var blockers []string
	if p.ServerBypass.Action != "" && p.ServerBypass.Action != "add" {
		blockers = append(blockers, "VPN server bypass cannot be prepared yet: "+humanPlanDetail(p.ServerBypass.Reason))
	}
	if p.DNS.Action == planner.DNSActionBlocked {
		blockers = append(blockers, "DNS cannot be configured yet: "+humanPlanDetail(p.DNS.Reason))
	}
	if p.Firewall.TableAction == planner.FirewallActionBlocked && !tunPlanFirewallBlockedForLocalDryRun(p.Firewall) {
		blockers = append(blockers, "Kill switch cannot be prepared yet: "+humanPlanDetail(p.Firewall.Reason))
	}
	return blockers
}

func tunPlanLocalDryRunLimitations(p planner.TunPlan) []string {
	if tunPlanFirewallBlockedForLocalDryRun(p.Firewall) {
		return []string{"Kill switch could not be fully planned in this local dry-run: " + humanPlanDetail(p.Firewall.Reason)}
	}
	return nil
}

func tunPlanFirewallBlockedForLocalDryRun(p planner.TunFirewallPlan) bool {
	if p.TableAction != planner.FirewallActionBlocked {
		return false
	}
	lower := strings.ToLower(p.Reason)
	if strings.Contains(lower, "availability is missing") || strings.Contains(lower, "availability is unsupported") {
		return false
	}
	return strings.Contains(lower, "availability is unknown") ||
		strings.Contains(lower, "operation not permitted") ||
		strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "not readable") ||
		strings.Contains(lower, "current user")
}

func tunPlanHumanWarnings(p planner.TunPlan) []string {
	seen := map[string]struct{}{}
	var warnings []string
	for _, warning := range append(append([]string{}, p.Warnings...), p.LoopRisks...) {
		message := humanPlanDetail(warning)
		if message == "" {
			continue
		}
		if _, ok := seen[message]; ok {
			continue
		}
		seen[message] = struct{}{}
		warnings = append(warnings, message)
	}
	return warnings
}

func humanPlanDetail(value string) string {
	value = humanSingleLine(render.Redact(value))
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "nftables") && strings.Contains(lower, "availability is missing"):
		return "nftables is not available. Install or enable nftables before applying TUN firewall or kill-switch rules."
	case strings.Contains(lower, "nftables") && strings.Contains(lower, "availability is unsupported"):
		return "nftables is not supported on this host. TUN firewall and kill-switch rules cannot be applied safely."
	case strings.Contains(lower, "nftables") && (strings.Contains(lower, "operation not permitted") || strings.Contains(lower, "not readable") || strings.Contains(lower, "inspect")):
		return "nftables cannot be inspected as current user. The daemon will check this again before applying changes."
	case strings.Contains(lower, "ipv6") && strings.Contains(lower, "default route"):
		return "IPv6 default route was not found. IPv6 will stay disabled/bypassed for this TUN plan."
	case strings.Contains(lower, "systemd-resolved") && strings.Contains(lower, "unsafe"):
		return "systemd-resolved state is not readable. The daemon will check DNS again before applying changes."
	}
	for _, marker := range []string{"; stderr:", ", stderr:", " stderr:"} {
		if idx := strings.Index(strings.ToLower(value), marker); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
			break
		}
	}
	return value
}

func printWarnings(w io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintf(w, "Warnings: %d\n", len(warnings))
	for _, warning := range warnings {
		fmt.Fprintf(w, "- %s\n", render.Redact(warning))
	}
}
