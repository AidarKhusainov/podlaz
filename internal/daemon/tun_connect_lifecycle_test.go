package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestConnectTunPreflightsBeforeActivePodlazReplace(t *testing.T) {
	withCoreIdentityTestHooks(t, 1000, nil, nil)

	preflightErr := errors.New("native TUN support unavailable")
	oldPreflight := preflightNativeTunSupport
	oldDeps := validateTunRuntimeDependenciesHook
	preflightCalled := false
	preflightNativeTunSupport = func(context.Context, string, coreExecutionIdentity) error {
		preflightCalled = true
		return preflightErr
	}
	validateTunRuntimeDependenciesHook = func() error { return nil }
	t.Cleanup(func() {
		preflightNativeTunSupport = oldPreflight
		validateTunRuntimeDependenciesHook = oldDeps
	})

	fakeXray := writeFakeXray(t, `#!/bin/sh
trap 'exit 0' TERM
while true; do
  sleep 3600 &
  wait $!
done
`)
	cmd := exec.Command(fakeXray)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start active fake Xray: %v", err)
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

	manager := &XrayManager{RuntimeDir: t.TempDir(), XrayPath: fakeXray, StopTimeout: time.Second}
	manager.mu.Lock()
	manager.cmd = cmd
	manager.done = done
	manager.state = xrayState{Connection: "active", Mode: planner.ModeTun, ProfileID: "active-profile", ProfileName: "active profile"}
	manager.mu.Unlock()

	req := connectRequestForTest()
	req.Mode = planner.ModeTun
	req.Handoff = api.HandoffReplacePodlaz

	_, err := manager.Connect(context.Background(), req)
	if !errors.Is(err, preflightErr) {
		t.Fatalf("expected native TUN preflight error before active replace, got %v", err)
	}
	if !preflightCalled {
		t.Fatal("expected native TUN preflight to run")
	}

	manager.mu.Lock()
	stillActive := manager.cmd == cmd && manager.state.Connection == "active"
	manager.mu.Unlock()
	if !stillActive {
		t.Fatal("unsupported native TUN preflight must not disconnect the active podlaz TUN before failing")
	}
	select {
	case <-done:
		t.Fatal("active Xray process was stopped before native TUN support preflight succeeded")
	default:
	}
	if _, err := os.Stat(manager.runtimeDir()); err != nil {
		t.Fatalf("runtime dir should remain inspectable: %v", err)
	}
}
