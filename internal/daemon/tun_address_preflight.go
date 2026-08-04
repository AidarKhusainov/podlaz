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
	allowed := make(map[string]struct{})
	policy := api.NormalizeHandoffPolicy(handoff)
	if policy == api.HandoffReplacePodlaz {
		status := m.statusForPublication(ctx)
		if tx, ok, err := activeCommittedTransaction(status, m.runtimeDir()); err == nil && ok && transactionOwnsExactTunAddress(tx, plan.TunAddress.CIDR) {
			allowed[netsnapshot.DefaultTunName] = struct{}{}
		}
	}
	if iface, ok := validatedRecoverableTunAddressInterface(m.runtimeDir(), plan.TunAddress.CIDR); ok {
		allowed[iface] = struct{}{}
	}
	if policy == api.HandoffStopKnown {
		for _, connection := range activeNetworkManagerVPNConnections(plan.Snapshot) {
			if device := strings.TrimSpace(connection.Device); device != "" {
				allowed[device] = struct{}{}
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

func snapshotWithoutAllowedTunAddressConflicts(s netsnapshot.Snapshot, desiredCIDR string, allowed map[string]struct{}) netsnapshot.Snapshot {
	out := s
	out.IPv4Addresses.Addresses = append([]netsnapshot.IPAddress(nil), s.IPv4Addresses.Addresses...)
	out.IPv4Routes.Routes = append([]netsnapshot.Route(nil), s.IPv4Routes.Routes...)
	addresses := out.IPv4Addresses.Addresses[:0]
	for _, address := range out.IPv4Addresses.Addresses {
		if _, ok := allowed[address.Interface]; ok && ipv4CIDRsOverlap(address.CIDR, desiredCIDR) {
			continue
		}
		addresses = append(addresses, address)
	}
	out.IPv4Addresses.Addresses = addresses
	routes := out.IPv4Routes.Routes[:0]
	for _, route := range out.IPv4Routes.Routes {
		if _, ok := allowed[route.Interface]; ok && ipv4CIDRsOverlap(route.Destination, desiredCIDR) {
			continue
		}
		routes = append(routes, route)
	}
	out.IPv4Routes.Routes = routes
	return out
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
