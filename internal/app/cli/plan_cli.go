package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

func runPlanCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printPlanHelp(stdout)
		return nil
	}
	parsed, err := parsePlanArgs(args)
	if err != nil {
		return err
	}
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}
	p, err := store.Get(parsed.profileID)
	if err != nil {
		return profileCommandError(err)
	}
	if parsed.mode == planner.ModeProxyOnly {
		plan, err := planner.PlanProxyOnly(p)
		if err != nil {
			return usageError("%s", err.Error())
		}
		if parsed.jsonOutput {
			return writeJSON(stdout, proxyOnlyPlanJSON(plan))
		}
		renderProxyOnlyPlan(stdout, plan)
		return nil
	}
	if err := engine.ValidateXrayTunProfile(p); err != nil {
		return usageError("%s", err.Error())
	}
	collect := opts.systemSnapshot
	if collect == nil {
		collect = netsnapshot.Collect
	}
	plan, err := planner.PlanTun(p, collect(ctx, netsnapshot.Options{
		Server:   p.Server,
		TunNames: []string{netsnapshot.DefaultTunName},
	}))
	if err != nil {
		return usageError("%s", err.Error())
	}
	if parsed.jsonOutput {
		return writeJSON(stdout, tunPlanJSON(plan))
	}
	if parsed.verbose {
		renderTunPlanVerbose(stdout, plan)
		return nil
	}
	renderTunPlanSummary(stdout, plan, parsed.profileID, parsed.plainOutput)
	return nil
}

type planArgs struct {
	mode        string
	profileID   string
	jsonOutput  bool
	verbose     bool
	plainOutput bool
}

func parsePlanArgs(args []string) (planArgs, error) {
	var parsed planArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasInlineValue := cutFlagValue(arg)
		switch {
		case arg == "--mode" || strings.HasPrefix(arg, "--mode="):
			v, next, err := flagValue("plan --mode", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.mode = strings.ToLower(strings.TrimSpace(v))
			i = next
		case arg == "--json":
			parsed.jsonOutput = true
		case arg == "--verbose" || arg == "-v":
			parsed.verbose = true
		case arg == "--plain":
			parsed.plainOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				return parsed, usageError("unsupported plan argument %q", arg)
			}
			if parsed.profileID != "" {
				return parsed, usageError("plan accepts exactly one profile id")
			}
			parsed.profileID = arg
		}
	}
	if parsed.mode == "" {
		return parsed, usageError("plan requires --mode proxy-only or tun")
	}
	if parsed.mode != planner.ModeProxyOnly && parsed.mode != planner.ModeTun {
		return parsed, usageError("unsupported plan mode %q", parsed.mode)
	}
	if parsed.profileID == "" {
		return parsed, usageError("plan requires a profile id")
	}
	return parsed, nil
}

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
	blocked := tunPlanBlockers(p)
	warnings := tunPlanHumanWarnings(p)
	commandID := safeCommandProfileID(profileID)

	fmt.Fprintln(w, "podlaz plan")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Profile")
	renderAlignedField(w, "Name", p.ProfileName)
	renderAlignedField(w, "Mode", humanModeLabel(p.TunnelMode))
	renderAlignedField(w, "Backend", "Xray / "+humanProtocolLabel("VLESS"))
	renderAlignedField(w, "Status", tunPlanHumanStatus(blocked, warnings))

	fmt.Fprintln(w)
	fmt.Fprintln(w, "What will happen")
	renderPlanAction(w, marks.OK, "Create TUN interface", fmt.Sprintf("%s, MTU %d", p.TunDevice.Name, p.TunDevice.MTU))
	renderPlanAction(w, marks.OK, "Route traffic through VPN", "default IPv4 route via podlaz table")
	renderServerBypassSummary(w, marks, p.ServerBypass)
	renderDNSSummary(w, marks, p.DNS)
	renderFirewallSummary(w, marks, p.Firewall)

	if len(blocked) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Blockers")
		for _, blocker := range blocked {
			fmt.Fprintf(w, "  %s %s\n", marks.Blocked, render.Redact(blocker))
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
	if len(blocked) > 0 {
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

func tunPlanHumanStatus(blockers, warnings []string) string {
	if len(blockers) > 0 {
		return "Blocked"
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
	if p.Firewall.TableAction == planner.FirewallActionBlocked {
		blockers = append(blockers, "Kill switch cannot be prepared yet: "+humanPlanDetail(p.Firewall.Reason))
	}
	return blockers
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

func printWarnings(w io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintf(w, "Warnings: %d\n", len(warnings))
	for _, warning := range warnings {
		fmt.Fprintf(w, "- %s\n", render.Redact(warning))
	}
}

func proxyOnlyPlanJSON(p planner.ProxyOnlyPlan) map[string]any {
	warnings := redactedStrings(p.Warnings)
	return map[string]any{
		"schema_version": "v1",
		"status":         jsonStatus(warnings),
		"warnings":       warnings,
		"errors":         []string{},
		"mode":           p.Mode,
		"plan": map[string]any{
			"profile": map[string]any{
				"id":   render.Redact(p.ProfileID),
				"name": render.Redact(p.ProfileName),
			},
			"runtime_config_path":       p.RuntimeConfigPath,
			"listeners":                 listenersForJSON(p.Listeners),
			"writes_config":             false,
			"starts_xray":               false,
			"modifies_system_networking": false,
			"system_networking":         "will not modify TUN, routes, DNS, nftables, or firewall",
		},
		"steps":          redactedStrings(p.Steps),
		"rollback_steps": redactedStrings(p.RollbackSteps),
	}
}

func tunPlanJSON(p planner.TunPlan) map[string]any {
	warnings := redactedStrings(p.Warnings)
	return map[string]any{
		"schema_version": "v1",
		"status":         jsonStatus(warnings),
		"warnings":       warnings,
		"errors":         []string{},
		"mode":           p.Mode,
		"loop_risks":     redactedStrings(p.LoopRisks),
		"plan": map[string]any{
			"profile": map[string]any{
				"id":   render.Redact(p.ProfileID),
				"name": render.Redact(p.ProfileName),
			},
			"tunnel_mode":               p.TunnelMode,
			"writes_config":             false,
			"starts_xray":               false,
			"modifies_system_networking": false,
			"tun":                       tunDeviceJSON(p.TunDevice),
			"routes":                    routesJSON(p.Routes),
			"policy_rules":              rulesJSON(p.PolicyRules),
			"server_bypass":             routePlanJSON(p.ServerBypass),
			"dns":                       dnsPlanJSON(p.DNS),
			"firewall":                  firewallPlanJSON(p.Firewall),
			"snapshot":                  snapshotForJSON(p.Snapshot),
			"claims_leak_protection":     false,
		},
		"steps":          redactedStrings(p.Steps),
		"rollback_steps": redactedStrings(p.RollbackSteps),
	}
}

func jsonStatus(warnings []string) string {
	if len(warnings) > 0 {
		return "warn"
	}
	return "ok"
}

func listenersForJSON(v []planner.Listener) []map[string]any {
	out := make([]map[string]any, len(v))
	for i, l := range v {
		out[i] = map[string]any{
			"protocol": strings.ToLower(l.Protocol),
			"address":  l.Address,
			"port":     l.Port,
		}
	}
	return out
}

func tunDeviceJSON(d planner.TunDevicePlan) map[string]any {
	return map[string]any{
		"name":   render.Redact(d.Name),
		"mtu":    d.MTU,
		"action": render.Redact(d.Action),
		"reason": render.Redact(d.Reason),
	}
}

func routesJSON(v []planner.TunRoutePlan) []map[string]any {
	out := make([]map[string]any, len(v))
	for i, r := range v {
		out[i] = routePlanJSON(r)
	}
	return out
}

func routePlanJSON(r planner.TunRoutePlan) map[string]any {
	return map[string]any{
		"family":      render.Redact(r.Family),
		"destination": render.Redact(r.Destination),
		"table":       render.Redact(r.Table),
		"interface":   render.Redact(r.Interface),
		"gateway":     render.Redact(r.Gateway),
		"action":      render.Redact(r.Action),
		"reason":      render.Redact(r.Reason),
	}
}

func rulesJSON(v []planner.TunPolicyRulePlan) []map[string]any {
	out := make([]map[string]any, len(v))
	for i, r := range v {
		out[i] = map[string]any{
			"family":   render.Redact(r.Family),
			"priority": r.Priority,
			"selector": render.Redact(r.Selector),
			"table":    render.Redact(r.Table),
			"action":   render.Redact(r.Action),
			"reason":   render.Redact(r.Reason),
		}
	}
	return out
}

func dnsPlanJSON(p planner.TunDNSPlan) map[string]any {
	return map[string]any{
		"backend":           render.Redact(p.Backend),
		"target_link":       render.Redact(p.TargetLink),
		"servers":           redactedStrings(p.Servers),
		"route_only_domain": "~.",
		"default_route":     true,
		"action":            render.Redact(p.Action),
		"reason":            render.Redact(p.Reason),
		"rollback":          render.Redact(p.Rollback),
		"rollback_steps":    redactedStrings(p.RollbackSteps),
	}
}

func firewallPlanJSON(p planner.TunFirewallPlan) map[string]any {
	return map[string]any{
		"backend":        render.Redact(p.Backend),
		"family":         render.Redact(p.Family),
		"table":          render.Redact(p.Table),
		"table_action":   render.Redact(p.TableAction),
		"chains":         firewallChainsJSON(p.Chains),
		"rules":          firewallRulesJSON(p.Rules),
		"kill_switch":    killSwitchPlanJSON(p.KillSwitch),
		"reason":         render.Redact(p.Reason),
		"rollback":       render.Redact(p.Rollback),
		"rollback_steps": redactedStrings(p.RollbackSteps),
	}
}

func firewallChainsJSON(v []planner.TunFirewallChainPlan) []map[string]any {
	out := make([]map[string]any, len(v))
	for i, chain := range v {
		out[i] = map[string]any{
			"name":     render.Redact(chain.Name),
			"type":     render.Redact(chain.Type),
			"hook":     render.Redact(chain.Hook),
			"priority": chain.Priority,
			"policy":   render.Redact(chain.Policy),
			"action":   render.Redact(chain.Action),
			"reason":   render.Redact(chain.Reason),
		}
	}
	return out
}

func firewallRulesJSON(v []planner.TunFirewallRulePlan) []map[string]any {
	out := make([]map[string]any, len(v))
	for i, rule := range v {
		out[i] = map[string]any{
			"chain":        render.Redact(rule.Chain),
			"expr":         render.Redact(rule.Expr),
			"verdict":      render.Redact(rule.Verdict),
			"action":       render.Redact(rule.Action),
			"reason":       render.Redact(rule.Reason),
			"ownership":    render.Redact(rule.Ownership),
			"rollback_key": render.Redact(rule.RollbackKey),
		}
	}
	return out
}

func killSwitchPlanJSON(p planner.TunKillSwitchPlan) map[string]any {
	return map[string]any{
		"policy":      render.Redact(p.Policy),
		"action":      render.Redact(p.Action),
		"scope":       render.Redact(p.Scope),
		"recovery":    render.Redact(p.Recovery),
		"limitations": redactedStrings(p.Limitations),
	}
}

func snapshotForJSON(s netsnapshot.Snapshot) map[string]any {
	return map[string]any{
		"os":                 render.Redact(s.OS),
		"default_ipv4_route": routeForJSON(s.DefaultIPv4),
		"default_ipv6_route": routeForJSON(s.DefaultIPv6),
		"server_route":       routeForJSON(s.ServerRoute),
		"dns": map[string]any{
			"mode":             render.Redact(s.DNS.Mode),
			"systemd_resolved": findingForJSON(s.DNS.Resolved),
		},
		"network_manager": map[string]any{
			"finding": findingForJSON(s.NetworkManager.Finding),
			"state":   render.Redact(s.NetworkManager.State),
		},
		"nftables": map[string]any{
			"availability": findingForJSON(s.Nftables.Availability),
			"podlaz_table": findingForJSON(s.Nftables.PodlazTable),
		},
		"tun_devices":     tunDevicesForJSON(s.TunDevices),
		"ipv4":            findingForJSON(s.IPv4),
		"ipv6":            findingForJSON(s.IPv6),
		"stale_resources": staleResourcesForJSON(s.StaleResources),
	}
}

func routeForJSON(r netsnapshot.Route) map[string]any {
	return map[string]any{
		"status":      string(r.Status),
		"family":      render.Redact(r.Family),
		"destination": render.Redact(r.Destination),
		"interface":   render.Redact(r.Interface),
		"gateway":     render.Redact(r.Gateway),
		"raw":         render.Redact(r.Raw),
		"detail":      render.Redact(r.Detail),
	}
}

func findingForJSON(f netsnapshot.Finding) map[string]any {
	return map[string]any{
		"status":  string(f.Status),
		"summary": render.Redact(f.Summary),
		"detail":  render.Redact(f.Detail),
	}
}

func tunDevicesForJSON(v []netsnapshot.TunDevice) []map[string]any {
	out := make([]map[string]any, len(v))
	for i, d := range v {
		out[i] = map[string]any{
			"name":   render.Redact(d.Name),
			"status": string(d.Status),
			"detail": render.Redact(d.Detail),
			"raw":    render.Redact(d.Raw),
		}
	}
	return out
}

func staleResourcesForJSON(v []netsnapshot.StaleResource) []map[string]any {
	out := make([]map[string]any, len(v))
	for i, r := range v {
		out[i] = map[string]any{
			"kind":   render.Redact(r.Kind),
			"name":   render.Redact(r.Name),
			"status": string(r.Status),
			"detail": render.Redact(r.Detail),
		}
	}
	return out
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

func redactedStrings(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = render.Redact(v)
	}
	return out
}

func printPlanHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz plan --mode proxy-only <profile-id> [--json]
  podlaz plan --mode tun <profile-id> [--json] [--verbose|-v] [--plain]

Print a read-only connection plan. TUN planning defaults to a compact human summary. Use --verbose for the full TUN/route/DNS/nftables kill-switch dry-run plan with server bypass, route-loop risk, warnings, and rollback steps. Use --plain for ASCII status markers in human output.
`)
}
