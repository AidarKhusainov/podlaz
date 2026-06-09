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

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/doctor"
)

type Server struct {
	RuntimeDir string
	Status     func(context.Context) api.StatusResponse
	Doctor     func(context.Context) api.DoctorResponse
	Lifecycle  *XrayManager
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

	lockPath := api.LockPath(runtimeDir)
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("daemon lock %s already exists; another tunwardend may be running or previous shutdown was unclean", lockPath)
		}
		return fmt.Errorf("create daemon lock %s: %w", lockPath, err)
	}
	defer func() { _ = lock.Close(); _ = os.Remove(lockPath) }()

	socketPath := api.SocketPath(runtimeDir)
	if err := removeStaleSocket(socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on daemon socket %s: %w", socketPath, err)
	}
	defer func() { _ = listener.Close(); _ = os.Remove(socketPath) }()
	if err := os.Chmod(socketPath, 0o660); err != nil {
		return fmt.Errorf("set daemon socket permissions %s: %w", socketPath, err)
	}
	log.Printf("tunwardend: daemon API listening on Unix socket")

	mux := http.NewServeMux()
	mux.HandleFunc(api.StatusPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("tunwardend: status request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		statusFn := s.Status
		if statusFn == nil {
			statusFn = lifecycle.Status
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(statusFn(r.Context()))
		log.Printf("tunwardend: status request handled")
	})
	mux.HandleFunc(api.DoctorPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("tunwardend: doctor request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		doctorFn := s.Doctor
		if doctorFn == nil {
			doctorFn = lifecycle.Doctor
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doctorFn(r.Context()))
		log.Printf("tunwardend: doctor request handled")
	})
	mux.HandleFunc(api.RecoverPath, func(w http.ResponseWriter, r *http.Request) {
		log.Printf("tunwardend: recover request method=%s path=%s", r.Method, r.URL.Path)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(daemonRecover(r.Context(), runtimeDir))
		log.Printf("tunwardend: recover request handled")
	})
	registerLifecycleHandlers(mux, lifecycle)

	httpServer := http.Server{Handler: mux}
	errc := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	select {
	case <-ctx.Done():
		_, _ = lifecycle.Disconnect(context.Background())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown daemon API: %w", err)
		}
		return <-errc
	case err := <-errc:
		_, _ = lifecycle.Disconnect(context.Background())
		return err
	}
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

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect daemon socket path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("daemon socket path %s exists and is not a Unix socket", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale daemon socket %s: %w", path, err)
	}
	return nil
}
