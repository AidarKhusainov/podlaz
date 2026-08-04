package daemon

import (
	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func preflightActiveReplacementOwnership(s netsnapshot.Snapshot, handoff string) error {
	policy := api.NormalizeHandoffPolicy(handoff)
	if policy != api.HandoffReplacePodlaz {
		return nil
	}
	conflicts := foreignOwnershipConflicts(s)
	if len(conflicts) == 0 {
		return nil
	}
	return &tunHandoffBlocker{
		Policy:    policy,
		Conflicts: conflicts,
		NextStep:  "Resolve unrelated foreign VPN/DNS/routing ownership before replacing the active podlaz TUN session.",
	}
}
