package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type daemonRuntime struct {
	runtimeDir              string
	stateDir                string
	lifecycle               *XrayManager
	authorizer              Authorizer
	productPhase            *productLifecyclePhaseTracker
	productTerminalReasons  productTerminalReasonStore
	currentStatus           func(context.Context) api.StatusResponse
	lock                    *daemonLock
	startupScan             *startupScanState
	refreshStartupScan      func(context.Context)
	forceRefreshStartupScan func(context.Context)
	operationLock           *lifecycleOperationLock
	coordinator             *tunRevalidationCoordinator
	continuation            networkSessionContinuationStore
	sessionLifecycle        *networkSessionLifecycle
	lockedLifecycle         lifecycleService
	startupMutationGate     *networkSessionStartupMutationGate
	eventCtx                context.Context
	cancelEvents            context.CancelFunc
}

func (s Server) prepareRuntime(ctx context.Context) (*daemonRuntime, error) {
	runtimeDir := s.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = api.RuntimeDirFromEnv()
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create runtime directory %s: %w", runtimeDir, err)
	}
	stateDir := daemonStateDir(s.StateDir, runtimeDir)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create daemon state directory %s: %w", stateDir, err)
	}

	lifecycle := s.Lifecycle
	if lifecycle == nil {
		lifecycle = NewXrayManager(runtimeDir)
	} else if lifecycle.RuntimeDir == "" {
		lifecycle.RuntimeDir = runtimeDir
	}
	reconciliationRuntime := newProductionTunEvidenceRevalidationRuntime(lifecycle)
	healthRuntime := reconciliationHealthRuntime(reconciliationRuntime)
	authorizer := s.Authorizer
	if authorizer == nil {
		authorizer = authorizerFromEnv()
	}
	productPhase := &productLifecyclePhaseTracker{}
	productTerminalReasons := newProductTerminalReasonStore(runtimeDir, nil)
	currentStatus := func(statusCtx context.Context) api.StatusResponse {
		statusFn := lifecycle.Status
		if s.Status != nil {
			statusFn = s.Status
		}
		status := lifecycle.statusForPublicationFrom(statusCtx, statusFn)
		status = decorateTunHealth(status, healthRuntime)
		return productPhase.decorate(status)
	}

	lockPath := api.LockPath(runtimeDir)
	lock, err := acquireDaemonLock(lockPath)
	if err != nil {
		if errors.Is(err, errDaemonLockHeld) {
			return nil, fmt.Errorf("daemon lock %s is held by another podlazd process: %w", lockPath, err)
		}
		return nil, err
	}

	startupScanFn := s.startupScan
	if startupScanFn == nil {
		startupScanFn = defaultStartupScanFunc(runtimeDir)
	}
	startupScan := newStartupScanState(startupScanFn)
	refreshStartupScan := func(refreshCtx context.Context) {
		scan := startupScan.Refresh(refreshCtx)
		logStartupScan(filterStartupScanForActiveRuntime(scan, currentStatus(refreshCtx), runtimeDir))
	}
	forceRefreshStartupScan := func(refreshCtx context.Context) {
		scan := startupScan.ForceRefresh(refreshCtx)
		logStartupScan(filterStartupScanForActiveRuntime(scan, currentStatus(refreshCtx), runtimeDir))
	}

	operationLock := newLifecycleOperationLock()
	var coordinator *tunRevalidationCoordinator
	retryScheduler := newTunReconciliationRetryScheduler(func(trigger tunRevalidationTrigger) {
		if coordinator != nil {
			coordinator.Notify(trigger)
		}
	})
	var automaticExecutor tunAutomaticDispositionExecutor
	coordinator = newTunAutomaticDispositionCoordinator(
		func(revalidationCtx context.Context, trigger tunRevalidationTrigger) tunAutomaticDisposition {
			var decision tunReconciliationDecision
			if err := operationLock.runRevalidation(revalidationCtx, func() {
				mutation := operationLock.lifecycleMutationSnapshot()
				decision = reconciliationRuntime.RunEvidenceRound(revalidationCtx, trigger, mutation.generation)
			}); err != nil {
				if revalidationCtx.Err() == nil {
					log.Printf("podlazd: TUN reconciliation serialization failed")
				}
				return tunAutomaticDisposition{}
			}
			retryScheduler.Apply(decision)
			if decision.Disposition == nil {
				return tunAutomaticDisposition{}
			}
			return *decision.Disposition
		},
		operationLock.tryAdmitAutomaticMutation,
		func(automaticCtx context.Context, admission *lifecycleAutomaticAdmission, disposition tunAutomaticDisposition) {
			automaticExecutor.Handle(automaticCtx, admission, disposition)
		},
	)
	operationLock.setRevalidationCancel(coordinator.InterruptForMutation)

	healthLifecycle := tunRevalidationLifecycle{
		lifecycle: startupScanRefreshingLifecycle{
			lifecycle:  lifecycle,
			refresh:    forceRefreshStartupScan,
			revalidate: coordinator.Notify,
		},
		runtime:     healthRuntime,
		schedule:    coordinator.Notify,
		cancelRetry: retryScheduler.Cancel,
	}
	continuation := newNetworkSessionContinuationStore(runtimeDir, s.bootID)
	sessionLifecycle := newNetworkSessionLifecycle(healthLifecycle, continuation)
	lockedLifecycle := operationLock.wrap(sessionLifecycle)
	startupMutationGate := newNetworkSessionStartupMutationGate(lockedLifecycle)
	automaticExecutor = tunAutomaticDispositionExecutor{
		reconcile: sessionLifecycle.ReconcileProtectedTun,
		terminal: newProductionTunAutomaticTerminalHandler(
			lifecycle,
			continuation.stateStore(),
			reconciliationRuntime,
			forceRefreshStartupScan,
		),
		retry: retryScheduler,
	}

	eventCtx, cancelEvents := context.WithCancel(ctx)

	return &daemonRuntime{
		runtimeDir:              runtimeDir,
		stateDir:                stateDir,
		lifecycle:               lifecycle,
		authorizer:              authorizer,
		productPhase:            productPhase,
		productTerminalReasons:  productTerminalReasons,
		currentStatus:           currentStatus,
		lock:                    lock,
		startupScan:             startupScan,
		refreshStartupScan:      refreshStartupScan,
		forceRefreshStartupScan: forceRefreshStartupScan,
		operationLock:           operationLock,
		coordinator:             coordinator,
		continuation:            continuation,
		sessionLifecycle:        sessionLifecycle,
		lockedLifecycle:         lockedLifecycle,
		startupMutationGate:     startupMutationGate,
		eventCtx:                eventCtx,
		cancelEvents:            cancelEvents,
	}, nil
}
