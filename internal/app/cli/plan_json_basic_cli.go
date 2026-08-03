package cli

import (
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

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

func tunAddressJSON(a planner.TunAddressPlan) map[string]any {
	return map[string]any{
		"family":         render.Redact(a.Family),
		"interface":      render.Redact(a.Interface),
		"cidr":           render.Redact(a.CIDR),
		"action":         render.Redact(a.Action),
		"reason":         render.Redact(a.Reason),
		"classification": render.Redact(a.Classification),
		"owner":          render.Redact(a.Owner),
		"rollback_key":   render.Redact(a.RollbackKey),
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

func redactedStrings(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = render.Redact(v)
	}
	return out
}
