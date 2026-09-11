package daemon

import (
	"errors"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func applyNetworkSessionResumeResult(
	response api.RecoveryResponse,
	gate *networkSessionStartupMutationGate,
	result networkSessionResumeResult,
	resumeErr error,
) api.RecoveryResponse {
	if resumeErr != nil {
		response.NetworkSession = failedNetworkSessionRecoveryState(response.NetworkSession, resumeErr)
		return withNetworkSessionResumeWarning(response, resumeErr)
	}

	switch result {
	case networkSessionResumeResumed:
		response.NetworkSession = successfulNetworkSessionRecoveryState(response.NetworkSession)
	case networkSessionResumeTerminalConverged, networkSessionResumeNoSession:
		response.NetworkSession = nil
	default:
		resumeErr = errors.New("network session resume returned no semantic result")
		response.NetworkSession = failedNetworkSessionRecoveryState(response.NetworkSession, resumeErr)
		return withNetworkSessionResumeWarning(response, resumeErr)
	}
	if gate != nil {
		gate.Release()
	}
	return response
}
