package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func buildHardenedTunDiagnosticAdapters(input tunDiagnosticInput) tundiag.ProbeAdapters {
	adapters := buildTunDiagnosticAdapters(input)
	client := tundiag.NetworkClient{}
	adapters.ServerBypass = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunServerBypassPath(ctx, input.plan)
	}
	adapters.HTTPSCloudflare = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunHTTPSEvidence(ctx, client, "https-cloudflare-small", tundiag.ClassHTTPSFailure)
	}
	adapters.HTTPSGoogle = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunHTTPSEvidence(ctx, client, "https-google-small", tundiag.ClassHTTPSFailure)
	}
	adapters.PMTUCloudflare = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunHTTPSEvidence(ctx, client, "pmtu-cloudflare-16k", tundiag.ClassHTTPSFailure)
	}
	adapters.PMTUHetzner = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunHTTPSEvidence(ctx, client, "pmtu-hetzner-16k", tundiag.ClassHTTPSFailure)
	}
	adapters.IPv6 = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunIPv6Path(ctx, client, input.plan, input.snapshot)
	}
	return adapters
}

func probeTunServerBypassPath(ctx context.Context, plan planner.TunPlan) tundiag.ProbeResult {
	bypass := tunDiagnosticServerBypass(plan)
	target := strings.TrimSuffix(strings.TrimSpace(bypass.Destination), "/32")
	if net.ParseIP(target) == nil {
		return diagnosticFailure(tundiag.ClassServerBypassFailure, "transaction has no concrete VPN server bypass address")
	}
	route, routeCommand, routeErr := lookupTunRoute(ctx, "-4", target)
	ruleResult, ruleErr := tunDiagnosticCommandRunner(ctx, "ip", "-4", "rule", "show")
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{
		Route:    &route,
		Commands: []tundiag.CommandEvidence{routeCommand, commandEvidence(ruleResult)},
	}}
	if routeErr != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassServerBypassFailure
		result.Error = routeErr.Error()
		return result
	}
	if ruleErr != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassPolicyRuleFailure
		result.Error = "inspect VPN server bypass policy rule: " + ruleErr.Error()
		return result
	}
	rule := tunDiagnosticHardenedServerBypassRule(ruleResult.stdout, target)
	if rule == "" {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassPolicyRuleFailure
		result.Error = fmt.Sprintf("missing priority %d rule routing VPN server %s through main", planner.ServerRulePriority, target)
		return result
	}
	result.Evidence.PolicyRules = []string{rule}
	route.Rule = rule
	if route.Table == "" {
		route.Table = planner.MainRoutingTable
	}
	if route.Table != planner.MainRoutingTable {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassServerBypassFailure
		result.Error = fmt.Sprintf("VPN server route uses table %s; expected main", route.Table)
		return result
	}
	if route.Interface == netsnapshot.DefaultTunName || (bypass.Interface != "" && route.Interface != bypass.Interface) {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassServerBypassFailure
		result.Error = fmt.Sprintf("VPN server route uses interface %s; expected physical interface %s", route.Interface, emptyAs(bypass.Interface, "outside podlaz0"))
		return result
	}
	if bypass.Gateway != "" && route.Gateway != bypass.Gateway {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassServerBypassFailure
		result.Error = fmt.Sprintf("VPN server route uses gateway %s; expected %s", emptyAs(route.Gateway, "direct"), bypass.Gateway)
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func tunDiagnosticHardenedServerBypassRule(output, target string) string {
	priority := strconv.Itoa(planner.ServerRulePriority) + ":"
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, priority) {
			continue
		}
		fields := strings.Fields(line)
		targetOK := false
		mainOK := false
		for i := 0; i < len(fields)-1; i++ {
			switch fields[i] {
			case "to":
				targetOK = strings.TrimSuffix(fields[i+1], "/32") == target
			case "lookup", "table":
				mainOK = fields[i+1] == planner.MainRoutingTable
			}
		}
		if targetOK && mainOK {
			return line
		}
	}
	return ""
}

func probeTunHTTPSEvidence(ctx context.Context, client tundiag.NetworkClient, targetID string, classification tundiag.Classification) tundiag.ProbeResult {
	target, ok := tundiag.FindTarget(targetID)
	if !ok {
		return diagnosticFailure(tundiag.ClassInternalDiagnosticError, "missing endpoint catalog target "+targetID)
	}
	evidence, err := client.HTTPSWithEvidence(ctx, target)
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{Endpoint: target.URL, HTTP: &evidence}}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = classification
		result.Error = err.Error()
		return result
	}
	if target.Kind == tundiag.TargetPMTU && evidence.BytesRead < target.MaxResponseBytes {
		evidence.FailurePhase = "short_body"
		result.Evidence.HTTP = &evidence
		result.Status = tundiag.ProbeFail
		result.Classification = classification
		result.Error = fmt.Sprintf("bounded transfer returned %d of %d requested bytes", evidence.BytesRead, target.MaxResponseBytes)
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunIPv6Path(ctx context.Context, client tundiag.NetworkClient, plan planner.TunPlan, snapshot netsnapshot.Snapshot) tundiag.ProbeResult {
	addressResult, addressErr := tunDiagnosticCommandRunner(ctx, "ip", "-6", "-brief", "address", "show")
	ruleResult, ruleErr := tunDiagnosticCommandRunner(ctx, "ip", "-6", "rule", "show")
	tunName := emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName)
	uplinkAddresses, tunAddresses := parseTunDiagnosticGlobalIPv6Addresses(addressResult.stdout, snapshot.DefaultIPv4.Interface, tunName)
	evidence := tundiag.IPv6Evidence{
		State:            string(snapshot.IPv6.Status),
		DefaultInterface: snapshot.DefaultIPv6.Interface,
		RouteTable:       snapshot.DefaultIPv6.Raw,
		UplinkAddresses:  uplinkAddresses,
		TunAddresses:     tunAddresses,
	}
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{
		IPv6:        &evidence,
		Commands:    []tundiag.CommandEvidence{commandEvidence(addressResult), commandEvidence(ruleResult)},
		PolicyRules: nonEmptyLines(ruleResult.stdout),
	}}
	if snapshot.IPv6.Status != netsnapshot.StatusDetected && len(uplinkAddresses) == 0 && len(tunAddresses) == 0 {
		evidence.State = "not_present"
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6NotPresent
		result.Error = "host has no detected global-unicast IPv6 path"
		return result
	}
	if addressErr != nil || ruleErr != nil {
		evidence.State = "unusable"
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = errors.Join(addressErr, ruleErr).Error()
		return result
	}
	if snapshot.DefaultIPv6.Status != netsnapshot.StatusDetected {
		evidence.State = "unusable"
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "IPv6 is present but no usable default route was detected"
		return result
	}
	addresses, err := client.Resolve(ctx, "www.cloudflare.com")
	if err != nil {
		evidence.State = "unusable"
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "resolve IPv6 diagnostic target: " + err.Error()
		return result
	}
	address := preferredAddress(addresses, true)
	result.Evidence.ResolvedAddresses = addresses
	if address == "" {
		evidence.State = "unusable"
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "diagnostic target returned no IPv6 address"
		return result
	}
	route, routeCommand, err := lookupTunRouteForAddress(ctx, address)
	result.Evidence.Route = &route
	result.Evidence.Commands = append(result.Evidence.Commands, routeCommand)
	if err != nil {
		evidence.State = "unusable"
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "lookup IPv6 route: " + err.Error()
		return result
	}
	if route.Interface != tunName {
		evidence.State = "possible_leak"
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Leak
		result.Error = "IPv6 route bypasses " + tunName + " via " + route.Interface
		return result
	}
	duration, err := client.TCP(ctx, address, 443)
	result.DurationMS = duration.Milliseconds()
	if err != nil {
		evidence.State = "unusable"
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "IPv6 TCP/443 connect failed: " + err.Error()
		return result
	}
	evidence.State = "through_tun"
	result.Status = tundiag.ProbePass
	return result
}

func parseTunDiagnosticGlobalIPv6Addresses(output, uplink, tunName string) ([]string, []string) {
	var uplinkAddresses []string
	var tunAddresses []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := strings.TrimSuffix(fields[0], ":")
		for _, field := range fields[2:] {
			candidate := strings.TrimSuffix(strings.TrimSpace(field), ",")
			ip, _, err := net.ParseCIDR(candidate)
			if err != nil {
				ip = net.ParseIP(strings.TrimSuffix(candidate, "/128"))
			}
			if ip == nil || ip.To4() != nil || !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() {
				continue
			}
			if name == uplink {
				uplinkAddresses = append(uplinkAddresses, candidate)
			}
			if name == tunName {
				tunAddresses = append(tunAddresses, candidate)
			}
		}
	}
	sort.Strings(uplinkAddresses)
	sort.Strings(tunAddresses)
	return uplinkAddresses, tunAddresses
}

func nonEmptyLines(output string) []string {
	var lines []string
	for _, raw := range strings.Split(output, "\n") {
		if line := strings.TrimSpace(raw); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
