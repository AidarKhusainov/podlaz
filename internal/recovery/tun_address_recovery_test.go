package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestDaemonRecoveryRemovesOnlyExactOwnedTunAddress(t *testing.T) {
	runtimeDir := t.TempDir()
	missingPID := 1 << 30
	runner := &tunAddressRecoveryRunner{addressPresent: true}
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		TUNAddresses: []txstate.TUNAddressRollback{ownedTunAddressRollback(7)},
		ChildProcesses: []txstate.ChildProcessRollback{{
			Label: "xray", PID: missingPID, Owner: txstate.TransactionOwner,
		}},
	})

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "tun-address", "recovered", "")
	assertCleanupResult(t, results, "child-process", "recovered", "already absent")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	assertTunAddressRecoveryCommands(t, runner)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("successful exact-address recovery must remove transaction state: %v", err)
	}
}

func TestDaemonRecoveryRefusesTunAddressOnReplacementLink(t *testing.T) {
	runtimeDir := t.TempDir()
	runner := &recordingRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]fakeCommand{
			"ip -details -o link show dev podlaz0": {stdout: recoveryTunLink(8)},
		},
	}
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		TUNAddresses: []txstate.TUNAddressRollback{ownedTunAddressRollback(7)},
	})

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "tun-link-identity", "failed", "does not match transaction-bound identity")
	assertCleanupResult(t, results, "tun-address", "failed", "link identity was not proven")
	assertCleanupResult(t, results, "transaction-state", "failed", "preserved")
	assertCommands(t, runner, []string{"ip -details -o link show dev podlaz0"})
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement-link refusal must preserve transaction state: %v", err)
	}
}

func TestDaemonRecoveryAcceptsMissingTunAddressOnlyAfterTrackedChildAbsence(t *testing.T) {
	missingErr := errors.New("exit status 1")
	tests := []struct {
		name      string
		processes []txstate.ChildProcessRollback
		want      string
	}{
		{
			name: "tracked child absent",
			processes: []txstate.ChildProcessRollback{{
				Label: "xray", PID: 1 << 30, Owner: txstate.TransactionOwner,
			}},
			want: "recovered",
		},
		{name: "child identity unavailable", want: "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			runner := &recordingRunner{
				paths: map[string]string{"ip": "/usr/sbin/ip"},
				commands: map[string]fakeCommand{
					"ip -details -o link show dev podlaz0": {
						stderr: `Device "podlaz0" does not exist.`, exitCode: 1, err: missingErr,
					},
				},
			}
			path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
				TUNAddresses:   []txstate.TUNAddressRollback{ownedTunAddressRollback(7)},
				ChildProcesses: tt.processes,
			})

			results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))
			assertCleanupResult(t, results, "tun-address", tt.want, "")
			if tt.want == "recovered" {
				assertCleanupResult(t, results, "transaction-state", "recovered", "")
			} else {
				assertCleanupResult(t, results, "transaction-state", "failed", "preserved")
			}
		})
	}
}

func ownedTunAddressRollback(index int) txstate.TUNAddressRollback {
	return txstate.TUNAddressRollback{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		LinkIndex:         index,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             "podlaz:tun-address",
	}
}

func recoveryTunLink(index int) string {
	return fmt.Sprintf(
		"%d: podlaz0: <POINTOPOINT,NOARP,UP,LOWER_UP> "+
			"mtu 1500 qdisc fq_codel state UP mode DEFAULT "+
			"group default qlen 500 link/none "+
			"tun type tun pi off vnet_hdr on persist off",
		index,
	)
}

func recoveryTunAddress(index int, cidr string) string {
	return fmt.Sprintf("%d: podlaz0    inet %s scope global podlaz0\\       valid_lft forever preferred_lft forever", index, cidr)
}

func TestOwnedTunAddressRollbackFixtureUsesDocumentationSafePolicy(t *testing.T) {
	got := ownedTunAddressRollback(7)
	if got.CIDR != planner.DefaultTunIPv4CIDR || strings.Contains(got.CIDR, "192.168.") {
		t.Fatalf("unexpected recovery fixture: %#v", got)
	}
}

func TestDaemonRecoveryClosesAddressCrashWindowFromBoundApplyingIntent(t *testing.T) {
	runtimeDir := t.TempDir()
	runner := &tunAddressRecoveryRunner{addressPresent: true}
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-address-syscall-crash", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.DesiredPlan.TUN.Owner = "xray:tun-inbound"
	tx.DesiredPlan.TUN.InterfaceName = managedInterface
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family: "ipv4", InterfaceName: managedInterface, CIDR: planner.DefaultTunIPv4CIDR, Scope: "global",
		LinkIndex: 7, LinkKind: "tun", AppearedAfterCore: true, Owner: "podlaz:tun-address",
	}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save crash-window transaction: %v", err)
	}

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))
	assertCleanupResult(t, results, "tun-address", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	assertTunAddressRecoveryCommands(t, runner)
}

func TestApplyingAddressCrashWindowCleansAddressButPreservesAmbiguousRouteIntent(t *testing.T) {
	runtimeDir := t.TempDir()
	runner := &tunAddressRecoveryRunner{addressPresent: true}
	tx := txstate.NewTransaction("tx-address-route-crash", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family: "ipv4", InterfaceName: managedInterface, CIDR: planner.DefaultTunIPv4CIDR, Scope: "global",
		LinkIndex: 7, LinkKind: "tun", AppearedAfterCore: true, Owner: "podlaz:tun-address",
	}
	tx.DesiredPlan.Routes = []txstate.RoutePlan{{
		Table: "podlaz", CIDR: "default", Dev: managedInterface, Owner: "podlaz:route", Operation: "add",
	}}
	path, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx)
	if err != nil {
		t.Fatal(err)
	}

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))
	assertCleanupResult(t, results, "tun-address", "recovered", "")
	assertCleanupResult(t, results, "transaction-ownership", "skipped", "routes")
	assertCleanupResult(t, results, "transaction-state", "skipped", "preserved")
	assertTunAddressRecoveryCommands(t, runner)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ambiguous applying transaction must remain after exact address cleanup: %v", err)
	}
}

type tunAddressRecoveryRunner struct {
	addressPresent bool
	commands       []string
}

func (r *tunAddressRecoveryRunner) LookPath(file string) (string, error) {
	if file == "ip" {
		return "/usr/sbin/ip", nil
	}
	return "", errors.New("command not found")
}

func (r *tunAddressRecoveryRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := filepath.Base(name) + " " + strings.Join(args, " ")
	r.commands = append(r.commands, command)

	switch command {
	case "ip -details -o link show dev podlaz0":
		return CommandResult{Stdout: recoveryTunLink(7)}, nil
	case "ip -4 -o address show dev podlaz0":
		if !r.addressPresent {
			return CommandResult{}, nil
		}
		return CommandResult{Stdout: recoveryTunAddress(7, planner.DefaultTunIPv4CIDR)}, nil
	case "ip -4 address del " + planner.DefaultTunIPv4CIDR + " dev podlaz0":
		if !r.addressPresent {
			return CommandResult{ExitCode: 1, Stderr: "address is already absent"}, errors.New("exit status 1")
		}
		r.addressPresent = false
		return CommandResult{}, nil
	default:
		return CommandResult{ExitCode: -1}, fmt.Errorf("unexpected command: %s", command)
	}
}

func assertTunAddressRecoveryCommands(t *testing.T, runner *tunAddressRecoveryRunner) {
	t.Helper()
	want := []string{
		"ip -details -o link show dev podlaz0",
		"ip -4 -o address show dev podlaz0",
		"ip -4 address del " + planner.DefaultTunIPv4CIDR + " dev podlaz0",
		"ip -4 -o address show dev podlaz0",
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}
