package cli

import (
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

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
