package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
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

func TestXrayManagerDisconnectActiveTunRollsBackHostBeforeStoppingXray(t *testing.T) {
	runtimeDir := t.TempDir()
	orderPath := filepath.Join(runtimeDir, "disconnect-order.log")
	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create generated dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}

	plan := transactionPlanForTest()
	plan.TunDevice.Action = "verify"
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: fixedClock()}
	tx := txstate.NewTransaction("tun-active-disconnect", "test-profile", planner.ModeTun, store.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	tx.Rollback = rollbackMetadataFromTunPlan(plan)
	tx.Rollback.GeneratedConfigs = append(tx.Rollback.GeneratedConfigs, txstate.GeneratedConfigRollback{Path: configPath, Owner: txstate.TransactionOwner})
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save active TUN transaction: %v", err)
	}

	fakeXray := writeFakeXray(t, `#!/bin/sh
trap 'printf "%s\n" stop-xray >> "$ORDER_FILE"; exit 0' TERM
while true; do sleep 1; done
`)
	cmd := exec.Command(fakeXray)
	cmd.Env = append(os.Environ(), "ORDER_FILE="+orderPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake Xray: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	})

	manager := &XrayManager{
		RuntimeDir:  runtimeDir,
		StopTimeout: time.Second,
		tunExecutor: &activeTunDisconnectOrderExecutor{orderPath: orderPath},
	}
	manager.mu.Lock()
	manager.cmd = cmd
	manager.done = done
	manager.state = xrayState{
		Connection:        "active",
		Mode:              planner.ModeTun,
		ProfileID:         "test-profile",
		ProfileName:       "test profile",
		RuntimeConfigPath: configPath,
		TransactionID:     tx.ID,
	}
	manager.mu.Unlock()

	disconnected, err := manager.Disconnect(context.Background())
	if err != nil {
		t.Fatalf("disconnect active TUN: %v", err)
	}
	if disconnected.Connection != "inactive" {
		t.Fatalf("expected inactive disconnect response, got %#v", disconnected)
	}
	orderData, err := os.ReadFile(orderPath)
	if err != nil {
		t.Fatalf("read disconnect order: %v", err)
	}
	if got, want := strings.Fields(string(orderData)), []string{"rollback-host", "stop-xray"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("wrong active TUN disconnect order: got %v want %v", got, want)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected generated config cleanup, got stat err %v", err)
	}
	if _, err := store.Load(tx.ID); err == nil {
		t.Fatal("expected rolled-back transaction file to be removed after active TUN disconnect")
	}
}

func TestXrayManagerConnectWritesVLESSXHTTPRuntimeConfigWithoutSystemNetworking(t *testing.T) {
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
	req := connectRequestForTest()
	req.Profile.ID = "test-vless-xhttp"
	req.Profile.Name = "test vless xhttp"
	req.Profile.Transport = "xhttp"
	req.Profile.Security = "reality"
	req.Profile.ServerName = "xhttp.edge.invalid"
	req.Profile.Path = "/xhttp"
	req.Profile.HostHeader = "xhttp.edge.invalid"
	req.Profile.RealityPublicKey = "test-public-key"
	req.Profile.RealityShortID = "abcd"

	connected, err := manager.Connect(context.Background(), req)
	if err != nil {
		t.Fatalf("connect xhttp failed: %v", err)
	}
	if connected.Connection != "active" || connected.Mode != "proxy-only" {
		t.Fatalf("unexpected xhttp connect response: %#v", connected)
	}
	if connected.Routes != "not modified" || connected.DNS != "not modified" || connected.Firewall != "not modified" {
		t.Fatalf("expected proxy-only xhttp connect not to mutate system networking, got %#v", connected)
	}

	configPath := filepath.Join(runtimeDir, "generated", "xray.json")
	generated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated xhttp config: %v", err)
	}
	config := string(generated)
	for _, want := range []string{`"network": "xhttp"`, `"xhttpSettings"`, `"path": "/xhttp"`, `"host": "xhttp.edge.invalid"`, `"realitySettings"`} {
		if !strings.Contains(config, want) {
			t.Fatalf("expected generated xhttp config to contain %s, got %s", want, config)
		}
	}

	if _, err := manager.Disconnect(context.Background()); err != nil {
		t.Fatalf("disconnect xhttp failed: %v", err)
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

type activeTunDisconnectOrderExecutor struct {
	orderPath string
}

func (e *activeTunDisconnectOrderExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (e *activeTunDisconnectOrderExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *activeTunDisconnectOrderExecutor) Rollback(context.Context, planner.TunPlan) error {
	return appendTestOrder(e.orderPath, "rollback-host")
}

func appendTestOrder(path, value string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(value + "\n")
	return err
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
