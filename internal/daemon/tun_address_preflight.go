package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
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
		if tx, ok, err := activeCommittedTransaction(status, m.runtimeDir()); err == nil && ok {
			if allowance, ok := transactionExactTunAddressAllowance(tx, plan.TunAddress.CIDR, plan.Snapshot); ok {
				allowed = append(allowed, allowance)
			}
		}
	}
	if allowance, ok := validatedRecoverableTunAddressAllowance(m.runtimeDir(), plan.TunAddress.CIDR, plan.Snapshot); ok {
		allowed = append(allowed, allowance)
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

func transactionExactTunAddressAllowance(tx txstate.Transaction, cidr string, s netsnapshot.Snapshot) (tunAddressPreflightAllowance, bool) {
	var matched *txstate.TUNAddressRollback
	for i := range tx.Rollback.TUNAddresses {
		address := tx.Rollback.TUNAddresses[i]
		if address.Owner != netexecutor.OwnerTunAddress ||
			address.InterfaceName != netsnapshot.DefaultTunName ||
			strings.TrimSpace(address.CIDR) != strings.TrimSpace(cidr) ||
			address.LinkIndex <= 0 || address.LinkKind != "tun" || !address.AppearedAfterCore {
			continue
		}
		if matched != nil {
			return tunAddressPreflightAllowance{}, false
		}
		matched = &tx.Rollback.TUNAddresses[i]
	}
	if matched == nil || !snapshotProvesExactTunAddress(*matched, s) {
		return tunAddressPreflightAllowance{}, false
	}
	return exactPodlazTunAddressAllowance(matched.CIDR), true
}

func validatedRecoverableTunAddressInterface(runtimeDir, cidr string) (string, bool) {
	if allowance, ok := validatedRecoverableTunAddressAllowance(runtimeDir, cidr, netsnapshot.Snapshot{}); ok {
		return allowance.Interface, true
	}
	return "", false
}

func validatedRecoverableTunAddressAllowance(runtimeDir, cidr string, s netsnapshot.Snapshot) (tunAddressPreflightAllowance, bool) {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 {
		return tunAddressPreflightAllowance{}, false
	}
	var allowance tunAddressPreflightAllowance
	matches := 0
	for _, summary := range summaries {
		if !summary.RequiresRecovery {
			continue
		}
		tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(summary.ID)
		if err != nil {
			return tunAddressPreflightAllowance{}, false
		}
		if a, ok := transactionExactTunAddressAllowance(tx, cidr, s); ok {
			matches++
			allowance = a
			continue
		}
		if tx.State != txstate.TransactionApplying {
			continue
		}
		address := tx.DesiredPlan.TUNAddress
		candidate := txstate.TUNAddressRollback{
			Family:            address.Family,
			InterfaceName:     address.InterfaceName,
			CIDR:              address.CIDR,
			Scope:             address.Scope,
			LinkIndex:         address.LinkIndex,
			LinkKind:          address.LinkKind,
			AppearedAfterCore: address.AppearedAfterCore,
			Owner:             address.Owner,
		}
		if address.Owner == netexecutor.OwnerTunAddress && address.InterfaceName == netsnapshot.DefaultTunName &&
			strings.TrimSpace(address.CIDR) == strings.TrimSpace(cidr) && address.LinkIndex > 0 &&
			address.LinkKind == "tun" && address.AppearedAfterCore && snapshotProvesExactTunAddress(candidate, s) {
			matches++
			allowance = exactPodlazTunAddressAllowance(address.CIDR)
		}
	}
	return allowance, matches == 1
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

func snapshotProvesExactTunAddress(address txstate.TUNAddressRollback, s netsnapshot.Snapshot) bool {
	if s.IPv4Addresses.Inspection.Status != netsnapshot.StatusDetected || s.IPv4Routes.Inspection.Status != netsnapshot.StatusDetected {
		return false
	}
	if !snapshotProvesTunLinkIdentity(address, s) {
		return false
	}
	exactAddresses := 0
	for _, item := range s.IPv4Addresses.Addresses {
		if item.Interface == address.InterfaceName && strings.TrimSpace(item.CIDR) == strings.TrimSpace(address.CIDR) {
			if strings.TrimSpace(item.Scope) != "global" {
				return false
			}
			exactAddresses++
		}
	}
	if exactAddresses != 1 {
		return false
	}
	exactRoutes := 0
	for _, route := range s.IPv4Routes.Routes {
		if kernelGeneratedLocalRouteForAddress(route, address.CIDR, address.InterfaceName) {
			exactRoutes++
		}
	}
	return exactRoutes == 1
}

func snapshotProvesTunLinkIdentity(address txstate.TUNAddressRollback, s netsnapshot.Snapshot) bool {
	for _, device := range s.TunDevices {
		if device.Name != address.InterfaceName || device.Status != netsnapshot.StatusDetected {
			continue
		}
		observation := strings.TrimSpace(device.Detail)
		if observation == "" {
			observation = strings.TrimSpace(device.Raw)
		}
		index, ok := tunDeviceRawIndex(observation)
		if !ok || index != address.LinkIndex {
			return false
		}
		return tunDeviceRawKind(observation) == address.LinkKind
	}
	return false
}

func tunDeviceRawIndex(raw string) (int, bool) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return 0, false
	}
	index, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":"))
	return index, err == nil && index > 0
}

func tunDeviceRawKind(raw string) string {
	fields := strings.Fields(strings.ToLower(raw))
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "type" && fields[i+1] == "tun" {
			return "tun"
		}
	}
	if strings.Contains(strings.ToLower(raw), " tun ") && strings.Contains(strings.ToLower(raw), " type tun") {
		return "tun"
	}
	return ""
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
	if strings.ToLower(strings.TrimSpace(route.Table)) != "local" {
		return false
	}
	raw := strings.ToLower(strings.TrimSpace(route.Raw + " " + route.Detail))
	return strings.Contains(raw, "local") && strings.Contains(raw, "dev "+strings.ToLower(iface)) && strings.Contains(raw, "proto kernel") && strings.Contains(raw, "scope host")
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
