package daemon

import "github.com/AidarKhusainov/podlaz/internal/api"

// networkSessionRecoveryInitialStage keeps a blocked startup gate mutation-free.
// Session intent/protection convergence must run inside the serialized follow-up
// through resumeNetworkSession, which owns privacy -> exact data-plane -> generic
// recovery ordering. An open gate retains the normal one-shot recover behavior.
func networkSessionRecoveryInitialStage(blocked bool, recover func() api.RecoveryResponse) api.RecoveryResponse {
	if blocked {
		return api.RecoveryResponse{Mode: "execute"}
	}
	if recover == nil {
		return api.RecoveryResponse{Mode: "execute"}
	}
	return recover()
}
