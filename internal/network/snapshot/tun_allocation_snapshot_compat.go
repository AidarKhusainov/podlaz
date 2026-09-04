package snapshot

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// TunAllocationEvidenceFromSnapshot converts explicit diagnostic snapshot
// inventories into typed allocation evidence for read-only planning and
// injected in-memory tests. Production TUN mutation authority must come from
// CollectTunAllocationEvidence and must never fall back to this adapter.
func TunAllocationEvidenceFromSnapshot(s Snapshot) (TunAllocationEvidence, error) {
	if !allocationSnapshotStatusAvailable(s.IPv4Addresses.Inspection.Status) {
		return TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: IPv4 address inventory is %s", allocationSnapshotStatus(s.IPv4Addresses.Inspection.Status))
	}
	if !allocationSnapshotStatusAvailable(s.IPv4Routes.Inspection.Status) {
		return TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: IPv4 route inventory is %s", allocationSnapshotStatus(s.IPv4Routes.Inspection.Status))
	}
	if !allocationSnapshotStatusAvailable(s.IPv4PolicyRules.Inspection.Status) {
		return TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: IPv4 policy-rule inventory is %s", allocationSnapshotStatus(s.IPv4PolicyRules.Inspection.Status))
	}

	evidence := TunAllocationEvidence{
		IPv4Addresses:   make([]netip.Prefix, 0, len(s.IPv4Addresses.Addresses)),
		IPv4Routes:      make([]TunAllocationRoute, 0, len(s.IPv4Routes.Routes)),
		IPv4PolicyRules: make([]TunAllocationRule, 0, len(allocationSnapshotPolicyRules(s))),
	}
	for _, address := range s.IPv4Addresses.Addresses {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(address.CIDR))
		if err != nil || !prefix.Addr().Is4() {
			return TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed IPv4 address inventory entry")
		}
		evidence.IPv4Addresses = append(evidence.IPv4Addresses, prefix)
	}
	for _, route := range s.IPv4Routes.Routes {
		table, ok := allocationSnapshotRouteTable(route.Table)
		if !ok {
			return TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed routing table identity %q", route.Table)
		}
		converted := TunAllocationRoute{Table: table}
		destination := strings.TrimSpace(route.Destination)
		if destination == "" || destination == "default" || destination == "0.0.0.0/0" {
			converted.Default = true
		} else {
			prefix, err := netip.ParsePrefix(destination)
			if err != nil || !prefix.Addr().Is4() {
				return TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed IPv4 route inventory entry")
			}
			converted.Destination = prefix.Masked()
		}
		evidence.IPv4Routes = append(evidence.IPv4Routes, converted)
	}
	for _, rule := range allocationSnapshotPolicyRules(s) {
		priority, err := strconv.ParseUint(strings.TrimSpace(rule.Priority), 10, 32)
		if err != nil {
			return TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed policy-rule priority")
		}
		table, ok := allocationSnapshotRouteTable(rule.Table)
		if !ok {
			return TunAllocationEvidence{}, fmt.Errorf("allocate TUN resources: malformed policy-rule routing table %q", rule.Table)
		}
		evidence.IPv4PolicyRules = append(evidence.IPv4PolicyRules, TunAllocationRule{Priority: uint32(priority), Table: table})
	}
	return evidence, nil
}

func allocationSnapshotStatusAvailable(status Status) bool {
	return status == "" || status == StatusDetected
}

func allocationSnapshotStatus(status Status) string {
	if status == "" {
		return "unspecified"
	}
	return string(status)
}

func allocationSnapshotPolicyRules(s Snapshot) []PolicyRoutingSignal {
	if s.IPv4PolicyRules.Inspection.Status == StatusDetected || len(s.IPv4PolicyRules.Rules) > 0 {
		return s.IPv4PolicyRules.Rules
	}
	if s.IPv4PolicyRules.Inspection.Status != "" {
		return nil
	}
	out := make([]PolicyRoutingSignal, 0, len(s.PolicyRouting))
	for _, signal := range s.PolicyRouting {
		if strings.TrimSpace(signal.Kind) == "rule" {
			out = append(out, signal)
		}
	}
	return out
}

func allocationSnapshotRouteTable(table string) (uint32, bool) {
	switch strings.TrimSpace(table) {
	case "", "main":
		return 254, true
	case "local":
		return 255, true
	case "default":
		return 253, true
	}
	value, err := strconv.ParseUint(strings.TrimSpace(table), 10, 32)
	return uint32(value), err == nil && value > 0
}
