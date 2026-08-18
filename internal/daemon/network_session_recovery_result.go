package daemon

import "github.com/AidarKhusainov/podlaz/internal/api"

func applyNetworkSessionResumeResult(
	response api.RecoveryResponse,
	gate *networkSessionStartupMutationGate,
	resumeErr error,
) api.RecoveryResponse {
	if resumeErr != nil {
		return withNetworkSessionResumeWarning(response)
	}
	if gate != nil {
		gate.Release()
	}
	return response
}
