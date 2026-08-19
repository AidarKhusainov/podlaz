package daemon

import (
	"fmt"
	"strconv"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// persistedTunResourceAllocation reconstructs the immutable allocation only
// from exact desired/rollback identities already persisted before mutation.
// It never consults the current host or historical constants to fill gaps.
func persistedTunResourceAllocation(tx txstate.Transaction) (planner.TunResourceAllocation, error) {
	allocation := planner.TunResourceAllocation{TunIPv4CIDR: strings.TrimSpace(tx.DesiredPlan.TUNAddress.CIDR)}
	if allocation.TunIPv4CIDR == "" {
		return planner.TunResourceAllocation{}, fmt.Errorf("persisted TUN transaction has no allocated TUN IPv4 address")
	}

	for _, route := range tx.DesiredPlan.Routes {
		if strings.TrimSpace(route.CIDR) != planner.IPv4DefaultRoute || strings.TrimSpace(route.Dev) != netsnapshot.DefaultTunName {
			continue
		}
		tableID, err := strconv.Atoi(strings.TrimSpace(route.Table))
		if err != nil || tableID <= 0 {
			return planner.TunResourceAllocation{}, fmt.Errorf("persisted TUN transaction has no exact numeric session routing table")
		}
		if allocation.RoutingTableID != 0 && allocation.RoutingTableID != tableID {
			return planner.TunResourceAllocation{}, fmt.Errorf("persisted TUN transaction has ambiguous session routing tables")
		}
		allocation.RoutingTableID = tableID
	}
	if allocation.RoutingTableID == 0 {
		return planner.TunResourceAllocation{}, fmt.Errorf("persisted TUN transaction has no session default route")
	}

	serverMatches := 0
	tunnelMatches := 0
	for _, rule := range tx.Rollback.PolicyRules {
		if !ownedRollbackOwner(rule.Owner, netexecutor.OwnerPolicyRule) || rule.Priority <= 0 {
			continue
		}
		table := strings.TrimSpace(rule.Table)
		switch {
		case table == planner.MainRoutingTable && strings.TrimSpace(rule.To) != "":
			serverMatches++
			allocation.ServerRulePriority = rule.Priority
		case table == strconv.Itoa(allocation.RoutingTableID) && strings.TrimSpace(rule.From) == "all":
			tunnelMatches++
			allocation.TunnelRulePriority = rule.Priority
		}
	}
	if serverMatches != 1 || tunnelMatches != 1 || allocation.ServerRulePriority >= allocation.TunnelRulePriority {
		return planner.TunResourceAllocation{}, fmt.Errorf("persisted TUN transaction has incomplete or ambiguous exact policy-rule allocation")
	}
	return allocation, nil
}
