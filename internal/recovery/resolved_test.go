package recovery

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type resolvedRecoveryRunner struct {
	paths    map[string]string
	commands map[string][]resolvedRecoveryCommand
	calls    []string
}

type resolvedRecoveryCommand struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func (r *resolvedRecoveryRunner) LookPath(file string) (string, error) {
	path, ok := r.paths[file]
	if !ok {
		return "", errors.New("command not found")
	}
	return path, nil
}

func (r *resolvedRecoveryRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	key := filepath.Base(name) + " " + strings.Join(args, " ")
	r.calls = append(r.calls, key)
	commands := r.commands[key]
	if len(commands) == 0 {
		return CommandResult{ExitCode: -1}, errors.New("unexpected command: " + key)
	}
	command := commands[0]
	r.commands[key] = commands[1:]
	return CommandResult{Stdout: command.stdout, Stderr: command.stderr, ExitCode: command.exitCode}, command.err
}

func TestPlanWithOptionsReportsStaleResolvedLinkCandidate(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink(), missingPodlazLink()},
		[]resolvedRecoveryCommand{resolvedLinkExists()},
	)

	plan := PlanWithOptions(context.Background(), Options{Runner: runner, RuntimeDir: filepath.Join(t.TempDir(), "podlaz")})

	assertCandidate(t, plan, managedDNSCandidateKind, managedInterface)
	got := plan.String()
	if !strings.Contains(got, "Would recover systemd-resolved link state: podlaz0") {
		t.Fatalf("expected dry-run to render stale resolved candidate, got %q", got)
	}
}

func TestExecuteWithOptionsRecoversStaleResolvedLinkCandidate(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink(), missingPodlazLink(), missingPodlazLink()},
		[]resolvedRecoveryCommand{resolvedLinkExists(), missingResolvedLink()},
	)
	runner.commands["resolvectl revert podlaz0"] = []resolvedRecoveryCommand{{}}
	runtimeDir := filepath.Join(t.TempDir(), "podlaz")

	result := ExecuteWithOptions(context.Background(), Options{
		Runner:     runner,
		RuntimeDir: runtimeDir,
		Executor:   DaemonCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir},
	})

	if len(result.Results) != 1 {
		t.Fatalf("expected one cleanup result, got %#v", result.Results)
	}
	cleanup := result.Results[0]
	if cleanup.Candidate.Kind != managedDNSCandidateKind || cleanup.Candidate.Target != managedInterface || cleanup.Status != "recovered" {
		t.Fatalf("expected recovered resolved candidate, got %#v", cleanup)
	}
	got := result.String()
	if !strings.Contains(got, "Recovered systemd-resolved link state: podlaz0") {
		t.Fatalf("expected execute output to render recovered resolved candidate, got %q", got)
	}
}

func TestExecuteWithOptionsReportsPersistentResolvedRecordAfterRevert(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink(), missingPodlazLink(), missingPodlazLink()},
		[]resolvedRecoveryCommand{resolvedLinkExists(), resolvedLinkExists()},
	)
	runner.commands["resolvectl revert podlaz0"] = []resolvedRecoveryCommand{{}}
	runtimeDir := filepath.Join(t.TempDir(), "podlaz")

	result := ExecuteWithOptions(context.Background(), Options{
		Runner:     runner,
		RuntimeDir: runtimeDir,
		Executor:   DaemonCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir},
	})

	if len(result.Results) != 1 {
		t.Fatalf("expected one cleanup result, got %#v", result.Results)
	}
	cleanup := result.Results[0]
	if cleanup.Candidate.Kind != managedDNSCandidateKind || cleanup.Status != "skipped" || !strings.Contains(cleanup.Message, "restart systemd-resolved manually") {
		t.Fatalf("expected skipped manual restart guidance, got %#v", cleanup)
	}
}

func newResolvedRecoveryRunner(ipLinkResults, resolvedStatusResults []resolvedRecoveryCommand) *resolvedRecoveryRunner {
	return &resolvedRecoveryRunner{
		paths: map[string]string{
			"ip":         "/usr/sbin/ip",
			"nft":        "/usr/sbin/nft",
			"resolvectl": "/usr/bin/resolvectl",
		},
		commands: map[string][]resolvedRecoveryCommand{
			"ip link show dev podlaz0":             ipLinkResults,
			"nft list table inet podlaz":           []resolvedRecoveryCommand{missingNFTTable()},
			"resolvectl status podlaz0 --no-pager": resolvedStatusResults,
		},
	}
}

func resolvedLinkExists() resolvedRecoveryCommand {
	return resolvedRecoveryCommand{stdout: `Link 7 (podlaz0)
    Current Scopes: none
       DNS Servers: 1.1.1.1`}
}

func missingResolvedLink() resolvedRecoveryCommand {
	return resolvedRecoveryCommand{stderr: "Link podlaz0 does not exist.", exitCode: 1, err: errors.New("exit status 1")}
}

func missingPodlazLink() resolvedRecoveryCommand {
	return resolvedRecoveryCommand{stderr: `Device "podlaz0" does not exist.`, exitCode: 1, err: errors.New("exit status 1")}
}

func missingNFTTable() resolvedRecoveryCommand {
	return resolvedRecoveryCommand{stderr: "Error: No such file or directory", exitCode: 1, err: errors.New("exit status 1")}
}
