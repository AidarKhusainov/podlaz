package daemon

import (
	"context"
	"errors"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

var errAutomaticRecoveryIncomplete = errors.New("unable to connect: exact-owned recovery did not converge safely; Podlaz may have cleaned previously owned stale state, but did not start a new VPN connection; run podlaz debug recover")

var automaticPodlazRecover = func(ctx context.Context, runtimeDir string) error {
	result := recovery.ExecuteWithOptions(ctx, recovery.Options{
		RuntimeDir: runtimeDir,
		Executor:   recovery.NetworkSessionCleanupExecutor{RuntimeDir: runtimeDir},
	})
	if automaticRecoveryComplete(result) {
		return nil
	}
	return errAutomaticRecoveryIncomplete
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
		return refreshed, errAutomaticRecoveryIncomplete
	}
	return refreshed, nil
}
