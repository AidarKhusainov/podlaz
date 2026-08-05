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

func (e TunExecutor) VerifyRollbackIdentity(ctx context.Context, plan planner.TunPlan) error {
	if shouldApplyTunAddress(plan.TunAddress) {
		verifier, ok := e.TunAddress.(tunAddressRollbackIdentityVerifier)
		if !ok {
			return fmt.Errorf("TUN address executor cannot prove rollback link identity")
		}
		return verifier.VerifyRollbackIdentity(ctx, plan.TunAddress)
	}
	if rollbackNeedsTUNLinkIdentity(plan) {
		return fmt.Errorf("link-scoped rollback requires exact transaction-bound TUN address identity")
	}
	return nil
}

func rollbackNeedsTUNLinkIdentity(plan planner.TunPlan) bool {
	return strings.TrimSpace(plan.DNS.TargetLink) != "" || strings.TrimSpace(plan.TunDevice.Name) != ""
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
		if plan.AllowMissingLink && resourceMissing(err) {
			return nil
		}
		return fmt.Errorf("inspect TUN link before rollback identity gate: %w", err)
	}
	return verifyBoundIdentity(plan, identity, false)
}
