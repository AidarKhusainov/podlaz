package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func requireTunAddressPreflight(plan planner.TunPlan) error {
	address := plan.TunAddress
	if strings.TrimSpace(address.CIDR) == "" {
		return errors.New("TUN plan is missing the daemon-owned IPv4 address")
	}
	switch address.Action {
	case planner.TunAddressActionAssign:
		return nil
	case planner.TunAddressActionBlocked:
		return fmt.Errorf("%w: %s", netexecutor.ErrTunAddressConflict, strings.TrimSpace(address.Reason))
	case planner.TunAddressActionDaemonRecheck:
		return fmt.Errorf("%w: authoritative daemon-side IPv4 address and route inspection is incomplete: %s", netexecutor.ErrTunAddressConflict, strings.TrimSpace(address.Reason))
	default:
		return fmt.Errorf("%w: unsupported TUN address action %q", netexecutor.ErrTunAddressConflict, address.Action)
	}
}

// requireTunAddressPreflightBeforeHandoff blocks unrelated or incomplete host
// state before active replacement, controlled recovery, or NetworkManager
// handoff. It temporarily ignores only resources whose removal is already
// authorized by exact active/stale transaction identity or by stop-known for a
// concrete NetworkManager connection. The normal post-handoff preflight still
// rechecks the fresh authoritative snapshot without allowances.
func (m *XrayManager) requireTunAddressPreflightBeforeHandoff(ctx context.Context, plan planner.TunPlan, handoff string) error {
	var allowed []tunAddressPreflightAllowance
	policy := api.NormalizeHandoffPolicy(handoff)
	if policy == api.HandoffReplacePodlaz {
		status := m.statusForPublication(ctx)
		if tx, ok, err := activeCommittedTransaction(status, m.runtimeDir()); err == nil && ok && transactionOwnsExactTunAddress(tx, plan.TunAddress.CIDR) {
			allowed = append(allowed, exactPodlazTunAddressAllowance(plan.TunAddress.CIDR))
		}
	}
	if iface, ok := validatedRecoverableTunAddressInterface(m.runtimeDir(), plan.TunAddress.CIDR); ok && iface == netsnapshot.DefaultTunName {
		allowed = append(allowed, exactPodlazTunAddressAllowance(plan.TunAddress.CIDR))
	}
	if policy == api.HandoffStopKnown {
		for _, connection := range activeNetworkManagerVPNConnections(plan.Snapshot) {
			if device := strings.TrimSpace(connection.Device); device != "" {
				allowed = append(allowed, tunAddressPreflightAllowance{Interface: device, InterfaceWide: true})
			}
		}
	}
	if len(allowed) == 0 {
		return requireTunAddressPreflight(plan)
	}
	filtered := plan
	filtered.Snapshot = snapshotWithoutAllowedTunAddressConflicts(plan.Snapshot, plan.TunAddress.CIDR, allowed)
	filtered.TunAddress = planner.PlanTunAddress(filtered.Snapshot)
	return requireTunAddressPreflight(filtered)
}

func transactionOwnsExactTunAddress(tx txstate.Transaction, cidr string) bool {
	matches := 0
	for _, address := range tx.Rollback.TUNAddresses {
		if address.Owner != netexecutor.OwnerTunAddress ||
			address.InterfaceName != netsnapshot.DefaultTunName ||
			strings.TrimSpace(address.CIDR) != strings.TrimSpace(cidr) ||
			address.LinkIndex <= 0 || address.LinkKind != "tun" || !address.AppearedAfterCore {
			continue
		}
		matches++
	}
	return matches == 1
}

func validatedRecoverableTunAddressInterface(runtimeDir, cidr string) (string, bool) {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 {
		return "", false
	}
	matches := 0
	for _, summary := range summaries {
		if !summary.RequiresRecovery {
			continue
		}
		tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(summary.ID)
		if err != nil {
			return "", false
		}
		if transactionOwnsExactTunAddress(tx, cidr) {
			matches++
			continue
		}
		if tx.State != txstate.TransactionApplying {
			continue
		}
		address := tx.DesiredPlan.TUNAddress
		if address.Owner == netexecutor.OwnerTunAddress && address.InterfaceName == netsnapshot.DefaultTunName &&
			strings.TrimSpace(address.CIDR) == strings.TrimSpace(cidr) && address.LinkIndex > 0 &&
			address.LinkKind == "tun" && address.AppearedAfterCore {
			matches++
		}
	}
	return netsnapshot.DefaultTunName, matches == 1
}

type tunAddressPreflightAllowance struct {
	Interface             string
	CIDR                  string
	InterfaceWide         bool
	AllowKernelLocalRoute bool
}

func exactPodlazTunAddressAllowance(cidr string) tunAddressPreflightAllowance {
	return tunAddressPreflightAllowance{
		Interface:             netsnapshot.DefaultTunName,
		CIDR:                  strings.TrimSpace(cidr),
		AllowKernelLocalRoute: true,
	}
}

func snapshotWithoutAllowedTunAddressConflicts(s netsnapshot.Snapshot, desiredCIDR string, allowed []tunAddressPreflightAllowance) netsnapshot.Snapshot {
	out := s
	out.IPv4Addresses.Addresses = append([]netsnapshot.IPAddress(nil), s.IPv4Addresses.Addresses...)
	out.IPv4Routes.Routes = append([]netsnapshot.Route(nil), s.IPv4Routes.Routes...)
	addresses := out.IPv4Addresses.Addresses[:0]
	for _, address := range out.IPv4Addresses.Addresses {
		if tunAddressAllowed(address, desiredCIDR, allowed) {
			continue
		}
		addresses = append(addresses, address)
	}
	out.IPv4Addresses.Addresses = addresses
	routes := out.IPv4Routes.Routes[:0]
	for _, route := range out.IPv4Routes.Routes {
		if tunAddressRouteAllowed(route, desiredCIDR, allowed) {
			continue
		}
		routes = append(routes, route)
	}
	out.IPv4Routes.Routes = routes
	return out
}

func tunAddressAllowed(address netsnapshot.IPAddress, desiredCIDR string, allowed []tunAddressPreflightAllowance) bool {
	for _, allowance := range allowed {
		if address.Interface != allowance.Interface {
			continue
		}
		if allowance.InterfaceWide && ipv4CIDRsOverlap(address.CIDR, desiredCIDR) {
			return true
		}
		if strings.TrimSpace(address.CIDR) == strings.TrimSpace(allowance.CIDR) && strings.TrimSpace(address.CIDR) == strings.TrimSpace(desiredCIDR) {
			return true
		}
	}
	return false
}

func tunAddressRouteAllowed(route netsnapshot.Route, desiredCIDR string, allowed []tunAddressPreflightAllowance) bool {
	for _, allowance := range allowed {
		if route.Interface != allowance.Interface {
			continue
		}
		if allowance.InterfaceWide && ipv4CIDRsOverlap(route.Destination, desiredCIDR) {
			return true
		}
		if allowance.AllowKernelLocalRoute && kernelGeneratedLocalRouteForAddress(route, desiredCIDR, allowance.Interface) {
			return true
		}
	}
	return false
}

func kernelGeneratedLocalRouteForAddress(route netsnapshot.Route, desiredCIDR, iface string) bool {
	if route.Interface != iface || strings.TrimSpace(route.Gateway) != "" {
		return false
	}
	desiredIP, desiredNet, err := net.ParseCIDR(strings.TrimSpace(desiredCIDR))
	if err != nil || desiredIP.To4() == nil {
		return false
	}
	routeIP, routeNet, err := net.ParseCIDR(strings.TrimSpace(route.Destination))
	if err != nil || routeIP.To4() == nil || !routeIP.Equal(desiredIP) {
		return false
	}
	desiredOnes, desiredBits := desiredNet.Mask.Size()
	routeOnes, routeBits := routeNet.Mask.Size()
	if desiredOnes != routeOnes || desiredBits != routeBits {
		return false
	}
	table := strings.ToLower(strings.TrimSpace(route.Table))
	if table != "" && table != "local" {
		return false
	}
	raw := strings.ToLower(route.Raw + " " + route.Detail)
	return table == "local" || (strings.Contains(raw, "proto kernel") && strings.Contains(raw, "scope host"))
}

func ipv4CIDRsOverlap(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" || left == "default" || left == "0.0.0.0/0" {
		return false
	}
	leftIP, leftNet, leftErr := net.ParseCIDR(left)
	rightIP, rightNet, rightErr := net.ParseCIDR(right)
	if leftErr != nil || rightErr != nil || leftIP.To4() == nil || rightIP.To4() == nil {
		return false
	}
	return leftNet.Contains(rightIP) || rightNet.Contains(leftIP)
}
