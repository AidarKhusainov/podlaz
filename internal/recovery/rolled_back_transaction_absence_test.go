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

func TestRolledBackTransactionAbsenceRejectsRemainingExactResources(t *testing.T) {
	for _, resource := range []string{"tun-link", "route", "policy-rule", "dns", "nftables", "child-process", "generated-config"} {
		t.Run(resource, func(t *testing.T) {
			runtimeDir := t.TempDir()
			tx := saveRolledBackAbsenceEvidence(t, runtimeDir)
			runner := terminalAbsenceRunner{present: resource}
			exists := func(path string) (bool, error) {
				switch {
				case resource == "child-process" && path == "/proc/4242":
					return true, nil
				case resource == "generated-config" && path == filepath.Join(runtimeDir, "generated", "xray.json"):
					return true, nil
				default:
					return false, nil
				}
			}

			err := verifyRolledBackTransactionAbsenceWithOptions(
				context.Background(),
				runtimeDir,
				tx.ID,
				rolledBackTransactionAbsenceOptions{Runner: runner, PathExists: exists},
			)
			if err == nil {
				t.Fatalf("remaining %s must make terminal absence proof inconclusive", resource)
			}
		})
	}
}

func TestRolledBackTransactionAbsenceAcceptsFreshExactAbsence(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := saveRolledBackAbsenceEvidence(t, runtimeDir)
	err := verifyRolledBackTransactionAbsenceWithOptions(
		context.Background(),
		runtimeDir,
		tx.ID,
		rolledBackTransactionAbsenceOptions{
			Runner: terminalAbsenceRunner{},
			PathExists: func(string) (bool, error) {
				return false, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("fresh exact rolled-back absence proof failed: %v", err)
	}
}

func TestRolledBackTransactionAbsenceRejectsOtherCleanupAuthority(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := saveRolledBackAbsenceEvidence(t, runtimeDir)
	_, other := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		Routes: []txstate.RouteRollback{{Table: "51820", CIDR: "203.0.113.0/24", Dev: managedInterface, Owner: netexecutor.OwnerRoute}},
	})
	_ = other

	err := verifyRolledBackTransactionAbsenceWithOptions(
		context.Background(),
		runtimeDir,
		tx.ID,
		rolledBackTransactionAbsenceOptions{Runner: terminalAbsenceRunner{}, PathExists: func(string) (bool, error) { return false, nil }},
	)
	if err == nil {
		t.Fatal("another recovery-required transaction must block terminal absence proof")
	}
}

func saveRolledBackAbsenceEvidence(t *testing.T, runtimeDir string) txstate.Transaction {
	t.Helper()
	configPath := filepath.Join(runtimeDir, "generated", "xray.json")
	rollback := txstate.RollbackMetadata{
		TUNAddresses: []txstate.TUNAddressRollback{ownedTunAddressRollback(7)},
		Routes: []txstate.RouteRollback{{Table: "51820", CIDR: "0.0.0.0/1", Dev: managedInterface, Owner: netexecutor.OwnerRoute}},
		PolicyRules: []txstate.PolicyRuleRollback{{Priority: 10000, From: "all", Table: "51820", Owner: netexecutor.OwnerPolicyRule}},
		DNS: []txstate.DNSRollback{{Backend: "systemd-resolved", Link: managedInterface, Owner: netexecutor.OwnerDNS}},
		NFTables: []txstate.NFTablesRollback{{Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall}},
		GeneratedConfigs: []txstate.GeneratedConfigRollback{{Path: configPath, Owner: txstate.TransactionOwner}},
		ChildProcesses: []txstate.ChildProcessRollback{{PID: 4242, Label: "xray", ConfigRef: configPath, Owner: txstate.TransactionOwner}},
	}
	_, tx := saveTransaction(t, runtimeDir, rollback)
	tx.State = txstate.TransactionRolledBack
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, ProcessLabel: "xray", Owner: txstate.TransactionOwner}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatalf("save rolled-back evidence: %v", err)
	}
	return tx
}

type terminalAbsenceRunner struct {
	present string
}

func (r terminalAbsenceRunner) LookPath(file string) (string, error) {
	return "/usr/bin/" + file, nil
}

func (r terminalAbsenceRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	key := filepath.Base(name) + " " + strings.Join(args, " ")
	switch {
	case strings.HasPrefix(key, "ip -details -o link show dev "):
		if r.present == "tun-link" {
			return CommandResult{Stdout: "7: podlaz0: <POINTOPOINT,UP> mtu 1500 type tun", ExitCode: 0}, nil
		}
		return missingCommandResult("Device podlaz0 does not exist")
	case strings.HasPrefix(key, "ip -4 route show table "):
		if r.present == "route" {
			return CommandResult{Stdout: "0.0.0.0/1 dev podlaz0 table 51820", ExitCode: 0}, nil
		}
		return CommandResult{ExitCode: 0}, nil
	case strings.HasPrefix(key, "ip -4 rule show priority "):
		if r.present == "policy-rule" {
			return CommandResult{Stdout: "10000: from all lookup 51820", ExitCode: 0}, nil
		}
		return CommandResult{ExitCode: 0}, nil
	case strings.HasPrefix(key, "resolvectl status podlaz0 --no-pager"):
		if r.present == "dns" {
			return CommandResult{Stdout: "Link 7 (podlaz0)\n    Current Scopes: DNS\n         Protocols: +DefaultRoute\nCurrent DNS Server: 192.0.2.53\n       DNS Servers: 192.0.2.53\n        DNS Domain: ~.", RawStdout: "Link 7 (podlaz0)\n    Current Scopes: DNS\n         Protocols: +DefaultRoute\nCurrent DNS Server: 192.0.2.53\n       DNS Servers: 192.0.2.53\n        DNS Domain: ~.\n", ExitCode: 0}, nil
		}
		return missingCommandResult(`Failed to resolve interface "podlaz0": No such device`)
	case strings.HasPrefix(key, "nft list table inet podlaz"):
		if r.present == "nftables" {
			return CommandResult{Stdout: "table inet podlaz { }", ExitCode: 0}, nil
		}
		return missingCommandResult("No such file or directory")
	default:
		return CommandResult{ExitCode: -1}, errors.New("unexpected command: " + key)
	}
}

func missingCommandResult(stderr string) (CommandResult, error) {
	return CommandResult{Stderr: stderr, RawStderr: stderr + "\n", ExitCode: 1}, errors.New(stderr)
}

var _ = os.ErrNotExist
