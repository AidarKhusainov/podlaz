package daemon

import (
	"github.com/AidarKhusainov/podlaz/internal/api"
)

func withNetworkSessionRecoveryStatus(
	status api.StatusResponse,
	continuation networkSessionContinuationStore,
	gate *networkSessionStartupMutationGate,
) api.StatusResponse {
	if status.StartupScan == nil {
		status.StartupScan = &api.StartupScanStatus{Status: api.StartupScanStatusClean}
	}
	plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
	if err != nil {
		status.StartupScan.Warnings = append(status.StartupScan.Warnings, api.RecoveryWarning{
			Target:  "network session authority",
			Message: "current-boot Network Session recovery authority could not be inspected",
		})
		status.StartupScan.Status = startupScanStatusFromPublished(*status.StartupScan)
		if status.StartupScan.SuggestedAction == "" {
			status.StartupScan.SuggestedAction = "podlaz doctor"
		}
		return status
	}
	if plan == nil {
		return status
	}
	status.StartupScan.NetworkSession = plan
	status.StartupScan.Status = startupScanStatusFromPublished(*status.StartupScan)
	status.StartupScan.SuggestedAction = "podlaz recover"
	return status
}

func startupScanStatusFromPublished(scan api.StartupScanStatus) string {
	hasWork := len(scan.Candidates) > 0 || scan.NetworkSession != nil
	hasWarnings := len(scan.Warnings) > 0
	switch {
	case hasWork && hasWarnings:
		return api.StartupScanStatusStaleIncomplete
	case hasWork:
		return api.StartupScanStatusStale
	case hasWarnings:
		return api.StartupScanStatusIncomplete
	default:
		return api.StartupScanStatusClean
	}
}
