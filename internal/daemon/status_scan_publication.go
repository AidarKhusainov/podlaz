package daemon

import (
	"context"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

// startupScanForPublication returns a status and recovery view derived from the
// same publication attempt. A stable TUN core-error state triggers a bounded
// refresh that inherits the request cancellation/deadline. The status is read
// again after that refresh so reconnect/disconnect transitions cannot be paired
// with recovery evidence collected for a different lifecycle state.
func startupScanForPublication(
	ctx context.Context,
	currentStatus func(context.Context) api.StatusResponse,
	lifecycle *XrayManager,
	startupScan *startupScanState,
	runtimeDir string,
	refreshTimeout time.Duration,
) (api.StatusResponse, recovery.PlanResult) {
	if ctx == nil {
		ctx = context.Background()
	}
	status := currentStatus(ctx)
	if startupScan == nil {
		return status, recovery.PlanResult{}
	}

	scan := startupScan.Snapshot()
	if lifecycle == nil || !isStableTunCoreError(status, lifecycle.statusPublicationIdentity()) {
		return status, filterStartupScanForActiveRuntime(scan, status, runtimeDir)
	}
	if refreshTimeout <= 0 {
		refreshTimeout = unexpectedCoreExitRefreshTimeout
	}

	refreshCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
	scan = startupScan.Refresh(refreshCtx)
	cancel()

	status = currentStatus(ctx)
	return status, filterStartupScanForActiveRuntime(scan, status, runtimeDir)
}

func isStableTunCoreError(status api.StatusResponse, identity statusPublicationIdentity) bool {
	return status.Connection == "error (core exited)" &&
		status.Mode == planner.ModeTun &&
		identity.Connection == "error (core exited)" &&
		identity.Mode == planner.ModeTun
}
