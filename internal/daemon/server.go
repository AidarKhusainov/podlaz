package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
)

const (
	daemonShutdownTimeout    = tunRollbackCleanupTimeout + 2*defaultStopTimeout + 5*time.Second
	defaultDaemonStateDir    = "/var/lib/podlaz"
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
	runtime, err := s.prepareRuntime(ctx)
	if err != nil {
		return err
	}
	defer runtime.lock.Close()
	defer runtime.cancelEvents()

	manifestStore := s.runStartup(ctx, runtime)

	listeners, cleanupListeners, err := openDaemonListeners(runtime.runtimeDir, runtime.authorizer)
	if err != nil {
		return err
	}
	defer cleanupListeners()

	startTunNetworkEventSources(runtime.eventCtx, runtime.coordinator.Notify)

	httpServer := s.newHTTPServer(runtime, manifestStore)
	errc := serveDaemonHTTP(httpServer, listeners)
	return s.waitForShutdown(ctx, httpServer, errc, len(listeners), runtime)
}

func (s Server) waitForShutdown(
	ctx context.Context,
	httpServer *http.Server,
	errc <-chan error,
	listenerCount int,
	runtime *daemonRuntime,
) error {
	select {
	case <-ctx.Done():
		return shutdownDaemonServer(
			context.Background(), httpServer, errc, listenerCount, runtime.cancelEvents,
			runtime.operationLock, runtime.sessionLifecycle, normalizeShutdownIntent(s.shutdownIntent()), nil,
		)
	case serveErr := <-errc:
		return shutdownDaemonServer(
			context.Background(), httpServer, errc, listenerCount-1, runtime.cancelEvents,
			runtime.operationLock, runtime.sessionLifecycle, ShutdownRestart, serveErr,
		)
	}
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
