package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

var automaticPodlazRecover = func(ctx context.Context, runtimeDir string) error {
	result := recovery.ExecuteWithOptions(ctx, recovery.Options{
		RuntimeDir: runtimeDir,
		Executor:   recovery.DaemonCleanupExecutor{RuntimeDir: runtimeDir},
	})
	if automaticRecoveryComplete(result) {
		return nil
	}
	return fmt.Errorf("automatic podlaz recovery did not fully complete before TUN connect: %s", strings.TrimSpace(result.String()))
}

func automaticRecoveryComplete(result recovery.ExecuteResult) bool {
	if len(result.Warnings) > 0 {
		return false
	}
	for _, cleanup := range result.Results {
		switch cleanup.Status {
		case "recovered":
		case "skipped":
			if cleanup.Candidate.Kind != "dns-link" || !strings.Contains(cleanup.Message, "persisted after revert") {
				return false
			}
		case "failed":
			return false
		default:
			return false
		}
	}
	return true
}

// autoRecoverTunOwnedState removes only unambiguous podlaz-owned stale state
// before a non-interactive TUN connect. A single stale resolved link record may
// remain deferred until Xray recreates podlaz0; DNS Apply refreshes that record
// immediately before writing the new per-link configuration. Foreign or
// ambiguous resources remain governed by the handoff policy and are never
// auto-removed.
func (m *XrayManager) autoRecoverTunOwnedState(ctx context.Context, s netsnapshot.Snapshot, handoff string, opts netsnapshot.Options) (netsnapshot.Snapshot, error) {
	if api.NormalizeHandoffPolicy(handoff) == api.HandoffAsk {
		return s, nil
	}
	s = m.withPodlazRuntimeStaleState(ctx, s)
	if stalePodlazStateBlocker(s) == nil {
		return s, nil
	}
	if err := automaticPodlazRecover(ctx, m.runtimeDir()); err != nil {
		return s, err
	}
	refreshed := m.withPodlazRuntimeStaleState(ctx, m.collectTunSnapshot(ctx, opts))
	if blocker := stalePodlazStateBlocker(refreshed); blocker != nil {
		return refreshed, blocker
	}
	return refreshed, nil
}
