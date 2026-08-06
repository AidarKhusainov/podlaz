package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type tunAddressRollbackIdentityVerifier interface {
	VerifyRollbackIdentity(context.Context, planner.TunAddressPlan) error
}

type tunDeviceRollbackIdentityVerifier interface {
	VerifyRollbackIdentity(context.Context, planner.TunDevicePlan) error
}

func (e TunExecutor) VerifyRollbackIdentity(ctx context.Context, plan planner.TunPlan) error {
	if shouldApplyTunAddress(plan.TunAddress) {
		verifier, ok := e.TunAddress.(tunAddressRollbackIdentityVerifier)
		if !ok {
			return fmt.Errorf("TUN address executor cannot prove rollback link identity")
		}
		return verifier.VerifyRollbackIdentity(ctx, plan.TunAddress)
	}
	if rollbackNeedsTUNLinkIdentity(plan) {
		if strings.TrimSpace(plan.TunDevice.Name) != "" {
			verifier, ok := e.TunDevice.(tunDeviceRollbackIdentityVerifier)
			if !ok {
				return fmt.Errorf("TUN device executor cannot prove rollback link identity")
			}
			return verifier.VerifyRollbackIdentity(ctx, plan.TunDevice)
		}
		return fmt.Errorf("link-scoped rollback requires exact transaction-bound TUN address or TUN creation identity")
	}
	return nil
}

func rollbackNeedsTUNLinkIdentity(plan planner.TunPlan) bool {
	if strings.TrimSpace(plan.DNS.TargetLink) != "" || strings.TrimSpace(plan.TunDevice.Name) != "" {
		return true
	}
	for _, route := range plan.Routes {
		if rollbackRouteRequiresTunIdentity(route.Table, route.Interface) {
			return true
		}
	}
	return false
}

func rollbackRouteRequiresTunIdentity(table, dev string) bool {
	if strings.TrimSpace(dev) == managedTunInterfaceName {
		return true
	}
	switch strings.TrimSpace(table) {
	case planner.TunRoutingTable, "51820":
		return true
	default:
		return false
	}
}

func (e IPTunAddressExecutor) VerifyRollbackIdentity(ctx context.Context, plan planner.TunAddressPlan) error {
	if strings.TrimSpace(plan.CIDR) == "" || strings.TrimSpace(plan.Interface) == "" {
		return nil
	}
	if err := validateBoundTunAddressPlan(plan); err != nil {
		return err
	}
	identity, err := e.inspectIdentity(ctx, plan.Interface)
	if err != nil {
		return fmt.Errorf("inspect TUN link before rollback identity gate: %w", err)
	}
	return verifyBoundIdentity(plan, identity, false)
}
