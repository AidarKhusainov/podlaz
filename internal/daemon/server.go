package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
)

const (
	daemonShutdownTimeout  = tunRollbackCleanupTimeout + 2*defaultStopTimeout + 5*time.Second
	defaultDaemonStateDir  = "/var/lib/podlaz"
	systemdStateDirectoryEnv = "STATE_DIRECTORY"
)

type Server struct {
	RuntimeDir     string
	StateDir       string
	Status         func(context.Context) api.StatusResponse
	Doctor         func(context.Context) api.DoctorResponse
	Lifecycle      *XrayManager
	Authorizer     Authorizer
	ShutdownIntent func() ShutdownIntent
	startupScan    startupScanFunc
	bootID         bootIDReader
}

func (s Server) Run(ctx context.Context) error {
	runtimeDir := s.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = api.RuntimeDirFromEnv()
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create runtime directory %s: %w", runtimeDir, err)
	}
	stateDir := daemonStateDir(s.StateDir, runtimeDir)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create daemon state directory %s: %w", stateDir, err)
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
	currentStatus := func(statusCtx context.Context) api.StatusResponse {
		statusFn := lifecycle.Status
		if s.Status != nil {
			statusFn = s.Status
		}
		status := lifecycle.statusForPublicationFrom(statusCtx, statusFn)
		return decorateTunHealth(status, healthRuntime)
	}

	lockPath := api.LockPath(runtimeDir)
	lock, err := acquireDaemonLock(lockPath)
	if err != nil {
		if errors.Is(err, errDaemonLockHeld) {
			return fmt.Errorf("daemon lock %s is held by another podlazd process: %w", lockPath, err)
		}
		return err
	}
	defer lock.Close()

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
	defer cancelEvents()
	go coordinator.Run(eventCtx)

	manifestStore := newBootAutostartManifestStore(stateDir, s.bootID)
	attemptStore := newBootAutostartAttemptStore(runtimeDir, s.bootID)
	startupResult, startupErr := runBootAutostartStartup(
		ctx,
		manifestStore,
		attemptStore,
		continuation,
		startupMutationGate,
		func(resumeCtx context.Context) (bool, error) {
			return resumeNetworkSession(
				resumeCtx,
				continuation,
				lockedLifecycle,
				currentStatus,
				func(recoveryCtx context.Context, status api.StatusResponse) api.RecoveryResponse {
					return daemonRecover(recoveryCtx, runtimeDir, status)
				},
			)
		},
	)
	switch {
	case startupErr == nil && startupResult == bootAutostartStartupConnected:
		log.Printf("podlazd: boot autostart connection established")
	case startupErr == nil && startupResult == bootAutostartStartupContinued:
		log.Printf("podlazd: current-boot network session continuation converged")
	case startupErr == nil && startupResult == bootAutostartStartupTerminal:
		log.Printf("podlazd: boot autostart lifecycle reached terminal outcome")
	case startupResult == bootAutostartStartupRecoveryFailed:
		startupMutationGate.Block()
		if errors.Is(startupErr, errNetworkSessionRecoveryIncomplete) {
			log.Printf("podlazd: current-boot network session continuation paused because exact startup recovery is incomplete")
		} else {
			log.Printf("podlazd: current-boot network session continuation could not be resumed")
		}
	case startupErr != nil:
		_, sessionExists, stateErr := continuation.stateStore().Load()
		if stateErr != nil || sessionExists {
			startupMutationGate.Block()
		}
		if startupResult == bootAutostartStartupTerminal {
			log.Printf("podlazd: boot autostart lifecycle reached terminal outcome")
		} else {
			log.Printf("podlazd: boot autostart was not started because startup authority is unavailable")
		}
	}
	refreshStartupScan(ctx)

	socketPath := api.SocketPath(runtimeDir)
	if err := removeStaleSocket(socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on daemon socket %s: %w", socketPath, err)
	}
	listeners := []net.Listener{listener}
	defer func() { _ = listener.Close(); _ = os.Remove(socketPath) }()
	if err := os.Chmod(socketPath, 0o660); err != nil {
		return fmt.Errorf("set daemon socket permissions %s: %w", socketPath, err)
	}
	log.Printf("podlazd: daemon API listening on Unix socket")

	if shouldListenOnAbstractSocket(authorizer) {
		abstractListener, err := net.Listen("unix", api.AbstractSocketAddress())
		if err != nil {
			return fmt.Errorf("listen on packaged daemon abstract socket: %w", err)
		}
		listeners = append(listeners, abstractListener)
		defer abstractListener.Close()
		log.Printf("podlazd: packaged daemon API listening on abstract Unix socket")
	}

	startTunNetworkEventSources(eventCtx, coordinator.Notify)

	mux := http.NewServeMux()
	mux.HandleFunc(api.StatusPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("podlazd: status request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		status, scan := startupScanForPublication(
			r.Context(), currentStatus, lifecycle, startupScan, runtimeDir, unexpectedCoreExitRefreshTimeout,
		)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(withStartupScanStatus(status, scan))
		log.Printf("podlazd: status request handled")
	})
	mux.HandleFunc(api.DoctorPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("podlazd: doctor request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		mutationBefore := operationLock.doctorMutationSnapshot()
		lifecycleSnapshot := lifecycle.captureDoctorLifecycleSnapshot()
		status, scan := startupScanForPublication(
			r.Context(), currentStatus, lifecycle, startupScan, runtimeDir, unexpectedCoreExitRefreshTimeout,
		)

		var response api.DoctorResponse
		if s.Doctor != nil {
			response = s.Doctor(r.Context())
		} else {
			response = lifecycle.doctorFromSnapshot(r.Context(), lifecycleSnapshot)
		}
		lifecycleAfter := lifecycle.captureDoctorLifecycleSnapshot()
		mutationAfter := operationLock.doctorMutationSnapshot()

		stable := !mutationBefore.pending && !mutationAfter.pending && mutationBefore.generation == mutationAfter.generation
		if s.Doctor == nil {
			stable = doctorPublicationLifecycleStable(mutationBefore, mutationAfter, lifecycleSnapshot, lifecycleAfter, status)
		}
		if stable {
			response = withStartupScanDoctor(response, scan, status)
		} else {
			response = withIncompleteDoctorLifecycle(response, scan)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		log.Printf("podlazd: doctor request handled")
	})
	registerTunDiagnosticsHandler(mux, lifecycle)
	mux.HandleFunc(api.RecoverPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("podlazd: recover request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := authorizeHTTPRequest(r, authorizer, ActionRecoverExecute); err != nil {
			writeAuthorizationHTTPError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		response := operationLock.runRecoveryWithFollowUp(
			r.Context(),
			func() api.RecoveryResponse {
				blocked := startupMutationGate.Blocked()
				response := networkSessionRecoveryInitialStage(blocked, func() api.RecoveryResponse {
					return daemonRecover(r.Context(), runtimeDir, currentStatus(r.Context()))
				})
				if !blocked {
					refreshCtx, cancel := boundedStartupScanRefreshContext(r.Context())
					forceRefreshStartupScan(refreshCtx)
					cancel()
				}
				return response
			},
			func(response api.RecoveryResponse) api.RecoveryResponse {
				if !startupMutationGate.Blocked() || !networkSessionRecoveryConverged(response) {
					return response
				}

				var genericResponse api.RecoveryResponse
				_, retryErr := resumeNetworkSession(
					r.Context(),
					continuation,
					sessionLifecycle,
					currentStatus,
					func(recoveryCtx context.Context, status api.StatusResponse) api.RecoveryResponse {
						genericResponse = daemonRecover(recoveryCtx, runtimeDir, status)
						return genericResponse
					},
				)
				if genericResponse.Mode != "" {
					response = genericResponse
				}
				response = applyNetworkSessionResumeResult(response, startupMutationGate, retryErr)
				refreshCtx, cancel := boundedStartupScanRefreshContext(r.Context())
				forceRefreshStartupScan(refreshCtx)
				cancel()
				if retryErr != nil {
					log.Printf("podlazd: network session startup recovery remains incomplete after recovery request")
				}
				return response
			},
		)
		_ = json.NewEncoder(w).Encode(response)
		log.Printf("podlazd: recover request handled")
	})
	registerBootAutostartHandlers(mux, manifestStore, authorizer)
	registerLifecycleHandlers(mux, startupMutationGate, authorizer)

	httpServer := http.Server{
		Handler: mux,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if subject, ok := peerSubjectFromConn(conn); ok {
				return contextWithPeerSubject(ctx, subject)
			}
			return ctx
		},
	}
	errc := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func(ln net.Listener) {
			err := httpServer.Serve(ln)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
				return
			}
			errc <- nil
		}(ln)
	}

	select {
	case <-ctx.Done():
		return shutdownDaemonServer(
			context.Background(), &httpServer, errc, len(listeners), cancelEvents,
			operationLock, sessionLifecycle, normalizeShutdownIntent(s.shutdownIntent()), nil,
		)
	case serveErr := <-errc:
		return shutdownDaemonServer(
			context.Background(), &httpServer, errc, len(listeners)-1, cancelEvents,
			operationLock, sessionLifecycle, ShutdownRestart, serveErr,
		)
	}
}

func daemonStateDir(explicit, runtimeDir string) string {
	if explicit != "" {
		return explicit
	}
	if systemdStateDir := os.Getenv(systemdStateDirectoryEnv); systemdStateDir != "" {
		return systemdStateDir
	}
	if api.ServiceFromEnv() == api.ServiceSystemd {
		return defaultDaemonStateDir
	}
	return filepath.Join(runtimeDir, "state")
}

func (s Server) shutdownIntent() ShutdownIntent {
	if s.ShutdownIntent == nil {
		return ShutdownStop
	}
	return s.ShutdownIntent()
}

func shutdownDaemonServer(
	ctx context.Context,
	httpServer *http.Server,
	errc <-chan error,
	remainingServeResults int,
	cancelEvents context.CancelFunc,
	operationLock *lifecycleOperationLock,
	sessionLifecycle *networkSessionLifecycle,
	intent ShutdownIntent,
	initialServeErr error,
) error {
	cancelEvents()
	shutdownCtx, cancel := context.WithTimeout(ctx, daemonShutdownTimeout)
	defer cancel()

	operationLock.fenceMutations()
	var stopIntentErr error
	if intent == ShutdownStop {
		stopIntentErr = sessionLifecycle.disarmForExplicitStop()
	}

	apiShutdownResult := make(chan error, 1)
	go func() { apiShutdownResult <- httpServer.Shutdown(shutdownCtx) }()

	var lifecycleErr error
	if drainErr := operationLock.waitMutationIdle(shutdownCtx); drainErr != nil {
		lifecycleErr = errors.Join(stopIntentErr, fmt.Errorf("drain lifecycle mutations before final teardown: %w", drainErr))
	} else {
		lifecycleErr = errors.Join(stopIntentErr, finalShutdownDisconnect(shutdownCtx, operationLock, sessionLifecycle, intent))
	}
	apiShutdownErr := <-apiShutdownResult
	serveErr := errors.Join(initialServeErr, collectServeErrors(errc, remainingServeResults))

	var wrappedLifecycleErr error
	if lifecycleErr != nil {
		wrappedLifecycleErr = fmt.Errorf("shutdown network session: %w", lifecycleErr)
	}
	var wrappedAPIErr error
	if apiShutdownErr != nil {
		wrappedAPIErr = fmt.Errorf("shutdown daemon API: %w", apiShutdownErr)
	}
	return errors.Join(wrappedLifecycleErr, wrappedAPIErr, serveErr)
}

func finalShutdownDisconnect(
	ctx context.Context,
	operationLock *lifecycleOperationLock,
	sessionLifecycle *networkSessionLifecycle,
	intent ShutdownIntent,
) error {
	if operationLock != nil {
		if err := operationLock.acquire(ctx); err != nil {
			return err
		}
		defer operationLock.release()
	}
	if intent == ShutdownRestart {
		_, err := sessionLifecycle.DisconnectForRestart(ctx)
		return err
	}
	_, err := sessionLifecycle.Disconnect(ctx)
	return err
}

func shouldListenOnAbstractSocket(authorizer Authorizer) bool {
	return api.ServiceFromEnv() == api.ServiceSystemd && requiresPeerCredentials(authorizer)
}

func collectServeErrors(errc <-chan error, count int) error {
	var errs []error
	for i := 0; i < count; i++ {
		if err := <-errc; err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func DefaultStatus(context.Context) api.StatusResponse {
	return api.StatusResponse{Daemon: "running", Service: api.ServiceFromEnv(), Connection: "inactive", RuntimeDirectory: "present", Proxy: "inactive", TUN: "disabled"}
}

func DefaultDoctor(ctx context.Context, runtimeDir string) api.DoctorResponse {
	report := doctor.RunWithOptions(ctx, doctor.Options{RuntimeDir: runtimeDir, RuntimeDirOwnedByDaemon: true})
	report = doctor.WithSource(report, doctor.SourceDaemon)
	report = doctor.WithDaemonCheck(report, doctor.SeverityOK, "running")
	return doctor.ToDaemon(report)
}
