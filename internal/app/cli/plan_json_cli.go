package cli

import (
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
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
			"runtime_config_path":        p.RuntimeConfigPath,
			"listeners":                  listenersForJSON(p.Listeners),
			"writes_config":              false,
			"starts_xray":                false,
			"modifies_system_networking": false,
			"system_networking":          "will not modify TUN, routes, DNS, nftables, or firewall",
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
			"tunnel_mode":                p.TunnelMode,
			"writes_config":              false,
			"starts_xray":                false,
			"modifies_system_networking": false,
			"tun":                        tunDeviceJSON(p.TunDevice),
			"tun_address":                tunAddressJSON(p.TunAddress),
			"routes":                     routesJSON(p.Routes),
			"policy_rules":               rulesJSON(p.PolicyRules),
			"server_bypass":              routePlanJSON(p.ServerBypass),
			"dns":                        dnsPlanJSON(p.DNS),
			"firewall":                   firewallPlanJSON(p.Firewall),
			"snapshot":                   snapshotForJSON(p.Snapshot),
			"claims_leak_protection":     false,
		},
		"steps":          redactedStrings(p.Steps),
		"rollback_steps": redactedStrings(p.RollbackSteps),
	}
}
