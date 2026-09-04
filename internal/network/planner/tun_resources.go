package planner

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const tunAddressAllocationLastHost = 254
const tunRoutingTableAllocationLast = TunRoutingTableID + 99

// TunResourceAllocation is the immutable set of collision-sensitive identities
// selected for one new TUN Network Session. It is derived from authoritative
// typed host evidence and must be persisted before mutation by the daemon.
type TunResourceAllocation struct {
	TunIPv4CIDR        string
	RoutingTableID     int
	ServerRulePriority int
	TunnelRulePriority int
}

// AllocateTunResources selects deterministic, verified-free resources for one
// new Network Session. Historical values remain first-choice candidates only;
// their numeric shape never implies Podlaz ownership.
func AllocateTunResources(evidence snapshot.TunAllocationEvidence) (TunResourceAllocation, error) {
	if err := validateTunAllocationEvidence(evidence); err != nil {
		return TunResourceAllocation{}, err
	}

	cidr, err := allocateTunIPv4CIDR(evidence)
	if err != nil {
		return TunResourceAllocation{}, err
	}
	tableID, err := allocateTunRoutingTable(evidence)
	if err != nil {
		return TunResourceAllocation{}, err
	}
	serverPriority, tunnelPriority, err := allocateTunRulePriorities(evidence)
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

func validateTunAllocationEvidence(evidence snapshot.TunAllocationEvidence) error {
	for _, address := range evidence.IPv4Addresses {
		if !address.IsValid() || !address.Addr().Is4() {
			return fmt.Errorf("allocate TUN resources: malformed IPv4 address inventory entry")
		}
	}
	for _, route := range evidence.IPv4Routes {
		if route.Table == 0 {
			return fmt.Errorf("allocate TUN resources: route inventory entry has no routing table")
		}
		if route.Default {
			if route.Destination.IsValid() {
				return fmt.Errorf("allocate TUN resources: default route inventory entry has a destination")
			}
			continue
		}
		if !route.Destination.IsValid() || !route.Destination.Addr().Is4() {
			return fmt.Errorf("allocate TUN resources: malformed IPv4 route inventory entry")
		}
	}
	for _, rule := range evidence.IPv4PolicyRules {
		if rule.Table == 0 {
			return fmt.Errorf("allocate TUN resources: policy-rule inventory entry has no routing table")
		}
		if rule.Priority == 0 || rule.Priority >= 32766 {
			return fmt.Errorf("allocate TUN resources: malformed policy-rule priority")
		}
	}
	return nil
}

func allocateTunIPv4CIDR(evidence snapshot.TunAllocationEvidence) (string, error) {
	for host := 1; host <= tunAddressAllocationLastHost; host++ {
		candidate := netip.MustParseAddr(fmt.Sprintf("198.18.0.%d", host))
		if !tunAllocationAddressOccupied(evidence, candidate) {
			return netip.PrefixFrom(candidate, 32).String(), nil
		}
	}
	return "", fmt.Errorf("allocate TUN resources: no collision-free TUN IPv4 address is available in the bounded session pool")
}

func tunAllocationAddressOccupied(evidence snapshot.TunAllocationEvidence, candidate netip.Addr) bool {
	for _, address := range evidence.IPv4Addresses {
		if address.Contains(candidate) {
			return true
		}
	}
	for _, route := range evidence.IPv4Routes {
		if !route.Default && route.Destination.Contains(candidate) {
			return true
		}
	}
	return false
}

func allocateTunRoutingTable(evidence snapshot.TunAllocationEvidence) (int, error) {
	used := map[uint32]struct{}{}
	for _, route := range evidence.IPv4Routes {
		used[route.Table] = struct{}{}
	}
	for _, rule := range evidence.IPv4PolicyRules {
		used[rule.Table] = struct{}{}
	}
	for candidate := TunRoutingTableID; candidate <= tunRoutingTableAllocationLast; candidate++ {
		if _, occupied := used[uint32(candidate)]; !occupied {
			return candidate, nil
		}
	}
	return 0, fmt.Errorf("allocate TUN resources: no collision-free routing table is available in the bounded session pool")
}

func allocateTunRulePriorities(evidence snapshot.TunAllocationEvidence) (serverPriority, tunnelPriority int, err error) {
	used := map[uint32]struct{}{}
	upperTunnelPriority := TunRulePriority
	for _, rule := range evidence.IPv4PolicyRules {
		used[rule.Priority] = struct{}{}
		priority := int(rule.Priority)
		if priority <= upperTunnelPriority {
			upperTunnelPriority = priority - 1
		}
	}

	for tunnel := upperTunnelPriority; tunnel >= 2; tunnel-- {
		server := tunnel - 1
		_, serverOccupied := used[uint32(server)]
		_, tunnelOccupied := used[uint32(tunnel)]
		if !serverOccupied && !tunnelOccupied {
			return server, tunnel, nil
		}
	}
	return 0, 0, fmt.Errorf("allocate TUN resources: no collision-free policy-rule priority pair can precede the existing host policy rules")
}

// tunAllocationEvidenceFromSnapshot is a compatibility adapter for read-only
// planning and explicit in-memory fixtures. Production mutation authority is
// collected separately from rtnetlink and must not fall back to this adapter.
func tunAllocationEvidenceFromSnapshot(s snapshot.Snapshot) (snapshot.TunAllocationEvidence, error) {
	if !allocationInspectionAvailable(s.IPv4Addresses.Inspection.Status) {
		return snapshot.TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: IPv4 address inventory is %s", allocationInspectionStatus(s.IPv4Addresses.Inspection.Status))
	}
	if !allocationInspectionAvailable(s.IPv4Routes.Inspection.Status) {
		return snapshot.TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: IPv4 route inventory is %s", allocationInspectionStatus(s.IPv4Routes.Inspection.Status))
	}
	if !allocationInspectionAvailable(s.IPv4PolicyRules.Inspection.Status) {
		return snapshot.TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: IPv4 policy-rule inventory is %s", allocationInspectionStatus(s.IPv4PolicyRules.Inspection.Status))
	}

	evidence := snapshot.TunAllocationEvidence{
		IPv4Addresses:   make([]netip.Prefix, 0, len(s.IPv4Addresses.Addresses)),
		IPv4Routes:      make([]snapshot.TunAllocationRoute, 0, len(s.IPv4Routes.Routes)),
		IPv4PolicyRules: make([]snapshot.TunAllocationRule, 0, len(allocationPolicyRules(s))),
	}
	for _, address := range s.IPv4Addresses.Addresses {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(address.CIDR))
		if err != nil || !prefix.Addr().Is4() {
			return snapshot.TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed IPv4 address inventory entry")
		}
		evidence.IPv4Addresses = append(evidence.IPv4Addresses, prefix)
	}
	for _, route := range s.IPv4Routes.Routes {
		table, ok := compatibilityRouteTable(route.Table)
		if !ok {
			return snapshot.TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed routing table identity %q", route.Table)
		}
		converted := snapshot.TunAllocationRoute{Table: table}
		destination := strings.TrimSpace(route.Destination)
		if destination == "" || destination == IPv4DefaultRoute || destination == "0.0.0.0/0" {
			converted.Default = true
		} else {
			prefix, err := netip.ParsePrefix(destination)
			if err != nil || !prefix.Addr().Is4() {
				return snapshot.TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed IPv4 route inventory entry")
			}
			converted.Destination = prefix.Masked()
		}
		evidence.IPv4Routes = append(evidence.IPv4Routes, converted)
	}
	for _, rule := range allocationPolicyRules(s) {
		priority, err := strconv.ParseUint(strings.TrimSpace(rule.Priority), 10, 32)
		if err != nil {
			return snapshot.TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed policy-rule priority")
		}
		table, ok := compatibilityRouteTable(rule.Table)
		if !ok {
			return snapshot.TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed policy-rule routing table %q", rule.Table)
		}
		evidence.IPv4PolicyRules = append(evidence.IPv4PolicyRules, snapshot.TunAllocationRule{Priority: uint32(priority), Table: table})
	}
	if err := validateTunAllocationEvidence(evidence); err != nil {
		return snapshot.TunAllocationEvidence{}, err
	}
	return evidence, nil
}

func allocationInspectionAvailable(status snapshot.Status) bool {
	// The empty value keeps existing in-memory test/legacy snapshot producers
	// compatible. Production mutation never uses this compatibility path.
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

func compatibilityRouteTable(table string) (uint32, bool) {
	switch strings.TrimSpace(table) {
	case "", MainRoutingTable, "main":
		return 254, true
	case "local":
		return 255, true
	case "default":
		return 253, true
	}
	value, err := strconv.ParseUint(strings.TrimSpace(table), 10, 32)
	return uint32(value), err == nil && value > 0
}

// PlanTunForSession builds a read-only plan from diagnostic snapshot evidence.
// Production mutation callers must use PlanTunForSessionWithAllocationEvidence.
func PlanTunForSession(p profile.Profile, s snapshot.Snapshot, opts TunOptions) (TunPlan, error) {
	evidence, err := tunAllocationEvidenceFromSnapshot(s)
	if err != nil {
		return TunPlan{}, err
	}
	return PlanTunForSessionWithAllocationEvidence(p, s, evidence, opts)
}

// PlanTunForSessionWithAllocationEvidence keeps diagnostic composition separate
// from collision-sensitive allocation authority.
func PlanTunForSessionWithAllocationEvidence(p profile.Profile, s snapshot.Snapshot, evidence snapshot.TunAllocationEvidence, opts TunOptions) (TunPlan, error) {
	if err := profile.Validate(p); err != nil {
		return TunPlan{}, err
	}
	resources, err := AllocateTunResources(evidence)
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
	action := "add"
	reason := "pin VPN server traffic to the concrete bootstrap path observed before the Podlaz full-tunnel policy"
	if exactServerBypassRouteExists(s, serverIP) {
		action = TunActionVerifyExisting
		reason = "use the exact pre-existing server bootstrap host route as a verified unowned prerequisite"
	}
	return TunRoutePlan{
		Family:      "ipv4",
		Destination: serverIP + "/32",
		Table:       MainRoutingTable,
		Interface:   s.ServerRoute.Interface,
		Gateway:     s.ServerRoute.Gateway,
		Action:      action,
		Reason:      reason,
	}
}

func exactServerBypassRouteExists(s snapshot.Snapshot, serverIP string) bool {
	if strings.TrimSpace(serverIP) == "" || s.IPv4Routes.Inspection.Status != snapshot.StatusDetected {
		return false
	}
	wantDestination := serverIP + "/32"
	for _, route := range s.IPv4Routes.Routes {
		if route.Status != snapshot.StatusDetected || strings.TrimSpace(route.Destination) != wantDestination {
			continue
		}
		if !mainTableIdentity(route.Table) {
			continue
		}
		if strings.TrimSpace(route.Interface) != strings.TrimSpace(s.ServerRoute.Interface) || strings.TrimSpace(route.Gateway) != strings.TrimSpace(s.ServerRoute.Gateway) {
			continue
		}
		return true
	}
	return false
}

func mainTableIdentity(table string) bool {
	switch strings.TrimSpace(table) {
	case "", MainRoutingTable, "254":
		return true
	default:
		return false
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
