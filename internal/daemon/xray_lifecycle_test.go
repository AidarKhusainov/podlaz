package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestXrayManagerConnectStatusAndDisconnect(t *testing.T) {
	runtimeDir := t.TempDir()
	fakeXray := writeFakeXray(t, `#!/bin/sh
config=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-config" ]; then
    shift
    config="$1"
  fi
  shift
done
if [ ! -s "$config" ]; then
  exit 65
fi
trap 'exit 0' TERM
while true; do sleep 1; done
`)
	manager := &XrayManager{RuntimeDir: runtimeDir, XrayPath: fakeXray, StopTimeout: time.Second}

	connected, err := manager.Connect(context.Background(), connectRequestForTest())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if connected.Connection != "active" || connected.Mode != "proxy-only" {
		t.Fatalf("unexpected connect response: %#v", connected)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "generated", "xray.json")); err != nil {
		t.Fatalf("expected generated Xray config: %v", err)
	}

	status := manager.Status(context.Background())
	if status.Connection != "active" {
		t.Fatalf("expected active status, got %#v", status)
	}
	if !strings.Contains(status.Proxy, "127.0.0.1:1080") {
		t.Fatalf("expected SOCKS listener in status, got %q", status.Proxy)
	}
	if status.Routes != "not modified" || status.DNS != "not modified" || status.Firewall != "not modified" {
		t.Fatalf("expected no system networking mutation status, got %#v", status)
	}

	disconnected, err := manager.Disconnect(context.Background())
	if err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}
	if disconnected.Connection != "inactive" || disconnected.Proxy != "inactive" {
		t.Fatalf("unexpected disconnect response: %#v", disconnected)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "generated", "xray.json")); !os.IsNotExist(err) {
		t.Fatalf("expected generated Xray config cleanup, got stat err %v", err)
	}

	disconnectedAgain, err := manager.Disconnect(context.Background())
	if err != nil {
		t.Fatalf("second disconnect failed: %v", err)
	}
	if disconnectedAgain.Connection != "inactive" {
		t.Fatalf("expected idempotent inactive disconnect, got %#v", disconnectedAgain)
	}
}

func TestXrayManagerReportsCoreCrashInStatus(t *testing.T) {
	runtimeDir := t.TempDir()
	fakeXray := writeFakeXray(t, "#!/bin/sh\nexit 23\n")
	manager := &XrayManager{RuntimeDir: runtimeDir, XrayPath: fakeXray, StopTimeout: time.Second}

	if _, err := manager.Connect(context.Background(), connectRequestForTest()); err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	var status api.StatusResponse
	for i := 0; i < 50; i++ {
		status = manager.Status(context.Background())
		if status.Connection == "error (core exited)" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.Connection != "error (core exited)" {
		t.Fatalf("expected crashed status, got %#v", status)
	}
	if len(status.Warnings) == 0 || !strings.Contains(status.Warnings[0], "Xray process exited unexpectedly") {
		t.Fatalf("expected crash warning, got %#v", status.Warnings)
	}
}

func writeFakeXray(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "xray")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func connectRequestForTest() api.ConnectRequest {
	return api.ConnectRequest{
		Mode: "proxy-only",
		Profile: api.ProfileSnapshot{
			ID:           "test-vless",
			Name:         "test vless",
			Source:       "imported_uri",
			Engine:       "xray",
			Server:       "example.com",
			Port:         443,
			Protocol:     "vless",
			UserIdentity: "11111111-1111-1111-1111-111111111111",
			Transport:    "tcp",
			Security:     "tls",
			Encryption:   "none",
			ServerName:   "example.com",
		},
	}
}
