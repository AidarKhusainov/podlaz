package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestNetworkSessionRecoveryInitialStageDefersGenericMutationWhileStartupGateBlocked(t *testing.T) {
	calls := 0
	generic := func() api.RecoveryResponse {
		calls++
		return api.RecoveryResponse{Mode: "execute", Results: []api.RecoveryCleanupResult{{Status: "recovered"}}}
	}

	response := networkSessionRecoveryInitialStage(true, generic)
	if calls != 0 {
		t.Fatalf("blocked startup gate ran generic recovery before session convergence: calls=%d", calls)
	}
	if response.Mode != "execute" || len(response.Results) != 0 || len(response.Warnings) != 0 {
		t.Fatalf("blocked startup gate initial stage = %#v, want neutral execute response", response)
	}

	response = networkSessionRecoveryInitialStage(false, generic)
	if calls != 1 {
		t.Fatalf("open startup gate did not run normal generic recovery: calls=%d", calls)
	}
	if len(response.Results) != 1 || response.Results[0].Status != "recovered" {
		t.Fatalf("open startup gate lost generic recovery response: %#v", response)
	}
}
