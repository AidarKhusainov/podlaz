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

// autoRecoverTunOwnedState converges only durable exact transaction authority.
// Historical routing values or foreign baseline objects are not recovery
// candidates by numeric/name resemblance. The refreshed snapshot is returned
// for a new independent session allocation after recovery completes.
func (m *XrayManager) autoRecoverTunOwnedState(ctx context.Context, s netsnapshot.Snapshot, handoff string, opts netsnapshot.Options) (netsnapshot.Snapshot, error) {
	if api.NormalizeHandoffPolicy(handoff) == api.HandoffAsk {
		return s, nil
	}
	resources, _ := m.transactionFileStaleState()
	if len(resources) == 0 {
		return s, nil
	}
	if err := automaticPodlazRecover(ctx, m.runtimeDir()); err != nil {
		return s, err
	}
	refreshed := m.collectTunResourceSnapshot(ctx, opts)
	remaining, _ := m.transactionFileStaleState()
	if len(remaining) != 0 {
		return refreshed, fmt.Errorf("automatic podlaz recovery left %d exact transaction state item(s); refusing a new network mutation", len(remaining))
	}
	return refreshed, nil
}
