package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/api"
)

type Server struct {
	RuntimeDir string
	Status     func(context.Context) api.StatusResponse
}

func (s Server) Run(ctx context.Context) error {
	runtimeDir := s.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = api.RuntimeDirFromEnv()
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create runtime directory %s: %w", runtimeDir, err)
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

	mux := http.NewServeMux()
	mux.HandleFunc(api.StatusPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		statusFn := s.Status
		if statusFn == nil {
			statusFn = DefaultStatus
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(statusFn(r.Context()))
	})

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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown daemon API: %w", err)
		}
		return <-errc
	case err := <-errc:
		return err
	}
}

func DefaultStatus(context.Context) api.StatusResponse {
	return api.StatusResponse{Daemon: "running", Connection: "inactive", RuntimeDirectory: "present", Proxy: "inactive", TUN: "disabled"}
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
