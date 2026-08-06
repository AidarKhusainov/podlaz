package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func (e IPTunDeviceExecutor) VerifyRollbackIdentity(ctx context.Context, plan planner.TunDevicePlan) error {
	if strings.TrimSpace(plan.Name) == "" {
		return nil
	}
	proof, ok := parseTunDeviceOwnershipDescription(plan.Name, plan.Reason)
	if !ok {
		return fmt.Errorf("link-scoped rollback requires exact transaction-bound TUN creation proof for %s", plan.Name)
	}
	current, err := e.inspectCreationProof(ctx, plan.Name)
	if err != nil {
		return fmt.Errorf("inspect TUN device %s before rollback identity gate: %w", plan.Name, err)
	}
	if current.Name != proof.Name || current.LinkIndex != proof.LinkIndex || current.LinkKind != proof.LinkKind {
		return fmt.Errorf("current TUN identity does not match transaction-bound creation proof: expected name=%s ifindex=%d kind=%s", proof.Name, proof.LinkIndex, proof.LinkKind)
	}
	return nil
}
