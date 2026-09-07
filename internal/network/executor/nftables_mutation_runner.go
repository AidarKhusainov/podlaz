package executor

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

// nftKernelMutationRunner is a private structural seam for deterministic
// cross-package boundary tests. Production CommandRunner implementations do not
// satisfy it and therefore use the real netlink backend.
type nftKernelMutationRunner interface {
	NftablesGeneration(context.Context) (uint32, error)
	NftablesRemoveTable(context.Context, string, string, uint64, uint32) error
	NftablesReplaceTable(context.Context, string, string, uint64, uint32, planner.TunFirewallPlan) error
}

func nftMutationBackendFromRunner(runner CommandRunner) *nftMutationBackend {
	provider, ok := runner.(nftKernelMutationRunner)
	if !ok {
		return nil
	}
	return &nftMutationBackend{
		getGeneration: provider.NftablesGeneration,
		removeTable: func(ctx context.Context, target nftMutationTarget) error {
			return provider.NftablesRemoveTable(ctx, target.Family, target.Table, target.Handle, target.Generation)
		},
		replaceTable: func(ctx context.Context, target nftMutationTarget, plan planner.TunFirewallPlan) error {
			return provider.NftablesReplaceTable(ctx, target.Family, target.Table, target.Handle, target.Generation, plan)
		},
	}
}
