package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestRecoveryClientAllowsOperationLongerThanDialTimeout(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.RecoverPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		time.Sleep(900 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RecoveryResponse{
			Mode: "execute",
			Results: []api.RecoveryCleanupResult{{
				Candidate: api.RecoveryCandidate{Kind: "generated-runtime-configs", Description: "generated runtime configs", Target: "/run/podlaz/generated"},
				Status:    "recovered",
			}},
		})
	})}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		if err := <-serverErr; err != nil && err != http.ErrServerClosed {
			t.Fatalf("serve unix socket: %v", err)
		}
	})

	response, err := (RecoveryClient{
		SocketPath:       socketPath,
		DialTimeout:      750 * time.Millisecond,
		OperationTimeout: 2 * time.Second,
	}).Recover(context.Background())
	if err != nil {
		t.Fatalf("recover should wait for operation timeout, got error: %v", err)
	}
	if response.Mode != "execute" || len(response.Results) != 1 || response.Results[0].Status != "recovered" {
		t.Fatalf("unexpected recovery response: %#v", response)
	}
}
