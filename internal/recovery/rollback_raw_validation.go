package recovery

import (
	"fmt"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// rawRollbackMetadataReasons validates every raw nested rollback entry before
// counters are allowed to authorize cleanup. Counter construction intentionally
// ignores unsupported entries; this guard prevents malformed or foreign entries
// from disappearing before the applied/rollback multiset comparison.
func rawRollbackMetadataReasons(rollback txstate.RollbackMetadata) []string {
	var reasons []string
	for i, item := range rollback.TUN {
		if !rawTUNRollbackEntryValid(item) {
			reasons = append(reasons, fmt.Sprintf("raw TUN rollback entry %d is unsupported or incomplete", i))
		}
	}
	for i, item := range rollback.TUNAddresses {
		if !rawTUNAddressRollbackEntryValid(item) {
			reasons = append(reasons, fmt.Sprintf("raw TUN address rollback entry %d is unsupported or incomplete", i))
		}
	}
	for i, item := range rollback.Routes {
		if !rawRouteRollbackEntryValid(item) {
			reasons = append(reasons, fmt.Sprintf("raw route rollback entry %d is unsupported or incomplete", i))
		}
	}
	for i, item := range rollback.PolicyRules {
		if !rawPolicyRuleRollbackEntryValid(item) {
			reasons = append(reasons, fmt.Sprintf("raw policy-rule rollback entry %d is unsupported or incomplete", i))
		}
	}
	for i, item := range rollback.DNS {
		if !rawDNSRollbackEntryValid(item) {
			reasons = append(reasons, fmt.Sprintf("raw DNS rollback entry %d is unsupported or incomplete", i))
		}
	}
	for i, item := range rollback.NFTables {
		if !rawNFTablesRollbackEntryValid(item) {
			reasons = append(reasons, fmt.Sprintf("raw nftables rollback entry %d is unsupported or incomplete", i))
		}
	}
	return compactReasonStrings(reasons)
}

func rawTUNRollbackEntryValid(item txstate.TUNRollback) bool {
	return ownedRollbackMetadata(item.Owner, netexecutor.OwnerTunDevice) && strings.TrimSpace(item.InterfaceName) == managedInterface
}

func rawTUNAddressRollbackEntryValid(item txstate.TUNAddressRollback) bool {
	return ownedRollbackMetadata(item.Owner, netexecutor.OwnerTunAddress) &&
		strings.TrimSpace(item.InterfaceName) == managedInterface &&
		strings.TrimSpace(item.Family) == "ipv4" &&
		strings.TrimSpace(item.Scope) == "global" &&
		strings.TrimSpace(item.CIDR) == planner.DefaultTunIPv4CIDR &&
		item.LinkIndex > 0 &&
		strings.TrimSpace(item.LinkKind) == "tun" &&
		item.AppearedAfterCore
}

func rawRouteRollbackEntryValid(item txstate.RouteRollback) bool {
	if !ownedRollbackMetadata(item.Owner, netexecutor.OwnerRoute) || strings.TrimSpace(item.CIDR) == "" || strings.TrimSpace(item.Table) == "" {
		return false
	}
	if strings.TrimSpace(item.Table) == planner.MainRoutingTable {
		return safeMainServerBypassRoute(item)
	}
	if _, ok := managedTableToken(item.Table); !ok {
		return false
	}
	dev := strings.TrimSpace(item.Dev)
	return dev == "" || dev == managedInterface
}

func rawPolicyRuleRollbackEntryValid(item txstate.PolicyRuleRollback) bool {
	if !ownedRollbackMetadata(item.Owner, netexecutor.OwnerPolicyRule) || item.Priority <= 0 || strings.TrimSpace(item.Table) == "" {
		return false
	}
	if strings.TrimSpace(item.Table) == planner.MainRoutingTable {
		return safeMainServerBypassPolicyRule(item)
	}
	_, ok := managedTableToken(item.Table)
	return ok
}

func rawDNSRollbackEntryValid(item txstate.DNSRollback) bool {
	return ownedRollbackMetadata(item.Owner, netexecutor.OwnerDNS) &&
		strings.TrimSpace(item.Link) == managedInterface &&
		systemdResolvedBackend(item.Backend)
}

func rawNFTablesRollbackEntryValid(item txstate.NFTablesRollback) bool {
	return ownedRollbackMetadata(item.Owner, netexecutor.OwnerFirewall) && isManagedNFTTarget(item.Family, item.Table)
}
