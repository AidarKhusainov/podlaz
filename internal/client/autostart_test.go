package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestAutostartClientConfigureStatusAndDisable(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	config := api.AutostartConfigureRequest{
		Mode: "tun",
		Profile: api.ProfileSnapshot{
			ID:       "example-profile",
			Name:     "Example VPN",
			Server:   "vpn.example.com",
			Port:     443,
			Protocol: "vless",
		},
	}
	enabled := false
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == api.AutostartConfigurePath && r.Method == http.MethodPost:
			var got api.AutostartConfigureRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode configure request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			encoded, _ := json.Marshal(got)
			if strings.Contains(string(encoded), "handoff") {
				t.Errorf("configure request unexpectedly contains handoff: %s", encoded)
			}
			if got != config {
				t.Errorf("configure request = %+v, want %+v", got, config)
			}
			enabled = true
			_ = json.NewEncoder(w).Encode(api.AutostartStatusResponse{Enabled: true, Mode: config.Mode, ProfileName: config.Profile.Name})
		case r.URL.Path == api.AutostartStatusPath && r.Method == http.MethodGet:
			status := api.AutostartStatusResponse{}
			if enabled {
				status = api.AutostartStatusResponse{Enabled: true, Mode: config.Mode, ProfileName: config.Profile.Name}
			}
			_ = json.NewEncoder(w).Encode(status)
		case r.URL.Path == api.AutostartConfigurePath && r.Method == http.MethodDelete:
			enabled = false
			_ = json.NewEncoder(w).Encode(api.AutostartStatusResponse{})
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	})}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		if err := <-serverErr; err != nil && err != http.ErrServerClosed {
			t.Errorf("serve unix socket: %v", err)
		}
	})

	client := AutostartClient{SocketPath: socketPath, Timeout: 2 * time.Second}
	status, err := client.Enable(context.Background(), config)
	if err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if !status.Enabled || status.ProfileName != "Example VPN" {
		t.Fatalf("Enable() status = %+v", status)
	}
	status, err = client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Enabled {
		t.Fatalf("Status() = %+v, want enabled", status)
	}
	status, err = client.Disable(context.Background())
	if err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if status.Enabled {
		t.Fatalf("Disable() status = %+v", status)
	}
}

func TestAutostartClientRejectsInvalidDaemonStatus(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AutostartStatusResponse{Enabled: true})
	})}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-serverErr
	})

	_, err = (AutostartClient{SocketPath: socketPath, Timeout: time.Second}).Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("Status() error = %v, want invalid response error", err)
	}
}
