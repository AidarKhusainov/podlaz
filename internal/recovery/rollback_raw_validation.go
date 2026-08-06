package recovery

import (
	"fmt"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// rawTransactionNetworkOwnershipReasons validates raw applied and rollback
// network ownership before counters are allowed to authorize cleanup. Counter
// construction intentionally ignores unsupported entries; this guard prevents
// malformed or foreign entries from disappearing before the applied/rollback
// multiset comparison.
func rawTransactionNetworkOwnershipReasons(tx txstate.Transaction) []string {
	reasons := rawRollbackMetadataReasons(tx.Rollback)
	reasons = append(reasons, rawAppliedNetworkStepReasons(tx)...)
	return compactReasonStrings(reasons)
}

// rawRollbackMetadataReasons validates every raw nested rollback entry before
// counters are allowed to authorize cleanup.
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

func rawAppliedNetworkStepReasons(tx txstate.Transaction) []string {
	rollbackCounter := rollbackNetworkStepCounter(tx.Rollback)
	appliedCounter := map[string]int{}
	var reasons []string
	for i, step := range tx.AppliedSteps {
		kind := strings.TrimSpace(step.Kind)
		if !rawAppliedStepLooksNetworkOwned(step) {
			continue
		}
		if !knownNetworkAppliedStepKind(kind) {
			reasons = append(reasons, fmt.Sprintf("raw applied network step %d has unknown kind %q", i, kind))
			continue
		}
		if !ownedNetworkStepOwner(kind, step.Owner) {
			reasons = append(reasons, fmt.Sprintf("raw applied network step %d has unsupported owner", i))
			continue
		}
		if strings.TrimSpace(step.Target) == "" {
			reasons = append(reasons, fmt.Sprintf("raw applied network step %d has empty target", i))
			continue
		}
		key := appliedStepKey(kind, step.Target, normalizeAppliedStepOwner(step.Owner, kind))
		appliedCounter[key]++
		if !rawAppliedStepTargetHasDesiredMapping(tx.DesiredPlan, step) {
			reasons = append(reasons, fmt.Sprintf("raw applied network step %d has no unique desired full-tuple mapping", i))
		}
	}
	for key, count := range appliedCounter {
		if rollbackCounter[key] != count {
			reasons = append(reasons, "raw applied network ownership multiset is not exactly represented in rollback metadata")
			break
		}
	}
	return compactReasonStrings(reasons)
}

func rawAppliedStepLooksNetworkOwned(step txstate.AppliedStep) bool {
	kind := strings.TrimSpace(step.Kind)
	if knownNetworkAppliedStepKind(kind) {
		return true
	}
	owner := strings.TrimSpace(step.Owner)
	if owner == txstate.TransactionOwner {
		return true
	}
	switch owner {
	case netexecutor.OwnerTunDevice, netexecutor.OwnerTunAddress, netexecutor.OwnerRoute, netexecutor.OwnerPolicyRule, netexecutor.OwnerDNS, netexecutor.OwnerFirewall:
		return true
	default:
		return strings.HasPrefix(owner, txstate.TransactionOwner+":")
	}
}

func knownNetworkAppliedStepKind(kind string) bool {
	switch kind {
	case "tun-device", "tun-address", "route", "policy-rule", "dns", "nftables":
		return true
	default:
		return false
	}
}

func rawAppliedStepTargetHasDesiredMapping(desired txstate.DesiredPlan, step txstate.AppliedStep) bool {
	switch step.Kind {
	case "tun-device":
		return strings.TrimSpace(step.Target) == managedInterface && ownedRollbackMetadata(step.Owner, netexecutor.OwnerTunDevice)
	case "tun-address":
		return strings.TrimSpace(step.Target) == tunAddressRollbackTarget(txstate.TUNAddressRollback{
			Family:            desired.TUNAddress.Family,
			InterfaceName:     desired.TUNAddress.InterfaceName,
			CIDR:              desired.TUNAddress.CIDR,
			Scope:             desired.TUNAddress.Scope,
			LinkIndex:         desired.TUNAddress.LinkIndex,
			LinkKind:          desired.TUNAddress.LinkKind,
			AppearedAfterCore: desired.TUNAddress.AppearedAfterCore,
			Owner:             desired.TUNAddress.Owner,
		}) && rawTUNAddressRollbackEntryValid(txstate.TUNAddressRollback{
			Family:            desired.TUNAddress.Family,
			InterfaceName:     desired.TUNAddress.InterfaceName,
			CIDR:              desired.TUNAddress.CIDR,
			Scope:             desired.TUNAddress.Scope,
			LinkIndex:         desired.TUNAddress.LinkIndex,
			LinkKind:          desired.TUNAddress.LinkKind,
			AppearedAfterCore: desired.TUNAddress.AppearedAfterCore,
			Owner:             desired.TUNAddress.Owner,
		})
	case "route":
		matches := 0
		for _, route := range desired.Routes {
			if route.Operation == "add" && ownedRollbackMetadata(route.Owner, netexecutor.OwnerRoute) && strings.TrimSpace(step.Target) == routeRollbackTarget(txstate.RouteRollback{Table: route.Table, CIDR: route.CIDR, Via: route.Via, Dev: route.Dev, Owner: route.Owner}) {
				matches++
			}
		}
		return matches == 1
	case "policy-rule":
		return desiredAppliedStepTargetCount(desired.Steps, "policy-rule", step.Target, netexecutor.OwnerPolicyRule) == 1
	case "dns":
		return strings.TrimSpace(step.Target) == strings.TrimSpace(desired.DNS.Link) && ownedRollbackMetadata(desired.DNS.Owner, netexecutor.OwnerDNS)
	case "nftables":
		return strings.TrimSpace(step.Target) == nftRollbackTarget(txstate.NFTablesRollback{Family: desired.NFT.Family, Table: desired.NFT.Table, Owner: desired.NFT.Owner}) && rawNFTablesRollbackEntryValid(txstate.NFTablesRollback{Family: desired.NFT.Family, Table: desired.NFT.Table, Owner: desired.NFT.Owner})
	default:
		return false
	}
}

func desiredAppliedStepTargetCount(steps []txstate.PlannedStep, kind, target, owner string) int {
	matches := 0
	for _, step := range steps {
		if step.Kind == kind && strings.TrimSpace(step.Target) == strings.TrimSpace(target) && ownedRollbackMetadata(step.Owner, owner) {
			matches++
		}
	}
	return matches
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
