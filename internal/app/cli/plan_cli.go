package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/render"
)

func runPlanCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	_ = ctx
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

	proxyPlan, err := planner.PlanProxyOnly(p)
	if err != nil {
		return usageError("%s", err.Error())
	}
	if parsed.jsonOutput {
		return writeJSON(stdout, proxyOnlyPlanJSON(proxyPlan))
	}
	renderProxyOnlyPlan(stdout, proxyPlan)
	return nil
}

type planArgs struct {
	mode       string
	profileID  string
	jsonOutput bool
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
		return parsed, usageError("plan requires --mode proxy-only")
	}
	if parsed.mode != planner.ModeProxyOnly {
		return parsed, usageError("unsupported plan mode %q", parsed.mode)
	}
	if parsed.profileID == "" {
		return parsed, usageError("plan requires a profile id")
	}
	return parsed, nil
}

func renderProxyOnlyPlan(stdout io.Writer, proxyPlan planner.ProxyOnlyPlan) {
	fmt.Fprintln(stdout, "Proxy-only plan")
	fmt.Fprintf(stdout, "Profile: %s\n", render.Redact(proxyPlan.ProfileName))
	fmt.Fprintf(stdout, "Profile ID: %s\n", render.Redact(proxyPlan.ProfileID))
	fmt.Fprintf(stdout, "Mode: %s\n", proxyPlan.Mode)
	fmt.Fprintf(stdout, "Will generate runtime Xray config: %s\n", proxyPlan.RuntimeConfigPath)
	for _, listener := range proxyPlan.Listeners {
		fmt.Fprintf(stdout, "Will listen on %s: %s\n", listener.Protocol, listener.Endpoint())
	}
	fmt.Fprintln(stdout, planner.NoSystemNetworkingPlan)
	fmt.Fprintln(stdout, "Will not start Xray or write the generated config in this dry-run.")
	if len(proxyPlan.Warnings) > 0 {
		fmt.Fprintf(stdout, "Warnings: %d\n", len(proxyPlan.Warnings))
		for _, warning := range proxyPlan.Warnings {
			fmt.Fprintf(stdout, "- %s\n", render.Redact(warning))
		}
	}
}

func proxyOnlyPlanJSON(proxyPlan planner.ProxyOnlyPlan) map[string]any {
	warnings := redactedStrings(proxyPlan.Warnings)
	status := "ok"
	if len(warnings) > 0 {
		status = "warn"
	}
	return map[string]any{
		"schema_version": "v1",
		"status":         status,
		"warnings":       warnings,
		"errors":         []string{},
		"mode":           proxyPlan.Mode,
		"plan": map[string]any{
			"profile": map[string]any{
				"id":   render.Redact(proxyPlan.ProfileID),
				"name": render.Redact(proxyPlan.ProfileName),
			},
			"runtime_config_path":        proxyPlan.RuntimeConfigPath,
			"listeners":                  listenersForJSON(proxyPlan.Listeners),
			"writes_config":              false,
			"starts_xray":                false,
			"modifies_system_networking": false,
			"system_networking":          "will not modify TUN, routes, DNS, nftables, or firewall",
		},
		"steps":          redactedStrings(proxyPlan.Steps),
		"rollback_steps": redactedStrings(proxyPlan.RollbackSteps),
	}
}

func listenersForJSON(listeners []planner.Listener) []map[string]any {
	out := make([]map[string]any, len(listeners))
	for i, listener := range listeners {
		out[i] = map[string]any{
			"protocol": strings.ToLower(listener.Protocol),
			"address":  listener.Address,
			"port":     listener.Port,
		}
	}
	return out
}

func redactedStrings(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = render.Redact(value)
	}
	return out
}

func printPlanHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden plan --mode proxy-only <profile-id> [--json]

Print the read-only proxy-only runtime plan for a stored profile. The command
builds an inspectable generated Xray config in memory, shows the local SOCKS and
HTTP listeners that would be used, and does not start Xray or mutate host
networking state.

Implemented in v0.1:
  proxy-only plans for supported stored VLESS profiles, human output, JSON
  output, deterministic generated Xray config fixtures, and explicit no TUN,
  route, DNS, nftables, or firewall mutation.

Not implemented yet:
  writing generated config files, Xray binary discovery/version checks, Xray
  process start, full-tunnel planning
`)
}
