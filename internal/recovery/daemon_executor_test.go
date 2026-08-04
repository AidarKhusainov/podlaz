package recovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestDaemonCleanupExecutorSkipsRuntimeRoot(t *testing.T) {
	runtimeDir := t.TempDir()
	marker := filepath.Join(runtimeDir, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := (DaemonCleanupExecutor{RuntimeDir: runtimeDir}).Cleanup(context.Background(), Candidate{
		Kind:        "runtime-directory",
		Description: "runtime directory",
		Target:      runtimeDir,
	})

	if result.Status != "skipped" {
		t.Fatalf("expected runtime root cleanup to be skipped, got %#v", result)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("runtime root child must remain after skipped cleanup: %v", err)
	}
}

func TestDaemonRecoveryCompletesAfterRecordedXrayProcessDisappears(t *testing.T) {
	runtimeDir := t.TempDir()
	missingPID := 1 << 30
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", missingPID)); !os.IsNotExist(err) {
		t.Fatalf("test requires an absent process identity for pid %d, stat err=%v", missingPID, err)
	}
	generatedPath := filepath.Join(runtimeDir, generatedDirName, "xray.json")
	if err := os.MkdirAll(filepath.Dir(generatedPath), 0o755); err != nil {
		t.Fatalf("create generated dir: %v", err)
	}
	if err := os.WriteFile(generatedPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		ChildProcesses: []txstate.ChildProcessRollback{{
			Label:     "xray",
			PID:       missingPID,
			ConfigRef: generatedPath,
			Owner:     txstate.TransactionOwner,
		}},
		GeneratedConfigs: []txstate.GeneratedConfigRollback{{
			Path:  generatedPath,
			Owner: txstate.TransactionOwner,
		}},
	})
	candidate := transactionCandidate(path, tx)

	first := ExecuteWithOptions(context.Background(), Options{
		RuntimeDir: runtimeDir,
		Scanner: fakeScanner{result: ScanResult{
			Candidates: []Candidate{candidate},
		}},
		Executor: DaemonCleanupExecutor{RuntimeDir: runtimeDir},
	})

	assertCleanupResult(t, first.Results, "child-process", "recovered", "already absent")
	assertCleanupResult(t, first.Results, "generated-runtime-config", "recovered", "")
	assertCleanupResult(t, first.Results, "transaction-state", "recovered", "")
	if first.HasFailures() || first.HasIncompleteCleanup() {
		t.Fatalf("recovery must complete after recorded Xray disappearance: %#v", first)
	}
	if _, err := os.Stat(generatedPath); !os.IsNotExist(err) {
		t.Fatalf("generated config must be removed after process absence is proven, stat err=%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("transaction file must be removed after complete recovery, stat err=%v", err)
	}

	runner := fakeMissingResourcesRunner()
	second := ExecuteWithOptions(context.Background(), Options{
		RuntimeDir: runtimeDir,
		Runner:     runner,
		Executor:   DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner},
	})
	if len(second.Results) != 0 || len(second.Warnings) != 0 || second.HasFailures() || second.HasIncompleteCleanup() {
		t.Fatalf("second daemon recovery run must be clean: %#v", second)
	}
}

func TestDaemonCleanupExecutorPreservesConfigWhenChildPIDStillExists(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedPath := filepath.Join(runtimeDir, generatedDirName, "xray.json")
	if err := os.MkdirAll(filepath.Dir(generatedPath), 0o755); err != nil {
		t.Fatalf("create generated dir: %v", err)
	}
	if err := os.WriteFile(generatedPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		ChildProcesses: []txstate.ChildProcessRollback{{
			Label:     "xray",
			PID:       os.Getpid(),
			ConfigRef: generatedPath,
			Owner:     txstate.TransactionOwner,
		}},
		GeneratedConfigs: []txstate.GeneratedConfigRollback{{
			Path:  generatedPath,
			Owner: txstate.TransactionOwner,
		}},
	})

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "child-process", "skipped", "process identity cannot be verified")
	assertCleanupResult(t, results, "generated-runtime-config", "skipped", "process absence is unproven")
	assertCleanupResult(t, results, "transaction-state", "skipped", "transaction state was preserved")
	if _, err := os.Stat(generatedPath); err != nil {
		t.Fatalf("generated config must remain while child identity is live and ambiguous: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transaction file must remain when cleanup is skipped: %v", err)
	}
}

func saveTransaction(t *testing.T, runtimeDir string, rollback txstate.RollbackMetadata) (string, txstate.Transaction) {
	t.Helper()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-child-process", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.Rollback = rollback
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save transaction: %v", err)
	}
	return path, tx
}

func transactionCandidate(path string, tx txstate.Transaction) Candidate {
	return Candidate{
		Kind:        "transaction-state",
		Description: "transaction rollback state",
		Target:      path,
		Transaction: &TransactionCandidate{
			ID:              tx.ID,
			State:           string(tx.State),
			Status:          "pending apply",
			RequiresCleanup: true,
			Path:            path,
		},
	}
}

func assertCleanupResult(t *testing.T, results []CleanupResult, kind string, status string, messageSubstring string) {
	t.Helper()
	for _, result := range results {
		if result.Candidate.Kind != kind || result.Status != status {
			continue
		}
		if messageSubstring == "" || strings.Contains(result.Message, messageSubstring) {
			return
		}
	}
	t.Fatalf("cleanup result kind=%q status=%q message containing %q not found in %#v", kind, status, messageSubstring, results)
}

func TestFullRecoveryPreservesForeignReplacementLinkAfterTransactionIdentityMismatch(t *testing.T) {
	runtimeDir := t.TempDir()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-replacement", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.DesiredPlan.TUN.Owner = "xray:tun-inbound"
	tx.DesiredPlan.TUN.InterfaceName = managedInterface
	tx.Rollback.TUNAddresses = []txstate.TUNAddressRollback{{
		Family: "ipv4", InterfaceName: managedInterface, CIDR: "198.18.0.1/32", Scope: "global",
		LinkIndex: 7, LinkKind: "tun", AppearedAfterCore: true, Owner: "podlaz:tun-address",
	}}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save transaction: %v", err)
	}

	runner := &recordingRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]fakeCommand{
			"ip link show dev podlaz0":             {stdout: "8: podlaz0: <POINTOPOINT,UP> mtu 1500"},
			"ip -details -o link show dev podlaz0": {stdout: "8: podlaz0: <POINTOPOINT,UP> mtu 1500\n    tun type tun pi off"},
		},
	}
	scan := (OSScanner{Runner: runner, RuntimeDir: runtimeDir}).Scan(context.Background())
	result := ExecuteWithOptions(context.Background(), Options{
		RuntimeDir: runtimeDir,
		Scanner:    fakeScanner{result: scan},
		Executor:   DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner},
	})

	for _, command := range runner.runCommands {
		if command == "ip link del dev podlaz0" {
			t.Fatalf("foreign replacement link was deleted after identity mismatch: %#v", runner.runCommands)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transaction must remain after identity mismatch: %v", err)
	}
	if !result.HasFailures() && !result.HasIncompleteCleanup() {
		t.Fatalf("replacement mismatch must keep recovery incomplete: %#v", result)
	}
}
