package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestResumeNetworkSessionConvergesPersistedTerminalIntentWithoutReconnect(t *testing.T) {
	for _, intent := range []networkSessionIntent{networkSessionIntentDisconnect, networkSessionIntentTerminal} {
		t.Run(string(intent), func(t *testing.T) {
			runtimeDir := t.TempDir()
			continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
			if err := continuation.Save(testContinuationRequest()); err != nil {
				t.Fatalf("save network session: %v", err)
			}
			if err := continuation.stateStore().SetIntent(intent); err != nil {
				t.Fatalf("persist teardown intent: %v", err)
			}

			events := []string{}
			continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
				events = append(events, "exact-recovery")
				return api.RecoveryResponse{Mode: "execute"}
			}
			continuation.continueTeardown = func(_ context.Context, store networkSessionStateStore) error {
				events = append(events, "teardown-converged")
				return store.Remove()
			}
			lifecycle := newNetworkSessionLifecycle(networkSessionRecordingLifecycle{events: &events}, continuation)
			resumed, err := resumeNetworkSession(
				context.Background(),
				continuation,
				lifecycle,
				func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
				func(context.Context, api.StatusResponse) api.RecoveryResponse {
					events = append(events, "generic-recovery")
					return api.RecoveryResponse{Mode: "execute"}
				},
			)
			if err != nil {
				t.Fatalf("converge persisted %s intent: %v", intent, err)
			}
			if resumed {
				t.Fatalf("persisted %s teardown must never resume the VPN", intent)
			}
			for _, event := range events {
				if event == "connect" {
					t.Fatalf("persisted %s teardown reconnected: %#v", intent, events)
				}
			}
			if _, exists, loadErr := continuation.stateStore().Load(); loadErr != nil || exists {
				t.Fatalf("persisted %s teardown did not clear converged authority: exists=%v err=%v", intent, exists, loadErr)
			}
		})
	}
}
