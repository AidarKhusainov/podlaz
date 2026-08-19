package planner

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const tunAddressAllocationLastHost = 254
const tunRoutingTableAllocationLast = TunRoutingTableID + 99

// TunResourceAllocation is the immutable set of collision-sensitive identities
// selected for one new TUN Network Session. It is derived from one read-only
// authoritative snapshot and must be persisted before mutation by the daemon.
type TunResourceAllocation struct {
	TunIPv4CIDR        string
	RoutingTableID     int
	ServerRulePriority int
	TunnelRulePriority int
}

// AllocateTunResources selects deterministic, verified-free resources for one
// new Network Session. Historical values remain first-choice candidates only;
// their numeric shape never implies Podlaz ownership.
func AllocateTunResources(s snapshot.Snapshot) (TunResourceAllocation, error) {
	if err := validateTunAllocationEvidence(s); err != nil {
		return TunResourceAllocation{}, err
	}

	cidr, err := allocateTunIPv4CIDR(s)
	if err != nil {
		return TunResourceAllocation{}, err
	}
	tableID, err := allocateTunRoutingTable(s)
	if err != nil {
		return TunResourceAllocation{}, err
	}
	serverPriority, tunnelPriority, err := allocateTunRulePriorities(s)
	if err != nil {
		return TunResourceAllocation{}, err
	}
	return TunResourceAllocation{
		TunIPv4CIDR:        cidr,
		RoutingTableID:     tableID,
		ServerRulePriority: serverPriority,
		TunnelRulePriority: tunnelPriority,
	}, nil
}

func validateTunAllocationEvidence(s snapshot.Snapshot) error {
	if !allocationInspectionAvailable(s.IPv4Addresses.Inspection.Status) {
		return fmt.Errorf("allocate TUN resources: IPv4 address inventory is %s", allocationInspectionStatus(s.IPv4Addresses.Inspection.Status))
	}
	if !allocationInspectionAvailable(s.IPv4Routes.Inspection.Status) {
		return fmt.Errorf("allocate TUN resources: IPv4 route inventory is %s", allocationInspectionStatus(s.IPv4Routes.Inspection.Status))
	}
	if !allocationInspectionAvailable(s.IPv4PolicyRules.Inspection.Status) {
		return fmt.Errorf("allocate TUN resources: IPv4 policy-rule inventory is %s", allocationInspectionStatus(s.IPv4PolicyRules.Inspection.Status))
	}

	for _, address := range s.IPv4Addresses.Addresses {
		ip, _, err := net.ParseCIDR(strings.TrimSpace(address.CIDR))
		if err != nil || ip.To4() == nil {
			return fmt.Errorf("allocate TUN resources: malformed IPv4 address inventory entry")
		}
	}
	for _, route := range s.IPv4Routes.Routes {
		destination := strings.TrimSpace(route.Destination)
		if destination == "" || destination == IPv4DefaultRoute || destination == "0.0.0.0/0" {
			continue
		}
		ip, _, err := net.ParseCIDR(destination)
		if err != nil || ip.To4() == nil {
			return fmt.Errorf("allocate TUN resources: malformed IPv4 route inventory entry")
		}
	}
	for _, rule := range allocationPolicyRules(s) {
		if strings.TrimSpace(rule.Priority) == "" {
			return fmt.Errorf("allocate TUN resources: policy-rule inventory entry has no priority")
		}
		priority, err := strconv.Atoi(strings.TrimSpace(rule.Priority))
		if err != nil || priority <= 0 || priority >= 32766 {
			return fmt.Errorf("allocate TUN resources: malformed policy-rule priority")
		}
	}
	return nil
}

func allocationInspectionAvailable(status snapshot.Status) bool {
	// The empty value keeps existing in-memory test/legacy snapshot producers
	// compatible. Production collection always publishes an explicit status.
	return status == "" || status == snapshot.StatusDetected
}

func allocationInspectionStatus(status snapshot.Status) string {
	if status == "" {
		return "unspecified"
	}
	return string(status)
}

func allocationPolicyRules(s snapshot.Snapshot) []snapshot.PolicyRoutingSignal {
	if s.IPv4PolicyRules.Inspection.Status == snapshot.StatusDetected || len(s.IPv4PolicyRules.Rules) > 0 {
		return s.IPv4PolicyRules.Rules
	}
	// Zero-value inventories are retained only for older in-memory fixtures.
	// Explicit unknown/missing inventories never fall back to lossy evidence.
	if s.IPv4PolicyRules.Inspection.Status == "" {
		var rules []snapshot.PolicyRoutingSignal
		for _, signal := range s.PolicyRouting {
			if signal.Kind == "rule" {
				rules = append(rules, signal)
			}
		}
		return rules
	}
	return nil
}

func allocateTunIPv4CIDR(s snapshot.Snapshot) (string, error) {
	for host := 1; host <= tunAddressAllocationLastHost; host++ {
		candidate := fmt.Sprintf("198.18.0.%d/32", host)
		if tunAddressConflict(s, candidate) == "" {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("allocate TUN resources: no collision-free TUN IPv4 address is available in the bounded session pool")
}

func allocateTunRoutingTable(s snapshot.Snapshot) (int, error) {
	used := map[int]struct{}{}
	for _, route := range s.IPv4Routes.Routes {
		if tableID, ok := numericRouteTable(route.Table); ok {
			used[tableID] = struct{}{}
		}
	}
	for _, rule := range allocationPolicyRules(s) {
		if tableID, ok := numericRouteTable(rule.Table); ok {
			used[tableID] = struct{}{}
		}
	}
	for candidate := TunRoutingTableID; candidate <= tunRoutingTableAllocationLast; candidate++ {
		if _, occupied := used[candidate]; !occupied {
			return candidate, nil
		}
	}
	return 0, fmt.Errorf("allocate TUN resources: no collision-free routing table is available in the bounded session pool")
}

func numericRouteTable(table string) (int, bool) {
	table = strings.TrimSpace(table)
	if table == "" {
		return 0, false
	}
	value, err := strconv.Atoi(table)
	return value, err == nil && value > 0
}

func allocateTunRulePriorities(s snapshot.Snapshot) (serverPriority, tunnelPriority int, err error) {
	used := map[int]struct{}{}
	upperTunnelPriority := TunRulePriority
	for _, rule := range allocationPolicyRules(s) {
		priority, parseErr := strconv.Atoi(strings.TrimSpace(rule.Priority))
		if parseErr != nil || priority <= 0 || priority >= 32766 {
			return 0, 0, fmt.Errorf("allocate TUN resources: malformed policy-rule priority")
		}
		used[priority] = struct{}{}
		if priority <= upperTunnelPriority {
			upperTunnelPriority = priority - 1
		}
	}

	for tunnel := upperTunnelPriority; tunnel >= 2; tunnel-- {
		server := tunnel - 1
		_, serverOccupied := used[server]
		_, tunnelOccupied := used[tunnel]
		if !serverOccupied && !tunnelOccupied {
			return server, tunnel, nil
		}
	}
	return 0, 0, fmt.Errorf("allocate TUN resources: no collision-free policy-rule priority pair can precede the existing host policy rules")
}

// PlanTunForSession builds a full-tunnel plan with exact numeric identities
// selected from the supplied host snapshot. Existing PlanTun remains the
// historical dry-run contract until its callers migrate explicitly.
func PlanTunForSession(p profile.Profile, s snapshot.Snapshot, opts TunOptions) (TunPlan, error) {
	if err := profile.Validate(p); err != nil {
		return TunPlan{}, err
	}
	resources, err := AllocateTunResources(s)
	if err != nil {
		return TunPlan{}, err
	}

	device := TunDevicePlan{Name: snapshot.DefaultTunName, MTU: DefaultTunMTU, Action: "verify", Reason: "Xray owns TUN link creation and lifetime; podlazd verifies the existing link before L3 mutations"}
	address := allocatedTunAddressPlan(device, resources.TunIPv4CIDR)
	serverIP := concreteServerBypassIP(s)
	serverBypass := allocatedServerBypassRoute(s, serverIP)
	table := strconv.Itoa(resources.RoutingTableID)
	routes := []TunRoutePlan{{
		Family:      "ipv4",
		Destination: IPv4DefaultRoute,
		Table:       table,
		Interface:   snapshot.DefaultTunName,
		Action:      "add",
		Reason:      "route default IPv4 traffic through the Podlaz TUN interface using this session's allocated table",
	}}
	policyRules := []TunPolicyRulePlan{{
		Family:   "ipv4",
		Priority: resources.TunnelRulePriority,
		Selector: IPv4DefaultSelector,
		Table:    table,
		Action:   "add",
		Reason:   "send default IPv4 traffic through this session's allocated routing table before pre-existing host policy rules",
	}}
	if serverIP != "" {
		routes = append(routes, serverBypass)
		policyRules = append([]TunPolicyRulePlan{{
			Family:   "ipv4",
			Priority: resources.ServerRulePriority,
			Selector: "to " + serverIP + "/32",
			Table:    MainRoutingTable,
			Action:   "add",
			Reason:   "keep VPN server traffic on the concrete current bootstrap path before the full-tunnel policy rule",
		}}, policyRules...)
	}

	dnsPlan := dnsPlan(s, device, normalizeDNSServers(opts.DNSServers))
	firewallPlan := firewallPlan(s, normalizeKillSwitchPolicy(opts.KillSwitchPolicy), device, serverIP)
	loopRisks := tunRouteLoopRisks(s)
	warnings := append([]string{}, s.Warnings...)
	warnings = append(warnings, tunSnapshotWarnings(s)...)
	warnings = append(warnings, tunDesiredStateWarnings(s, serverIP)...)
	warnings = append(warnings, dnsPlanWarnings(s, dnsPlan)...)
	warnings = append(warnings, firewallPlanWarnings(s, firewallPlan)...)
	warnings = append(warnings, loopRisks...)

	steps := []string{
		"Collect the current host networking baseline read-only",
		fmt.Sprintf("Allocate TUN address %s, routing table %d, and policy priorities %d/%d for this Network Session", resources.TunIPv4CIDR, resources.RoutingTableID, resources.ServerRulePriority, resources.TunnelRulePriority),
		fmt.Sprintf("Plan Xray-owned TUN interface %s with MTU %d and daemon-side identity verification", device.Name, device.MTU),
		fmt.Sprintf("Plan daemon-owned IPv4 address %s on %s", address.CIDR, address.Interface),
		fmt.Sprintf("Plan routing table %d with IPv4 default route through %s", resources.RoutingTableID, device.Name),
	}
	if serverIP != "" {
		steps = append(steps, fmt.Sprintf("Plan policy rule priority %d for VPN server bootstrap via %s", resources.ServerRulePriority, MainRoutingTable))
	}
	steps = append(steps,
		fmt.Sprintf("Plan policy rule priority %d for default IPv4 traffic via table %d", resources.TunnelRulePriority, resources.RoutingTableID),
		fmt.Sprintf("Plan DNS backend %s on link %s with server(s) %s", dnsPlan.Backend, dnsPlan.TargetLink, strings.Join(dnsPlan.Servers, ", ")),
		fmt.Sprintf("Plan nftables table %s %s with %d chain(s), %d rule(s), and %s kill-switch policy", firewallPlan.Family, firewallPlan.Table, len(firewallPlan.Chains), len(firewallPlan.Rules), firewallPlan.KillSwitch.Policy),
		"Leave unrelated TUN devices, routes, policy rules, DNS links, and firewall objects unchanged",
	)

	return TunPlan{
		Mode:          ModeTun,
		TunnelMode:    TunTunnelMode,
		ProfileID:     p.ID,
		ProfileName:   p.Name,
		Snapshot:      s,
		TunDevice:     device,
		TunAddress:    address,
		Routes:        routes,
		PolicyRules:   policyRules,
		ServerBypass:  serverBypass,
		DNS:           dnsPlan,
		Firewall:      firewallPlan,
		LoopRisks:     loopRisks,
		Warnings:      compactWarnings(warnings),
		Steps:         steps,
		RollbackSteps: rollbackSteps(address, routes, policyRules, dnsPlan, firewallPlan),
	}, nil
}

func allocatedTunAddressPlan(device TunDevicePlan, cidr string) TunAddressPlan {
	return TunAddressPlan{
		Family:      "ipv4",
		Interface:   device.Name,
		CIDR:        cidr,
		Scope:       "global",
		Action:      TunAddressActionAssign,
		Reason:      "assign the collision-free Podlaz-owned point address allocated for this Network Session",
		Owner:       TunAddressOwner,
		RollbackKey: device.Name + "/" + cidr,
		LinkKind:    "tun",
	}
}

func allocatedServerBypassRoute(s snapshot.Snapshot, serverIP string) TunRoutePlan {
	if serverIP == "" {
		return TunRoutePlan{Family: "ipv4", Destination: "<server-ip>", Table: MainRoutingTable, Action: "blocked", Reason: "server route did not resolve to a concrete IP address"}
	}
	return TunRoutePlan{
		Family:      "ipv4",
		Destination: serverIP + "/32",
		Table:       MainRoutingTable,
		Interface:   s.ServerRoute.Interface,
		Gateway:     s.ServerRoute.Gateway,
		Action:      "add",
		Reason:      "pin VPN server traffic to the concrete bootstrap path observed before the Podlaz full-tunnel policy",
	}
}

// TunResourceAllocationFromPlan extracts and validates the exact allocated
// identities that must be persisted with a newly planned session.
func TunResourceAllocationFromPlan(plan TunPlan) (TunResourceAllocation, error) {
	allocation := TunResourceAllocation{TunIPv4CIDR: strings.TrimSpace(plan.TunAddress.CIDR)}
	for _, route := range plan.Routes {
		if route.Destination != IPv4DefaultRoute || route.Interface != snapshot.DefaultTunName {
			continue
		}
		table, err := strconv.Atoi(strings.TrimSpace(route.Table))
		if err != nil || table <= 0 {
			return TunResourceAllocation{}, fmt.Errorf("TUN plan has no exact numeric session routing table")
		}
		allocation.RoutingTableID = table
		break
	}
	for _, rule := range plan.PolicyRules {
		switch {
		case rule.Table == MainRoutingTable && strings.HasPrefix(strings.TrimSpace(rule.Selector), "to "):
			allocation.ServerRulePriority = rule.Priority
		case rule.Priority > 0 && rule.Table == strconv.Itoa(allocation.RoutingTableID):
			allocation.TunnelRulePriority = rule.Priority
		}
	}
	if allocation.TunIPv4CIDR == "" || allocation.RoutingTableID <= 0 || allocation.ServerRulePriority <= 0 || allocation.TunnelRulePriority <= 0 || allocation.ServerRulePriority >= allocation.TunnelRulePriority {
		return TunResourceAllocation{}, fmt.Errorf("TUN plan is missing a complete exact session resource allocation")
	}
	return allocation, nil
}
