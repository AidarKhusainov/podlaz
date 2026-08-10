package snapshot

import (
	"context"
	"errors"
	"testing"
)

type networkManagerInspectionRunner struct{}

func (networkManagerInspectionRunner) LookPath(file string) (string, error) {
	if file == "nmcli" {
		return "/usr/bin/nmcli", nil
	}
	return "", errors.New("command not found")
}

func (networkManagerInspectionRunner) Run(_ context.Context, _ string, args ...string) (CommandResult, error) {
	if len(args) >= 4 && args[len(args)-1] == "general" {
		return CommandResult{Stdout: "running:connected", ExitCode: 0}, nil
	}
	if len(args) >= 3 && args[len(args)-1] == "--active" {
		err := errors.New("active connection inspection failed")
		return CommandResult{Stderr: "temporary D-Bus failure", ExitCode: 1}, err
	}
	return CommandResult{ExitCode: 1}, errors.New("unexpected command")
}

func TestNetworkManagerSeparatesActiveConnectionInspectionFailureFromDaemonDetection(t *testing.T) {
	nm := networkManager(context.Background(), networkManagerInspectionRunner{})
	if nm.Finding.Status != StatusDetected {
		t.Fatalf("NetworkManager finding=%q, want detected", nm.Finding.Status)
	}
	if nm.ActiveConnectionsInspection.Status != StatusUnknown {
		t.Fatalf("active connection inspection=%q, want unknown", nm.ActiveConnectionsInspection.Status)
	}
	if nm.ActiveConnectionsInspection.Detail == "" {
		t.Fatal("active connection inspection failure lost command evidence")
	}
	if len(nm.ActiveConnections) != 0 {
		t.Fatalf("failed active connection inspection published connections: %#v", nm.ActiveConnections)
	}
}
