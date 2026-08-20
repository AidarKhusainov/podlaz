package planner

import (
	"net/netip"
	"strconv"
	"strings"
)

// IsAllocatedTunIPv4CIDR validates only the bounded address namespace used by
// the session allocator. It is a shape/range check, never cleanup authority.
func IsAllocatedTunIPv4CIDR(cidr string) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 {
		return false
	}
	addr := prefix.Addr().As4()
	return addr[0] == 198 && addr[1] == 18 && addr[2] == 0 && addr[3] >= 1 && addr[3] <= tunAddressAllocationLastHost
}

// IsAllocatedTunRoutingTable validates only the bounded numeric table namespace
// used by the session allocator. Exact transaction proof is still required
// before recovery may mutate such a table.
func IsAllocatedTunRoutingTable(table string) bool {
	value, err := strconv.Atoi(strings.TrimSpace(table))
	return err == nil && value >= TunRoutingTableID && value <= tunRoutingTableAllocationLast
}
