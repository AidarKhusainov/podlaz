package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

type daemonRecoveryFakeRunner struct {
	paths    map[string]string
	commands map[string][]daemonRecoveryFakeCommand
	calls    []string
}

type daemonRecoveryFakeCommand struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func (r *daemonRecoveryFakeRunner) LookPath(file string) (string, error) {
	path, ok := r.paths[file]
	if !ok {
		return "", errors.New("command not found")
	}
	return path, nil
}

func (r *daemonRecoveryFakeRunner) Run(_ context.Context, name string, args ...string) (recovery.CommandResult, error) {
	key := filepath.Base(name) + " " + strings.Join(args, " ")
	r.calls = append(r.calls, key)
	commands := r.commands[key]
	if len(commands) == 0 {
		return recovery.CommandResult{ExitCode: -1}, errors.New("unexpected command: " + key)
	}
	command := commands[0]
	r.commands[key] = commands[1:]
	return recovery.CommandResult{Stdout: command.stdout, Stderr: command.stderr, ExitCode: command.exitCode}, command.err
}

func TestScanStaleResolvedLinkDetectsMissingInterfaceCandidate(t *testing.T) {
	runner := &daemonRecoveryFakeRunner{
		paths: map[string]string{
			"ip":         "/usr/sbin/ip",
			"resolvectl": "/usr/bin/resolvectl",
		},
		commands: map[string][]daemonRecoveryFakeCommand{
			"resolvectl status podlaz0 --no-pager": {{stdout: `Link 7 (podlaz0)
    Current Scopes: none
       DNS Servers: 1.1.1.1`}},
			"ip link show dev podlaz0": {{stderr: `Device "podlaz0" does not exist.`, exitCode: 1, err: errors.New("exit status 1")}},
		},
	}
	var result recovery.ScanResult

	scanStaleResolvedLink(context.Background(), runner, &result)

	if len(result.Candidates) != 1 {
		t.Fatalf("expected one recovery candidate, got %#v", result.Candidates)
	}
	candidate := result.Candidates[0]
	if candidate.Kind != recoveryResolvedCandidateKind || candidate.Target != recoveryResolvedInterface {
		t.Fatalf("unexpected candidate: %#v", candidate)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", result.Warnings)
	}
}

func TestCleanupStaleResolvedLinkRevertsMissingInterfaceRecord(t *testing.T) {
	runner := &daemonRecoveryFakeRunner{
		paths: map[string]string{
			"ip":         "/usr/sbin/ip",
			"resolvectl": "/usr/bin/resolvectl",
		},
		commands: map[string][]daemonRecoveryFakeCommand{
			"ip link show dev podlaz0": {{stderr: `Device "podlaz0" does not exist.`, exitCode: 1, err: errors.New("exit status 1")}},
			"resolvectl revert podlaz0": {{ }},
			"resolvectl status podlaz0 --no-pager": {{stderr: "Link podlaz0 does not exist.", exitCode: 1, err: errors.New("exit status 1")}},
		},
	}
	candidate := recovery.Candidate{Kind: recoveryResolvedCandidateKind, Description: "systemd-resolved link state", Target: recoveryResolvedInterface}

	result := cleanupStaleResolvedLink(context.Background(), runner, candidate)

	if result.Status != "recovered" {
		t.Fatalf("expected recovered result, got %#v", result)
	}
	wantCalls := []string{
		"ip link show dev podlaz0",
		"resolvectl revert podlaz0",
		"resolvectl status podlaz0 --no-pager",
	}
	if strings.Join(runner.calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("unexpected command order:\nwant %#v\n got %#v", wantCalls, runner.calls)
	}
}

func TestCleanupStaleResolvedLinkDoesNotClaimRecoveredWhenRecordPersists(t *testing.T) {
	runner := &daemonRecoveryFakeRunner{
		paths: map[string]string{
			"ip":         "/usr/sbin/ip",
			"resolvectl": "/usr/bin/resolvectl",
		},
		commands: map[string][]daemonRecoveryFakeCommand{
			"ip link show dev podlaz0": {{stderr: `Device "podlaz0" does not exist.`, exitCode: 1, err: errors.New("exit status 1")}},
			"resolvectl revert podlaz0": {{ }},
			"resolvectl status podlaz0 --no-pager": {{stdout: `Link 7 (podlaz0)
    Current Scopes: none`}},
		},
	}
	candidate := recovery.Candidate{Kind: recoveryResolvedCandidateKind, Description: "systemd-resolved link state", Target: recoveryResolvedInterface}

	result := cleanupStaleResolvedLink(context.Background(), runner, candidate)

	if result.Status != "skipped" || !strings.Contains(result.Message, "restart systemd-resolved manually") {
		t.Fatalf("expected skipped manual restart instruction, got %#v", result)
	}
}
