package daemon

import (
	"context"
	"errors"
	"log"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func (s Server) runStartup(ctx context.Context, runtime *daemonRuntime) bootAutostartManifestStore {
	go runtime.coordinator.Run(runtime.eventCtx)

	manifestStore := newBootAutostartManifestStore(runtime.stateDir, s.bootID)
	attemptStore := newBootAutostartAttemptStore(runtime.runtimeDir, s.bootID)
	startupResult, startupErr := runBootAutostartStartupWithOptions(
		ctx,
		manifestStore,
		attemptStore,
		runtime.continuation,
		runtime.startupMutationGate,
		func(resumeCtx context.Context) (bool, error) {
			return resumeNetworkSession(
				resumeCtx,
				runtime.continuation,
				runtime.lockedLifecycle,
				runtime.currentStatus,
				func(recoveryCtx context.Context, status api.StatusResponse) api.RecoveryResponse {
					return daemonRecover(recoveryCtx, runtime.runtimeDir, status)
				},
			)
		},
		bootAutostartStartupOptions{waitForNetwork: newBootNetworkReadinessWaiter()},
	)
	switch {
	case startupErr == nil && startupResult == bootAutostartStartupConnected:
		log.Printf("podlazd: boot autostart connection established")
	case startupErr == nil && startupResult == bootAutostartStartupContinued:
		log.Printf("podlazd: current-boot network session continuation converged")
	case startupErr == nil && startupResult == bootAutostartStartupTerminal:
		log.Printf("podlazd: boot autostart lifecycle reached terminal outcome")
	case startupResult == bootAutostartStartupRecoveryFailed:
		runtime.startupMutationGate.Block()
		if errors.Is(startupErr, errNetworkSessionRecoveryIncomplete) {
			log.Printf("podlazd: current-boot network session continuation paused because exact startup recovery is incomplete")
		} else {
			log.Printf("podlazd: current-boot network session continuation could not be resumed")
		}
	case startupErr != nil:
		_, sessionExists, stateErr := runtime.continuation.stateStore().Load()
		if stateErr != nil || sessionExists {
			runtime.startupMutationGate.Block()
		}
		if startupResult == bootAutostartStartupTerminal {
			log.Printf("podlazd: boot autostart lifecycle reached terminal outcome")
		} else {
			log.Printf("podlazd: boot autostart was not started because startup authority is unavailable")
		}
	}
	runtime.refreshStartupScan(ctx)
	return manifestStore
}
