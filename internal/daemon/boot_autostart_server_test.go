package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/client"
)

func TestServerExposesBootAutostartPolicyFromPrivateStateDirectory(t *testing.T) {
	runtimeDir := t.TempDir()
	stateDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (Server{
			RuntimeDir: runtimeDir,
			StateDir:   stateDir,
			bootID:     fixedBootID(testBootConfigured),
		}).Run(ctx)
	}()

	client := client.AutostartClient{SocketPath: filepath.Join(runtimeDir, "podlazd.sock"), Timeout: time.Second}
	var initialErr error
	for i := 0; i < 50; i++ {
		status, err := client.Status(context.Background())
		if err == nil {
			if status.Enabled {
				t.Fatal("autostart unexpectedly enabled before configuration")
			}
			initialErr = nil
			break
		}
		initialErr = err
		time.Sleep(10 * time.Millisecond)
	}
	if initialErr != nil {
		cancel()
		<-done
		t.Fatalf("autostart status did not become available: %v", initialErr)
	}

	configured, err := client.Enable(context.Background(), testBootAutostartConfig())
	if err != nil {
		cancel()
		<-done
		t.Fatalf("enable autostart: %v", err)
	}
	if !configured.Enabled || configured.ProfileName != "Example VPN" {
		t.Fatalf("configured status = %+v", configured)
	}
	info, err := os.Stat(filepath.Join(stateDir, bootAutostartManifestFileName))
	if err != nil {
		cancel()
		<-done
		t.Fatalf("persistent manifest missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("persistent manifest mode = %o, want 600", info.Mode().Perm())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}
}
