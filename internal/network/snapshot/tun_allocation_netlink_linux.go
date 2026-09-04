//go:build linux

package snapshot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const tunAllocationNetlinkTimeout = 3 * time.Second

// CollectTunAllocationEvidence reads the complete IPv4 allocation authority
// from the current Linux network namespace. Interrupted dumps are discarded in
// full and retried; presentation-oriented iproute2 output is never a fallback.
func CollectTunAllocationEvidence(ctx context.Context) (TunAllocationEvidence, error) {
	return retryTunAllocationEvidenceDump(ctx, collectTunAllocationEvidenceOnce)
}

func collectTunAllocationEvidenceOnce(ctx context.Context) (TunAllocationEvidence, error) {
	if err := ctx.Err(); err != nil {
		return TunAllocationEvidence{}, err
	}

	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return TunAllocationEvidence{}, fmt.Errorf("open rtnetlink handle for TUN allocation evidence: %w", err)
	}
	defer handle.Close()

	if err := handle.SetSocketTimeout(netlinkTimeoutForContext(ctx)); err != nil {
		return TunAllocationEvidence{}, fmt.Errorf("configure rtnetlink timeout for TUN allocation evidence: %w", err)
	}

	addresses, err := handle.AddrList(nil, netlink.FAMILY_V4)
	if err != nil {
		return TunAllocationEvidence{}, tunAllocationNetlinkError("dump IPv4 addresses", err)
	}
	if err := ctx.Err(); err != nil {
		return TunAllocationEvidence{}, err
	}

	routes, err := handle.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: unix.RT_TABLE_UNSPEC},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		return TunAllocationEvidence{}, tunAllocationNetlinkError("dump IPv4 routes", err)
	}
	if err := ctx.Err(); err != nil {
		return TunAllocationEvidence{}, err
	}

	rules, err := handle.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return TunAllocationEvidence{}, tunAllocationNetlinkError("dump IPv4 policy rules", err)
	}
	if err := ctx.Err(); err != nil {
		return TunAllocationEvidence{}, err
	}

	links, err := handle.LinkList()
	if err != nil {
		return TunAllocationEvidence{}, tunAllocationNetlinkError("dump links for reserved routing tables", err)
	}
	if err := ctx.Err(); err != nil {
		return TunAllocationEvidence{}, err
	}

	return tunAllocationEvidenceFromNetlink(addresses, routes, rules, links)
}

func netlinkTimeoutForContext(ctx context.Context) time.Duration {
	timeout := tunAllocationNetlinkTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	if timeout < time.Microsecond {
		return time.Microsecond
	}
	return timeout
}

func tunAllocationNetlinkError(operation string, err error) error {
	if errors.Is(err, netlink.ErrDumpInterrupted) {
		return fmt.Errorf("%s: %w", operation, errTunAllocationDumpInterrupted)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func tunAllocationEvidenceFromNetlink(addresses []netlink.Addr, routes []netlink.Route, rules []netlink.Rule, links []netlink.Link) (TunAllocationEvidence, error) {
	evidence := TunAllocationEvidence{
		IPv4Addresses:        make([]netip.Prefix, 0, len(addresses)),
		IPv4Routes:           make([]TunAllocationRoute, 0, len(routes)),
		IPv4PolicyRules:      make([]TunAllocationRule, 0, len(rules)),
		ReservedRoutingTables: make([]uint32, 0),
	}

	for _, address := range addresses {
		prefix, err := ipv4PrefixFromIPNet(address.IPNet, false)
		if err != nil {
			return TunAllocationEvidence{}, fmt.Errorf("convert IPv4 address allocation evidence: %w", err)
		}
		evidence.IPv4Addresses = append(evidence.IPv4Addresses, prefix)
	}

	for _, route := range routes {
		if route.Table <= 0 || uint64(route.Table) > math.MaxUint32 {
			return TunAllocationEvidence{}, fmt.Errorf("convert IPv4 route allocation evidence: invalid routing table %d", route.Table)
		}
		converted := TunAllocationRoute{Table: uint32(route.Table)}
		if route.Dst == nil {
			converted.Default = true
		} else {
			prefix, err := ipv4PrefixFromIPNet(route.Dst, true)
			if err != nil {
				return TunAllocationEvidence{}, fmt.Errorf("convert IPv4 route allocation evidence: %w", err)
			}
			converted.Destination = prefix
		}
		evidence.IPv4Routes = append(evidence.IPv4Routes, converted)
	}

	for _, rule := range rules {
		if rule.Table < 0 || uint64(rule.Table) > math.MaxUint32 {
			return TunAllocationEvidence{}, fmt.Errorf("convert IPv4 policy-rule allocation evidence: invalid routing table %d", rule.Table)
		}
		if rule.Priority < 0 || uint64(rule.Priority) > math.MaxUint32 {
			return TunAllocationEvidence{}, fmt.Errorf("convert IPv4 policy-rule allocation evidence: invalid priority %d", rule.Priority)
		}
		priority := uint32(rule.Priority)
		table := uint32(rule.Table)
		if defaultKernelPolicyRuleNumeric(priority, table) {
			continue
		}
		evidence.IPv4PolicyRules = append(evidence.IPv4PolicyRules, TunAllocationRule{Priority: priority, Table: table})
	}

	reserved := make(map[uint32]struct{})
	for _, link := range links {
		vrf, ok := link.(*netlink.Vrf)
		if !ok {
			continue
		}
		if vrf.Table == 0 {
			return TunAllocationEvidence{}, fmt.Errorf("convert VRF allocation evidence: link %q has no routing table", vrf.Attrs().Name)
		}
		if _, exists := reserved[vrf.Table]; exists {
			continue
		}
		reserved[vrf.Table] = struct{}{}
		evidence.ReservedRoutingTables = append(evidence.ReservedRoutingTables, vrf.Table)
	}

	return evidence, nil
}

func ipv4PrefixFromIPNet(network *net.IPNet, maskNetwork bool) (netip.Prefix, error) {
	if network == nil {
		return netip.Prefix{}, errors.New("missing IPv4 prefix")
	}
	ipv4 := network.IP.To4()
	if ipv4 == nil {
		return netip.Prefix{}, errors.New("non-IPv4 prefix")
	}
	ones, bits := network.Mask.Size()
	if bits != 32 || ones < 0 {
		return netip.Prefix{}, errors.New("invalid IPv4 prefix mask")
	}
	addr, ok := netip.AddrFromSlice(ipv4)
	if !ok || !addr.Is4() {
		return netip.Prefix{}, errors.New("invalid IPv4 address")
	}
	prefix := netip.PrefixFrom(addr, ones)
	if maskNetwork {
		prefix = prefix.Masked()
	}
	return prefix, nil
}

func defaultKernelPolicyRuleNumeric(priority, table uint32) bool {
	switch priority {
	case 0:
		return table == unix.RT_TABLE_LOCAL
	case 32766:
		return table == unix.RT_TABLE_MAIN
	case 32767:
		return table == unix.RT_TABLE_DEFAULT
	default:
		return false
	}
}
