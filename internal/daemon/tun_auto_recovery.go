package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

// autoRecoverTunOwnedState removes only unambiguous podlaz-owned stale state
// before a non-interactive TUN connect. Foreign or ambiguous resources remain
// governed by the selected handoff policy and are never auto-removed.
func (m *XrayManager) autoRecoverTunOwnedState(ctx context.Context, s netsnapshot.Snapshot, handoff string, opts netsnapshot.Options) (netsnapshot.Snapshot, error) {
	if api.NormalizeHandoffPolicy(handoff) == api.HandoffAsk {
		return s, nil
	}
	s = m.withPodlazRuntimeStaleState(ctx, s)
	if stalePodlazStateBlocker(s) == nil {
		return s, nil
	}
	if err := m.runControlledPodlazRecover(ctx); err != nil {
		return s, err
	}
	refreshed := m.withPodlazRuntimeStaleState(ctx, m.collectTunSnapshot(ctx, opts))
	if blocker := stalePodlazStateBlocker(refreshed); blocker != nil {
		return refreshed, blocker
	}
	return refreshed, nil
}
