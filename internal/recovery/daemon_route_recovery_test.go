package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestDaemonCleanupExecutorRemovesOwnedMainTableServerBypassRoute(t *testing.T) {
	runtimeDir := t.TempDir()
	runner := &recordingRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]fakeCommand{
			"ip -4 route del 203.0.113.10/32 via 147.90.14.1 dev ens1 table main": {},
		},
	}
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		Routes: []txstate.RouteRollback{{
			Owner: netexecutor.OwnerRoute,
			Table: "main",
			CIDR:  "203.0.113.10/32",
			Via:   "147.90.14.1",
			Dev:   "ens1",
		}},
	})

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "route", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	assertCommands(t, runner, []string{"ip -4 route del 203.0.113.10/32 via 147.90.14.1 dev ens1 table main"})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("transaction file must be removed after complete cleanup, stat err=%v", err)
	}
}

func TestDaemonCleanupExecutorRejectsMainTableNonHostRoute(t *testing.T) {
	assertMainTableRouteSkipped(t, txstate.RouteRollback{
		Owner: netexecutor.OwnerRoute,
		Table: "main",
		CIDR:  "203.0.113.0/24",
		Via:   "147.90.14.1",
		Dev:   "ens1",
	})
}

func TestDaemonCleanupExecutorRejectsMainTableRouteWithoutVia(t *testing.T) {
	assertMainTableRouteSkipped(t, txstate.RouteRollback{
		Owner: netexecutor.OwnerRoute,
		Table: "main",
		CIDR:  "203.0.113.10/32",
		Dev:   "ens1",
	})
}

func TestDaemonCleanupExecutorRejectsMainTableRouteWithoutDev(t *testing.T) {
	assertMainTableRouteSkipped(t, txstate.RouteRollback{
		Owner: netexecutor.OwnerRoute,
		Table: "main",
		CIDR:  "203.0.113.10/32",
		Via:   "147.90.14.1",
	})
}

func TestDaemonCleanupExecutorRejectsNonPodlazRouteOwner(t *testing.T) {
	assertMainTableRouteSkipped(t, txstate.RouteRollback{
		Owner: "other",
		Table: "main",
		CIDR:  "203.0.113.10/32",
		Via:   "147.90.14.1",
		Dev:   "ens1",
	})
}

func TestDaemonCleanupExecutorRollsBackManagedTableRoutes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		table   string
		command string
	}{
		{
			name:    "named table",
			table:   "podlaz",
			command: "ip -4 route del 0.0.0.0/0 dev podlaz0 table 51820",
		},
		{
			name:    "numeric table",
			table:   "51820",
			command: "ip -4 route del 0.0.0.0/0 dev podlaz0 table 51820",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			runner := &recordingRunner{
				paths: map[string]string{"ip": "/usr/sbin/ip"},
				commands: map[string]fakeCommand{
					tc.command: {},
				},
			}
			path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
				Routes: []txstate.RouteRollback{{
					Owner: netexecutor.OwnerRoute,
					Table: tc.table,
					CIDR:  "0.0.0.0/0",
					Dev:   "podlaz0",
				}},
			})

			results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))

			assertCleanupResult(t, results, "route", "recovered", "")
			assertCleanupResult(t, results, "transaction-state", "recovered", "")
			assertCommands(t, runner, []string{tc.command})
		})
	}
}

func TestRecoveryDoesNotUseIPRouteReplace(t *testing.T) {
	for _, path := range []string{"daemon_executor.go", "recovery.go"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		if strings.Contains(text, "route replace") || strings.Contains(text, `"route", "replace"`) {
			t.Fatalf("recovery must not use ip route replace in %s", path)
		}
	}
}

func assertMainTableRouteSkipped(t *testing.T, route txstate.RouteRollback) {
	t.Helper()
	runtimeDir := t.TempDir()
	runner := &recordingRunner{paths: map[string]string{"ip": "/usr/sbin/ip"}}
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{Routes: []txstate.RouteRollback{route}})

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "route", "skipped", "")
	assertCleanupResult(t, results, "transaction-state", "skipped", "transaction state was preserved")
	assertCommands(t, runner, nil)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transaction file must remain after skipped cleanup: %v", err)
	}
}

type recordingRunner struct {
	paths       map[string]string
	commands    map[string]fakeCommand
	runCommands []string
}

func (r *recordingRunner) LookPath(file string) (string, error) {
	path, ok := r.paths[file]
	if !ok {
		return "", errors.New("command not found")
	}
	return path, nil
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	key := filepath.Base(name) + " " + strings.Join(args, " ")
	r.runCommands = append(r.runCommands, key)
	command, ok := r.commands[key]
	if !ok {
		return CommandResult{ExitCode: -1}, errors.New("unexpected command: " + key)
	}
	return CommandResult{
		Stdout:   command.stdout,
		Stderr:   command.stderr,
		ExitCode: command.exitCode,
	}, command.err
}

func assertCommands(t *testing.T, runner *recordingRunner, want []string) {
	t.Helper()
	if len(runner.runCommands) != len(want) {
		t.Fatalf("unexpected commands: got %#v want %#v", runner.runCommands, want)
	}
	for i := range want {
		if runner.runCommands[i] != want[i] {
			t.Fatalf("unexpected command[%d]: got %q want %q; all commands %#v", i, runner.runCommands[i], want[i], runner.runCommands)
		}
	}
}
