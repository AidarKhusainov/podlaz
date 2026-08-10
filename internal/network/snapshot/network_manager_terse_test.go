package snapshot

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestParseNMActiveConnectionsHonorsTerseEscaping(t *testing.T) {
	got := parseNMActiveConnections(`Office\:Floor\\One:11111111-2222-3333-4444-555555555555:802-11-wireless:wlan0:activated`)
	want := []NetworkManagerConnection{{
		Name:   `Office:Floor\One`,
		UUID:   "11111111-2222-3333-4444-555555555555",
		Type:   "802-11-wireless",
		Device: "wlan0",
		State:  "activated",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("escaped nmcli terse row parsed as %#v, want %#v", got, want)
	}
}

type localeRecordingNMRunner struct {
	commands [][]string
}

func (r *localeRecordingNMRunner) LookPath(file string) (string, error) {
	if file == "nmcli" {
		return "/usr/bin/nmcli", nil
	}
	return "", errors.New("command not found")
}

func (r *localeRecordingNMRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	r.commands = append(r.commands, append([]string{name}, args...))
	if args[len(args)-1] == "general" {
		return CommandResult{Stdout: "running:connected", ExitCode: 0}, nil
	}
	if args[len(args)-1] == "--active" {
		return CommandResult{Stdout: "Example:11111111-2222-3333-4444-555555555555:802-11-wireless:wlan0:activated", ExitCode: 0}, nil
	}
	return CommandResult{ExitCode: 1}, errors.New("unexpected command")
}

func TestNetworkManagerRunsNMCLIWithCLocale(t *testing.T) {
	runner := &localeRecordingNMRunner{}
	nm := networkManager(context.Background(), runner)
	if nm.ActiveConnectionsInspection.Status != StatusDetected {
		t.Fatalf("active inspection=%q, want detected", nm.ActiveConnectionsInspection.Status)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("nmcli command count=%d, want 2: %#v", len(runner.commands), runner.commands)
	}
	for _, command := range runner.commands {
		if len(command) < 4 || command[0] != "env" || command[1] != "LC_ALL=C" || command[2] != "/usr/bin/nmcli" {
			t.Fatalf("NetworkManager command is locale-dependent: %#v", command)
		}
	}
}
