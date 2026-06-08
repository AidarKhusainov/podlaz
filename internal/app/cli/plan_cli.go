package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/tunwarden/internal/network/snapshot"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/render"
)

func runPlanCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) { printPlanHelp(stdout); return nil }
	parsed, err := parsePlanArgs(args); if err != nil { return err }
	store, err := profile.NewStore(opts.profileStorePath); if err != nil { return err }
	p, err := store.Get(parsed.profileID); if err != nil { return profileCommandError(err) }
	if parsed.mode == planner.ModeProxyOnly {
		plan, err := planner.PlanProxyOnly(p); if err != nil { return usageError("%s", err.Error()) }
		if parsed.jsonOutput { return writeJSON(stdout, proxyOnlyPlanJSON(plan)) }
		renderProxyOnlyPlan(stdout, plan); return nil
	}
	collect := opts.systemSnapshot; if collect == nil { collect = netsnapshot.Collect }
	plan, err := planner.PlanTun(p, collect(ctx, netsnapshot.Options{Server: p.Server, TunNames: []string{netsnapshot.DefaultTunName}}))
	if err != nil { return usageError("%s", err.Error()) }
	if parsed.jsonOutput { return writeJSON(stdout, tunPlanJSON(plan)) }
	renderTunPlan(stdout, plan); return nil
}

type planArgs struct { mode, profileID string; jsonOutput bool }

func parsePlanArgs(args []string) (planArgs, error) {
	var parsed planArgs
	for i := 0; i < len(args); i++ { arg := args[i]; value, inline := cutFlagValue(arg); switch {
	case arg == "--mode" || strings.HasPrefix(arg, "--mode="):
		v, next, err := flagValue("plan --mode", args, i, value, inline); if err != nil { return parsed, err }
		parsed.mode = strings.ToLower(strings.TrimSpace(v)); i = next
	case arg == "--json": parsed.jsonOutput = true
	default:
		if strings.HasPrefix(arg, "-") { return parsed, usageError("unsupported plan argument %q", arg) }
		if parsed.profileID != "" { return parsed, usageError("plan accepts exactly one profile id") }
		parsed.profileID = arg
	} }
	if parsed.mode == "" { return parsed, usageError("plan requires --mode proxy-only or tun") }
	if parsed.mode != planner.ModeProxyOnly && parsed.mode != planner.ModeTun { return parsed, usageError("unsupported plan mode %q", parsed.mode) }
	if parsed.profileID == "" { return parsed, usageError("plan requires a profile id") }
	return parsed, nil
}

func renderProxyOnlyPlan(w io.Writer, p planner.ProxyOnlyPlan) {
	fmt.Fprintln(w, "Proxy-only plan")
	fmt.Fprintf(w, "Profile: %s\nProfile ID: %s\nMode: %s\n", render.Redact(p.ProfileName), render.Redact(p.ProfileID), p.Mode)
	fmt.Fprintf(w, "Will generate runtime Xray config: %s\n", p.RuntimeConfigPath)
	for _, l := range p.Listeners { fmt.Fprintf(w, "Will listen on %s: %s\n", l.Protocol, l.Endpoint()) }
	fmt.Fprintln(w, planner.NoSystemNetworkingPlan)
	fmt.Fprintln(w, "Will not start Xray or write the generated config in this dry-run.")
	printWarnings(w, p.Warnings)
}

func renderTunPlan(w io.Writer, p planner.TunPlan) {
	s := p.Snapshot
	fmt.Fprintln(w, "TunWarden TUN plan")
	fmt.Fprintln(w, "TUN planning snapshot")
	fmt.Fprintf(w, "Profile: %s\nProfile ID: %s\nMode: %s\n", render.Redact(p.ProfileName), render.Redact(p.ProfileID), p.Mode)
	fmt.Fprintf(w, "Full-tunnel mode: %s\n", p.TunnelMode)
	fmt.Fprintln(w, "Read-only: will not create TUN devices, mutate routes, mutate policy rules, mutate DNS, mutate nftables, start Xray, or write runtime config.")
	fmt.Fprintf(w, "TUN: %s %s (MTU %d)\n", p.TunDevice.Action, render.Redact(p.TunDevice.Name), p.TunDevice.MTU)
	fmt.Fprintf(w, "Routing table: %s (%d)\n", planner.TunRoutingTable, planner.TunRoutingTableID)
	fmt.Fprintln(w, "Default traffic: route through tunwarden table")
	fmt.Fprintf(w, "VPN server bypass: %s\n", routePlanLine(p.ServerBypass))
	fmt.Fprintln(w, "Policy rules:"); for _, r := range p.PolicyRules { fmt.Fprintf(w, "- %s\n", ruleLine(r)) }
	fmt.Fprintln(w, "Routes:"); for _, r := range p.Routes { fmt.Fprintf(w, "- %s\n", routePlanLine(r)) }
	fmt.Fprintf(w, "Default IPv4 route: %s\n", renderRoute(s.DefaultIPv4))
	fmt.Fprintf(w, "Default interface: %s\n", renderDefaultInterface(s.DefaultIPv4))
	fmt.Fprintf(w, "Default IPv6 route: %s\n", renderRoute(s.DefaultIPv6))
	fmt.Fprintf(w, "Route to VPN server candidate: %s\n", renderRoute(s.ServerRoute))
	fmt.Fprintf(w, "DNS mode: %s (%s)\n", render.Redact(s.DNS.Mode), renderFinding(s.DNS.Resolved))
	fmt.Fprintf(w, "NetworkManager: %s\n", renderNetworkManager(s.NetworkManager))
	fmt.Fprintf(w, "nftables: %s\n", renderFinding(s.Nftables.Availability))
	fmt.Fprintf(w, "TunWarden nftables table: %s\n", renderFinding(s.Nftables.TunWardenTable))
	fmt.Fprintf(w, "IPv4 assumption: %s\nIPv6 assumption: %s\n", renderFinding(s.IPv4), renderFinding(s.IPv6))
	if len(s.StaleResources) == 0 { fmt.Fprintln(w, "Stale TunWarden-owned resources: none detected") } else { fmt.Fprintf(w, "Stale TunWarden-owned resources: %d detected\n", len(s.StaleResources)) }
	if len(p.LoopRisks) > 0 { fmt.Fprintf(w, "Route-loop risks: %d\n", len(p.LoopRisks)); for _, risk := range p.LoopRisks { fmt.Fprintf(w, "- %s\n", render.Redact(risk)) } }
	printWarnings(w, p.Warnings)
	fmt.Fprintln(w, "Rollback steps:"); for _, step := range p.RollbackSteps { fmt.Fprintf(w, "- %s\n", render.Redact(step)) }
	fmt.Fprintln(w, "No changes were applied.")
}

func printWarnings(w io.Writer, warnings []string) { if len(warnings) > 0 { fmt.Fprintf(w, "Warnings: %d\n", len(warnings)); for _, warning := range warnings { fmt.Fprintf(w, "- %s\n", render.Redact(warning)) } } }

func proxyOnlyPlanJSON(p planner.ProxyOnlyPlan) map[string]any {
	warnings := redactedStrings(p.Warnings); return map[string]any{"schema_version":"v1","status":jsonStatus(warnings),"warnings":warnings,"errors":[]string{},"mode":p.Mode,"plan":map[string]any{"runtime_config_path":p.RuntimeConfigPath,"listeners":listenersForJSON(p.Listeners),"writes_config":false,"starts_xray":false,"modifies_system_networking":false},"steps":redactedStrings(p.Steps),"rollback_steps":redactedStrings(p.RollbackSteps)}
}

func tunPlanJSON(p planner.TunPlan) map[string]any {
	warnings := redactedStrings(p.Warnings); return map[string]any{"schema_version":"v1","status":jsonStatus(warnings),"warnings":warnings,"errors":[]string{},"mode":p.Mode,"loop_risks":redactedStrings(p.LoopRisks),"plan":map[string]any{"tunnel_mode":p.TunnelMode,"writes_config":false,"starts_xray":false,"modifies_system_networking":false,"tun":map[string]any{"name":p.TunDevice.Name,"mtu":p.TunDevice.MTU,"action":p.TunDevice.Action},"routes":routesJSON(p.Routes),"policy_rules":rulesJSON(p.PolicyRules),"server_bypass":routePlanJSON(p.ServerBypass),"snapshot":snapshotJSON(p.Snapshot)},"steps":redactedStrings(p.Steps),"rollback_steps":redactedStrings(p.RollbackSteps)}
}

func jsonStatus(warnings []string) string { if len(warnings) > 0 { return "warn" }; return "ok" }
func listenersForJSON(v []planner.Listener) []map[string]any { out := make([]map[string]any,len(v)); for i,l := range v { out[i]=map[string]any{"protocol":strings.ToLower(l.Protocol),"address":l.Address,"port":l.Port} }; return out }
func routesJSON(v []planner.TunRoutePlan) []map[string]any { out := make([]map[string]any,len(v)); for i,r := range v { out[i]=routePlanJSON(r) }; return out }
func routePlanJSON(r planner.TunRoutePlan) map[string]any { return map[string]any{"family":r.Family,"destination":render.Redact(r.Destination),"table":r.Table,"interface":render.Redact(r.Interface),"gateway":render.Redact(r.Gateway),"action":r.Action} }
func rulesJSON(v []planner.TunPolicyRulePlan) []map[string]any { out := make([]map[string]any,len(v)); for i,r := range v { out[i]=map[string]any{"family":r.Family,"priority":r.Priority,"selector":render.Redact(r.Selector),"table":r.Table,"action":r.Action} }; return out }
func snapshotJSON(s netsnapshot.Snapshot) map[string]any { return map[string]any{"default_ipv4_route":routeJSON(s.DefaultIPv4),"default_ipv6_route":routeJSON(s.DefaultIPv6),"server_route":routeJSON(s.ServerRoute),"stale_resources":s.StaleResources} }
func routeJSON(r netsnapshot.Route) map[string]any { return map[string]any{"status":string(r.Status),"destination":render.Redact(r.Destination),"interface":render.Redact(r.Interface),"gateway":render.Redact(r.Gateway),"raw":render.Redact(r.Raw),"detail":render.Redact(r.Detail)} }
func routePlanLine(r planner.TunRoutePlan) string { parts := []string{r.Action,r.Table,r.Destination}; if r.Gateway!="" { parts=append(parts,"via",r.Gateway) }; if r.Interface!="" { parts=append(parts,"dev",r.Interface) }; return render.Redact(strings.Join(parts," ")) }
func ruleLine(r planner.TunPolicyRulePlan) string { return render.Redact(fmt.Sprintf("%s priority %d %s lookup %s", r.Action,r.Priority,r.Selector,r.Table)) }
func renderRoute(r netsnapshot.Route) string { if r.Status==netsnapshot.StatusDetected { parts:=[]string{string(r.Status)}; if r.Interface!="" { parts=append(parts,"dev "+r.Interface) }; if r.Gateway!="" { parts=append(parts,"via "+r.Gateway) }; if r.Raw!="" { parts=append(parts,"raw: "+r.Raw) }; return render.Redact(strings.Join(parts,", ")) }; return renderStatusDetail(r.Status,r.Detail,r.Raw) }
func renderDefaultInterface(r netsnapshot.Route) string { if r.Status==netsnapshot.StatusDetected && r.Interface!="" { return render.Redact(r.Interface) }; return renderStatusDetail(r.Status,r.Detail,r.Raw) }
func renderNetworkManager(nm netsnapshot.NetworkManager) string { line:=renderFinding(nm.Finding); if nm.State!="" { line += " state="+nm.State }; return render.Redact(line) }
func renderFinding(f netsnapshot.Finding) string { return renderStatusDetail(f.Status,f.Summary,f.Detail) }
func renderStatusDetail(status netsnapshot.Status, a,b string) string { parts:=[]string{string(status)}; if a!="" { parts=append(parts,a) }; if b!="" { parts=append(parts,b) }; return render.Redact(strings.Join(parts,": ")) }
func redactedStrings(values []string) []string { out:=make([]string,len(values)); for i,value := range values { out[i]=render.Redact(value) }; return out }

func printPlanHelp(w io.Writer) { fmt.Fprint(w, "Usage:\n  tunwarden plan --mode proxy-only <profile-id> [--json]\n  tunwarden plan --mode tun <profile-id> [--json]\n\nPrint a read-only connection plan. TUN mode renders a full-tunnel route dry-run plan.\n") }
