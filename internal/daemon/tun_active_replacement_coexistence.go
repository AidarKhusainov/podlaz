package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

// preflightActiveReplacementSessionOwnership proves the active Podlaz session
// identity before replace-podlaz disconnects it. Unrelated baseline networking
// does not participate in that ownership proof.
func (m *XrayManager) preflightActiveReplacementSessionOwnership(ctx context.Context, _ netsnapshot.Snapshot, handoff string) error {
	policy := api.NormalizeHandoffPolicy(handoff)
	if policy != api.HandoffReplacePodlaz {
		return nil
	}
	status := m.statusForPublication(ctx)
	_, ok, err := activeCommittedTransaction(status, m.runtimeDir())
	if err == nil && ok {
		return nil
	}
	detail := "active Podlaz TUN transaction identity could not be proven"
	if err != nil {
		detail = err.Error()
	}
	return &tunHandoffBlocker{
		Policy:    policy,
		Conflicts: []string{detail},
		NextStep:  "Resolve the active Podlaz transaction identity before replacing that session; unrelated host networking will remain untouched.",
	}
}
