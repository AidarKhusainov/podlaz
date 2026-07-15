package daemon

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func buildTunDiagnosticAdapters(input tunDiagnosticInput) tundiag.ProbeAdapters {
	adapters := tunDiagnosticAdapters(input)
	client := tundiag.NetworkClient{}
	adapters.Session = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunDiagnosticOwnership(ctx, input)
	}
	adapters.DNSState = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunDNSRouting(ctx, input.plan, input.snapshot)
	}
	adapters.SystemResolver = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunSystemResolverRoute(ctx, client, input.plan, "example.com")
	}
	adapters.TCP443 = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunTCPRoute(ctx, client, input.plan, "www.cloudflare.com", 443)
	}
	adapters.IPv6 = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunIPv6Connectivity(ctx, client, input.plan, input.snapshot)
	}
	return adapters
}

func probeTunDiagnosticOwnership(ctx context.Context, input tunDiagnosticInput) tundiag.ProbeResult {
	base := probeTunDiagnosticSession(input)
	if base.Status != tundiag.ProbePass {
		return base
	}
	tunName := emptyAs(input.plan.TunDevice.Name, netsnapshot.DefaultTunName)
	foundTun := false
	for _, device := range input.snapshot.TunDevices {
		if device.Name == tunName && device.Status == netsnapshot.StatusDetected {
			foundTun = true
			break
		}
	}
	result := tundiag.ProbeResult{Status: tundiag.ProbePass, Evidence: tundiag.Evidence{Notes: []string{
		"active transaction and Xray process agree on TUN mode",
		"TUN interface " + tunName + " is present in the current snapshot",
	}}}
	if !foundTun {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassOwnershipMismatch
		result.Error = "active TUN metadata exists but " + tunName + " is not detected"
		return result
	}
	if !input.plan.Firewall.Enabled {
		result.Evidence.Notes = append(result.Evidence.Notes, "nftables ownership is not required by this transaction")
		return result
	}
	tableName := emptyAs(input.plan.Firewall.TableName, "podlaz")
	if input.snapshot.Nftables.PodlazTable.Status != netsnapshot.StatusDetected || input.snapshot.Nftables.PodlazTable.Table != tableName {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassOwnershipMismatch
		result.Error = fmt.Sprintf("expected nftables table inet %s is not present in the current snapshot", tableName)
		return result
	}
	commandResult, err := tunDiagnosticCommandRunner(ctx, "nft", "list", "table", "inet", tableName)
	result.Evidence.Commands = append(result.Evidence.Commands, commandEvidence(commandResult))
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassOwnershipMismatch
		result.Error = "inspect expected nftables ownership: " + err.Error()
		return result
	}
	result.Evidence.Notes = append(result.Evidence.Notes, "nftables table inet "+tableName+" is present and readable")
	return result
}

func probeTunDNSRouting(ctx context.Context, plan planner.TunPlan, snapshot netsnapshot.Snapshot) tundiag.ProbeResult {
	targetLink := emptyAs(plan.DNS.TargetLink, netsnapshot.DefaultTunName)
	matches := make([]netsnapshot.ResolvedLink, 0, 1)
	for _, link := range snapshot.DNS.ResolvedLinks {
		if link.Name == targetLink {
			matches = append(matches, link)
			continue
		}
		if tunDiagnosticContains(link.DNSDomains, "~.") {
			return diagnosticFailure(tundiag.ClassForeignDNSConflict, fmt.Sprintf("foreign resolved link %s owns route-only domain ~.", link.Name))
		}
	}
	if len(matches) == 0 {
		return diagnosticFailure(tundiag.ClassDNSApplyFailure, "systemd-resolved has no current link record for "+targetLink)
	}
	if len(matches) > 1 {
		return diagnosticFailure(tundiag.ClassDNSApplyFailure, fmt.Sprintf("systemd-resolved has %d duplicate link records for %s", len(matches), targetLink))
	}
	link := matches[0]
	missing := make([]string, 0, 3)
	for _, server := range plan.DNS.Servers {
		if !tunDiagnosticContains(link.DNSServers, server) {
			missing = append(missing, "DNS server "+server)
		}
	}
	if !tunDiagnosticContains(link.DNSDomains, "~.") {
		missing = append(missing, "route-only domain ~.")
	}
	if !tunDiagnosticContains(link.Protocols, "+DefaultRoute") {
		missing = append(missing, "+DefaultRoute")
	}
	if len(missing) > 0 {
		return diagnosticFailure(tundiag.ClassDNSApplyFailure, "resolved link "+targetLink+" is missing "+strings.Join(missing, ", "))
	}

	result := tundiag.ProbeResult{Status: tundiag.ProbePass, Evidence: tundiag.Evidence{Notes: []string{
		"resolved link " + targetLink + " uniquely owns ~. and the planned DNS servers",
	}}}
	for _, server := range plan.DNS.Servers {
		route, command, err := lookupTunRouteForAddress(ctx, server)
		result.Evidence.Commands = append(result.Evidence.Commands, command)
		if err != nil {
			result.Status = tundiag.ProbeFail
			result.Classification = tundiag.ClassRouteFailure
			result.Error = fmt.Sprintf("lookup route to DNS server %s: %v", server, err)
			return result
		}
		if route.Interface != targetLink {
			result.Status = tundiag.ProbeFail
			result.Classification = tundiag.ClassRouteFailure
			result.Error = fmt.Sprintf("DNS server %s routes through %s; expected %s", server, route.Interface, targetLink)
			result.Evidence.Route = &route
			return result
		}
		result.Evidence.Notes = append(result.Evidence.Notes, fmt.Sprintf("DNS server %s routes through %s", server, targetLink))
	}
	return result
}

func probeTunSystemResolverRoute(ctx context.Context, client tundiag.NetworkClient, plan planner.TunPlan, name string) tundiag.ProbeResult {
	addresses, err := client.Resolve(ctx, name)
	if err != nil {
		return tundiag.ProbeResult{Status: tundiag.ProbeFail, Classification: tundiag.ClassDNSResolutionFailure, Error: err.Error()}
	}
	address := preferredAddress(addresses, false)
	if address == "" {
		return tundiag.ProbeResult{Status: tundiag.ProbeFail, Classification: tundiag.ClassDNSResolutionFailure, Error: "system resolver returned no usable IPv4 address", Evidence: tundiag.Evidence{ResolvedAddresses: addresses}}
	}
	route, command, err := lookupTunRouteForAddress(ctx, address)
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{ResolvedAddresses: addresses, Route: &route, Commands: []tundiag.CommandEvidence{command}}}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassRouteFailure
		result.Error = "lookup route to resolved address: " + err.Error()
		return result
	}
	expectedInterface := emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName)
	if route.Interface != expectedInterface {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassRouteFailure
		result.Error = fmt.Sprintf("resolved address %s routes through %s; expected %s", address, route.Interface, expectedInterface)
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunTCPRoute(ctx context.Context, client tundiag.NetworkClient, plan planner.TunPlan, host string, port uint16) tundiag.ProbeResult {
	addresses, err := client.Resolve(ctx, host)
	if err != nil {
		return tundiag.ProbeResult{Status: tundiag.ProbeFail, Classification: tundiag.ClassDNSResolutionFailure, Error: err.Error()}
	}
	address := preferredAddress(addresses, false)
	if address == "" {
		return tundiag.ProbeResult{Status: tundiag.ProbeFail, Classification: tundiag.ClassDNSResolutionFailure, Error: "resolver returned no usable IPv4 address", Evidence: tundiag.Evidence{ResolvedAddresses: addresses}}
	}
	route, command, err := lookupTunRouteForAddress(ctx, address)
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{Endpoint: net.JoinHostPort(address, strconv.Itoa(int(port))), ResolvedAddresses: addresses, Route: &route, Commands: []tundiag.CommandEvidence{command}}}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassRouteFailure
		result.Error = "lookup route to TCP target: " + err.Error()
		return result
	}
	expectedInterface := emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName)
	if route.Interface != expectedInterface {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassRouteFailure
		result.Error = fmt.Sprintf("TCP target %s routes through %s; expected %s", address, route.Interface, expectedInterface)
		return result
	}
	duration, err := client.TCP(ctx, address, port)
	result.DurationMS = duration.Milliseconds()
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassTCP443Failure
		result.Error = err.Error()
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunIPv6Connectivity(ctx context.Context, client tundiag.NetworkClient, plan planner.TunPlan, snapshot netsnapshot.Snapshot) tundiag.ProbeResult {
	command, _ := tunDiagnosticCommandRunner(ctx, "ip", "-6", "-brief", "address", "show")
	evidence := tundiag.IPv6Evidence{
		State:            string(snapshot.IPv6.Status),
		DefaultInterface: snapshot.DefaultIPv6.Interface,
		RouteTable:       snapshot.DefaultIPv6.Raw,
	}
	tunName := emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName)
	evidence.UplinkAddresses, evidence.TunAddresses = parseTunDiagnosticIPv6Addresses(command.stdout, snapshot.DefaultIPv4.Interface, tunName)
	result := tundiag.ProbeResult{Evidence: tundiag.Evidence{IPv6: &evidence, Commands: []tundiag.CommandEvidence{commandEvidence(command)}}}
	if snapshot.IPv6.Status != netsnapshot.StatusDetected {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6NotPresent
		result.Error = "host has no detected IPv6 path"
		return result
	}
	if snapshot.DefaultIPv6.Status != netsnapshot.StatusDetected {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "IPv6 is present but no usable default route was detected"
		return result
	}
	addresses, err := client.Resolve(ctx, "www.cloudflare.com")
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "resolve IPv6 diagnostic target: " + err.Error()
		return result
	}
	address := preferredAddress(addresses, true)
	result.Evidence.ResolvedAddresses = addresses
	if address == "" {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "diagnostic target returned no IPv6 address"
		return result
	}
	route, routeCommand, err := lookupTunRouteForAddress(ctx, address)
	result.Evidence.Route = &route
	result.Evidence.Commands = append(result.Evidence.Commands, routeCommand)
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "lookup IPv6 route: " + err.Error()
		return result
	}
	if route.Interface != tunName {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Leak
		result.Error = "IPv6 route bypasses " + tunName + " via " + route.Interface
		return result
	}
	duration, err := client.TCP(ctx, address, 443)
	result.DurationMS = duration.Milliseconds()
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassIPv6Unusable
		result.Error = "IPv6 TCP/443 connect failed: " + err.Error()
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func lookupTunRouteForAddress(ctx context.Context, address string) (tundiag.RouteEvidence, tundiag.CommandEvidence, error) {
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil {
		return tundiag.RouteEvidence{Destination: address}, tundiag.CommandEvidence{}, fmt.Errorf("%q is not an IP address", address)
	}
	family := "-6"
	if ip.To4() != nil {
		family = "-4"
	}
	return lookupTunRoute(ctx, family, address)
}

func preferredAddress(addresses []string, ipv6 bool) string {
	copyOfAddresses := append([]string(nil), addresses...)
	sort.Strings(copyOfAddresses)
	for _, address := range copyOfAddresses {
		ip := net.ParseIP(address)
		if ip == nil {
			continue
		}
		if ipv6 && ip.To4() == nil {
			return address
		}
		if !ipv6 && ip.To4() != nil {
			return address
		}
	}
	return ""
}

func tunDiagnosticLocalAddresses(interfaceName string) []string {
	if strings.TrimSpace(interfaceName) == "" {
		return nil
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(addresses))
	for _, address := range addresses {
		out = append(out, address.String())
	}
	sort.Strings(out)
	return out
}

func tunDiagnosticConnectionName(snapshot netsnapshot.Snapshot) string {
	physicalInterface := snapshot.DefaultIPv4.Interface
	for _, connection := range snapshot.NetworkManager.ActiveConnections {
		if connection.Device == physicalInterface {
			return connection.Name
		}
	}
	return ""
}

func tunDiagnosticDoHEndpoints() []string {
	targets := tundiag.TargetsByKind(tundiag.TargetDoH)
	endpoints := make([]string, 0, len(targets))
	for _, target := range targets {
		endpoints = append(endpoints, target.URL)
	}
	return endpoints
}

func tunDiagnosticNftablesStatus(plan planner.TunPlan, snapshot netsnapshot.Snapshot) string {
	if !plan.Firewall.Enabled {
		return "not required by transaction"
	}
	if snapshot.Nftables.PodlazTable.Status != netsnapshot.StatusDetected {
		return "expected table not detected"
	}
	return "owned table inet " + snapshot.Nftables.PodlazTable.Table + " detected"
}
