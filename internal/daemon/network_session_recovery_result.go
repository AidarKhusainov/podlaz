package daemon

import "github.com/AidarKhusainov/podlaz/internal/api"

func applyNetworkSessionResumeResult(
	response api.RecoveryResponse,
	gate *networkSessionStartupMutationGate,
	resumeErr error,
) api.RecoveryResponse {
	if resumeErr != nil {
		response.NetworkSession = failedNetworkSessionRecoveryState(response.NetworkSession, resumeErr)
		return withNetworkSessionResumeWarning(response)
	}
	response.NetworkSession = successfulNetworkSessionRecoveryState(response.NetworkSession)
	if gate != nil {
		gate.Release()
	}
	return response
}
