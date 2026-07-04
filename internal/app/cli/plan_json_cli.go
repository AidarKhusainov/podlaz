package cli

import (
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

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

func redactedStrings(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = render.Redact(v)
	}
	return out
}
