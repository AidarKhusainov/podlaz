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
// from desired identities persisted before mutation. It never treats these
// planned identities as cleanup authority; rollback remains applied-step-backed.
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
	for _, step := range tx.DesiredPlan.Steps {
		if strings.TrimSpace(step.Kind) != "policy-rule" || strings.TrimSpace(step.Owner) != netexecutor.OwnerPolicyRule {
			continue
		}
		priority, selector, table, ok := parsePlannedPolicyRuleTarget(step.Target)
		if !ok {
			return planner.TunResourceAllocation{}, fmt.Errorf("persisted TUN transaction has malformed planned policy-rule identity")
		}
		switch {
		case table == planner.MainRoutingTable && strings.HasPrefix(selector, "to "):
			serverMatches++
			allocation.ServerRulePriority = priority
		case table == strconv.Itoa(allocation.RoutingTableID) && selector == planner.IPv4DefaultSelector:
			tunnelMatches++
			allocation.TunnelRulePriority = priority
		}
	}
	if serverMatches != 1 || tunnelMatches != 1 || allocation.ServerRulePriority >= allocation.TunnelRulePriority {
		return planner.TunResourceAllocation{}, fmt.Errorf("persisted TUN transaction has incomplete or ambiguous exact policy-rule allocation")
	}
	return allocation, nil
}

func parsePlannedPolicyRuleTarget(target string) (priority int, selector, table string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(target))
	if len(fields) < 5 || fields[0] != "priority" {
		return 0, "", "", false
	}
	priority, err := strconv.Atoi(fields[1])
	if err != nil || priority <= 0 {
		return 0, "", "", false
	}
	lookup := -1
	for i := 2; i+1 < len(fields); i++ {
		if fields[i] == "lookup" {
			lookup = i
			break
		}
	}
	if lookup <= 2 || lookup+1 != len(fields)-1 {
		return 0, "", "", false
	}
	selector = strings.Join(fields[2:lookup], " ")
	table = fields[lookup+1]
	return priority, selector, table, selector != "" && table != ""
}
