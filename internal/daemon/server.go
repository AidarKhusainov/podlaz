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
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
)

type Server struct {
	RuntimeDir  string
	Status      func(context.Context) api.StatusResponse
	Doctor      func(context.Context) api.DoctorResponse
	Lifecycle   *XrayManager
	Authorizer  Authorizer
	startupScan startupScanFunc
}

func (s Server) Run(ctx context.Context) error {
	runtimeDir := s.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = api.RuntimeDirFromEnv()
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create runtime directory %s: %w", runtimeDir, err)
	}

	lifecycle := s.Lifecycle
	if lifecycle == nil {
		lifecycle = NewXrayManager(runtimeDir)
	} else if lifecycle.RuntimeDir == "" {
		lifecycle.RuntimeDir = runtimeDir
	}
	revalidationRuntime := newProductionTunRevalidationRuntime(lifecycle)
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
		return decorateTunHealth(status, revalidationRuntime)
	}

	lockPath := api.LockPath(runtimeDir)
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("daemon lock %s already exists; another podlazd may be running or previous shutdown was unclean", lockPath)
		}
		return fmt.Errorf("create daemon lock %s: %w", lockPath, err)
	}
	defer func() { _ = lock.Close(); _ = os.Remove(lockPath) }()

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

	operationLock := newLifecycleOperationLock()
	var lockedLifecycle lifecycleService
	var terminalHandler tunRevalidationTerminalHandler
	coordinator := newTunRevalidationOutcomeCoordinator(func(revalidationCtx context.Context, trigger tunRevalidationTrigger) tunRevalidationOutcome {
		var outcome tunRevalidationOutcome
		if err := operationLock.runRevalidation(revalidationCtx, func() {
			if trigger == tunRevalidationTriggerInitial {
				outcome = revalidationRuntime.InitializePending(revalidationCtx)
				return
			}
			outcome = revalidationRuntime.Revalidate(revalidationCtx, trigger)
		}); err != nil {
			if revalidationCtx.Err() == nil {
				log.Printf("podlazd: TUN revalidation serialization failed")
			}
			return tunRevalidationOutcome{}
		}
		return outcome
	}, func(terminalCtx context.Context, outcome tunRevalidationOutcome) {
		terminalHandler.Handle(terminalCtx, outcome)
	})
	operationLock.setRevalidationCancel(coordinator.InterruptForMutation)

	healthLifecycle := tunRevalidationLifecycle{
		lifecycle: startupScanRefreshingLifecycle{lifecycle: lifecycle, refresh: forceRefreshStartupScan},
		runtime:   revalidationRuntime,
		schedule:  coordinator.Notify,
	}
	lockedLifecycle = operationLock.wrap(healthLifecycle)
	terminalHandler = tunRevalidationTerminalHandler{
		collect: lifecycle.collectTunRevalidationFailureDiagnostics,
		disconnect: func(cleanupCtx context.Context) error {
			_, err := lockedLifecycle.Disconnect(cleanupCtx)
			return err
		},
		finalize:            lifecycle.finalizeTunFailureDiagnosticRollback,
		markCleanupRequired: revalidationRuntime.MarkCleanupRequired,
		cleanupTimeout:      tunRollbackCleanupTimeout,
	}

	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	go coordinator.Run(eventCtx)
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
		mutationAfter := operationLock.doctorMutationSnapshot()

		stable := !mutationBefore.pending && !mutationAfter.pending && mutationBefore.generation == mutationAfter.generation
		if s.Doctor == nil {
			stable = doctorPublicationLifecycleStable(mutationBefore, mutationAfter, lifecycleSnapshot, status)
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
		response := operationLock.runRecovery(r.Context(), func() api.RecoveryResponse {
			response := daemonRecover(r.Context(), runtimeDir, currentStatus(r.Context()))
			refreshCtx, cancel := boundedStartupScanRefreshContext(r.Context())
			forceRefreshStartupScan(refreshCtx)
			cancel()
			return response
		})
		_ = json.NewEncoder(w).Encode(response)
		log.Printf("podlazd: recover request handled")
	})
	registerLifecycleHandlers(mux, lockedLifecycle, authorizer)

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
		cancelEvents()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = lockedLifecycle.Disconnect(shutdownCtx)
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown daemon API: %w", err)
		}
		return collectServeErrors(errc, len(listeners))
	case err := <-errc:
		cancelEvents()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = lockedLifecycle.Disconnect(shutdownCtx)
		_ = httpServer.Shutdown(shutdownCtx)
		return err
	}
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
