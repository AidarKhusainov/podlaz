package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type fakeScanner struct {
	result ScanResult
}

func (s fakeScanner) Scan(context.Context) ScanResult {
	return s.result
}

type fakeRunner struct {
	paths    map[string]string
	commands map[string]fakeCommand
}

type fakeCommand struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func (r fakeRunner) LookPath(file string) (string, error) {
	path, ok := r.paths[file]
	if !ok {
		return "", errors.New("command not found")
	}
	return path, nil
}

func (r fakeRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	key := filepath.Base(name) + " " + strings.Join(args, " ")
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

func TestPlanWithFakeScannerRendersRecoveryCandidates(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{Scanner: fakeScanner{result: ScanResult{
		Candidates: []Candidate{
			{Kind: "tun-interface", Description: "TUN interface", Target: "podlaz0"},
			{Kind: "nftables-table", Description: "nftables table", Target: "inet podlaz"},
			{Kind: "generated-runtime-configs", Description: "generated runtime configs", Target: "/run/podlaz/generated"},
		},
	}}})

	got := plan.String()
	want := []string{
		"podlaz recovery dry-run",
		"Would recover TUN interface: podlaz0",
		"Would recover nftables table: inet podlaz",
		"Would recover generated runtime configs: /run/podlaz/generated",
		"No changes were applied.",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
	if strings.Contains(got, "runtime directory") || strings.Contains(got, "command:") {
		t.Fatalf("dry-run output must not render runtime root or executable cleanup commands, got %q", got)
	}
}

func TestPlanWithFakeScannerRendersTransactionCandidate(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{Scanner: fakeScanner{result: ScanResult{
		Candidates: []Candidate{{
			Kind:        "transaction-state",
			Description: "transaction rollback state",
			Target:      "/run/podlaz/transactions/tx-apply.json",
			Transaction: &TransactionCandidate{
				ID:                "tx-apply",
				State:             "applying",
				Status:            "pending apply",
				RollbackAvailable: true,
				RequiresCleanup:   true,
				Path:              "/run/podlaz/transactions/tx-apply.json",
			},
		}},
	}}})

	got := plan.String()
	want := []string{
		"podlaz recovery dry-run",
		"Transaction: pending apply",
		"Rollback available: yes",
		"State path: /run/podlaz/transactions/tx-apply.json",
		"No changes were applied.",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
	if strings.Contains(got, "Would recover transaction rollback state") {
		t.Fatalf("transaction candidates must render structured details, got %q", got)
	}
}

func TestPlanWithFakeScannerRendersCleanHost(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{Scanner: fakeScanner{}})

	got := plan.String()
	want := []string{
		"podlaz recovery dry-run",
		"No podlaz-owned recovery candidates found.",
		"No changes were applied.",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
	if strings.Contains(got, "Would recover") {
		t.Fatalf("clean host output must not contain recovery candidates, got %q", got)
	}
}

func TestPlanWithFakeScannerDoesNotRenderCleanHostWhenWarningsExist(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{Scanner: fakeScanner{result: ScanResult{
		Warnings: []Warning{{
			Target:  "TUN interface podlaz0",
			Message: "ip command is unavailable",
		}},
	}}})

	got := plan.String()
	if strings.Contains(got, "No podlaz-owned recovery candidates found.") {
		t.Fatalf("warning-only output must not claim a clean host, got %q", got)
	}
	if !strings.Contains(got, "Warning: could not inspect TUN interface podlaz0: ip command is unavailable") {
		t.Fatalf("expected warning in output, got %q", got)
	}
	if !strings.Contains(got, "No changes were applied.") {
		t.Fatalf("expected no-mutation footer, got %q", got)
	}
}

func TestPlanDoesNotReportRuntimeRootAsCandidate(t *testing.T) {
	runtimeDir := t.TempDir()

	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: runtimeDir,
		Runner:     fakeMissingResourcesRunner(),
	})

	if len(plan.Candidates) != 0 {
		t.Fatalf("expected runtime root alone to be ignored, got candidates %#v", plan.Candidates)
	}
	got := plan.String()
	if strings.Contains(got, runtimeDir) || strings.Contains(got, "runtime directory") {
		t.Fatalf("runtime root must not be rendered as a recovery candidate, got %q", got)
	}
	if !strings.Contains(got, "No podlaz-owned recovery candidates found.") {
		t.Fatalf("expected clean host message, got %q", got)
	}
}

func TestExecuteDoesNotReportRuntimeRootWhenOnlyRuntimeDirExists(t *testing.T) {
	runtimeDir := t.TempDir()

	result := ExecuteWithOptions(context.Background(), Options{
		RuntimeDir: runtimeDir,
		Runner:     fakeMissingResourcesRunner(),
	})

	if len(result.Results) != 0 {
		t.Fatalf("expected no cleanup results when only runtime root exists, got %#v", result.Results)
	}
	if result.HasFailures() || result.HasIncompleteCleanup() {
		t.Fatalf("runtime root alone must not be treated as failed or incomplete cleanup, got %#v", result)
	}
	got := result.String()
	if strings.Contains(got, runtimeDir) || strings.Contains(got, "runtime directory") {
		t.Fatalf("runtime root must not be rendered in execute output, got %q", got)
	}
	if !strings.Contains(got, "No podlaz-owned recovery candidates found.") {
		t.Fatalf("expected clean execute output, got %q", got)
	}
}

func TestOSScannerDetectsOwnedResources(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, "generated")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatalf("create generated dir: %v", err)
	}

	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: runtimeDir,
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":  "/usr/sbin/ip",
				"nft": "/usr/sbin/nft",
			},
			commands: map[string]fakeCommand{
				"ip link show dev podlaz0": {
					stdout: "2: podlaz0: <POINTOPOINT,UP> mtu 1500",
				},
				"nft list table inet podlaz": {
					stdout: "table inet podlaz {}",
				},
			},
		},
	})

	assertCandidate(t, plan, "tun-interface", "podlaz0")
	assertCandidate(t, plan, "nftables-table", "inet podlaz")
	assertCandidate(t, plan, "generated-runtime-configs", generatedDir)
	assertNoCandidateKind(t, plan, "runtime-directory")
	if len(plan.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", plan.Warnings)
	}
}

func TestOSScannerExplainsPendingTransactionState(t *testing.T) {
	runtimeDir := t.TempDir()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-apply", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.Rollback = txstate.RollbackMetadata{
		TUN: []txstate.TUNRollback{{
			InterfaceName: "podlaz0",
			Owner:         txstate.TransactionOwner,
		}},
	}
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save transaction: %v", err)
	}

	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: runtimeDir,
		Runner:     fakeMissingResourcesRunner(),
	})

	transaction := assertTransactionCandidate(t, plan, "tx-apply")
	if transaction.State != "applying" || transaction.Status != "pending apply" || !transaction.RollbackAvailable || !transaction.RequiresCleanup {
		t.Fatalf("unexpected transaction candidate: %#v", transaction)
	}
	got := plan.String()
	want := []string{
		"Transaction: pending apply",
		"Rollback available: yes",
		"State path: " + filepath.Join(runtimeDir, txstate.TransactionDirName, "tx-apply.json"),
		"No changes were applied.",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestOSScannerRendersCleanHostFromMissingOwnedResources(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
		Runner:     fakeMissingResourcesRunner(),
	})

	if len(plan.Candidates) != 0 {
		t.Fatalf("expected clean host, got candidates %#v", plan.Candidates)
	}
	got := plan.String()
	if !strings.Contains(got, "No podlaz-owned recovery candidates found.") {
		t.Fatalf("expected clean host message, got %q", got)
	}
}

func TestOSScannerPreservesInspectionWarnings(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":  "/usr/sbin/ip",
				"nft": "/usr/sbin/nft",
			},
			commands: map[string]fakeCommand{
				"ip link show dev podlaz0": {
					stderr:   "Operation not permitted",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
				"nft list table inet podlaz": {
					stderr:   "Error: No such file or directory",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
			},
		},
	})

	if len(plan.Warnings) != 1 {
		t.Fatalf("expected one warning, got %#v", plan.Warnings)
	}
	got := plan.String()
	if strings.Contains(got, "No podlaz-owned recovery candidates found.") {
		t.Fatalf("warning-only output must not claim a clean host, got %q", got)
	}
	if !strings.Contains(got, "Warning: could not inspect TUN interface podlaz0") || !strings.Contains(got, "Operation not permitted") {
		t.Fatalf("expected inspection warning in output, got %q", got)
	}
}

func fakeMissingResourcesRunner() fakeRunner {
	return fakeRunner{
		paths: map[string]string{
			"ip":  "/usr/sbin/ip",
			"nft": "/usr/sbin/nft",
		},
		commands: map[string]fakeCommand{
			"ip link show dev podlaz0": {
				stderr:   "Device \"podlaz0\" does not exist.",
				exitCode: 1,
				err:      errors.New("exit status 1"),
			},
			"nft list table inet podlaz": {
				stderr:   "Error: No such file or directory",
				exitCode: 1,
				err:      errors.New("exit status 1"),
			},
		},
	}
}

func assertCandidate(t *testing.T, plan PlanResult, kind string, target string) {
	t.Helper()
	for _, candidate := range plan.Candidates {
		if candidate.Kind == kind && candidate.Target == target {
			return
		}
	}
	t.Fatalf("candidate kind=%q target=%q not found in %#v", kind, target, plan.Candidates)
}

func assertNoCandidateKind(t *testing.T, plan PlanResult, kind string) {
	t.Helper()
	for _, candidate := range plan.Candidates {
		if candidate.Kind == kind {
			t.Fatalf("candidate kind=%q must not be present in %#v", kind, plan.Candidates)
		}
	}
}

func assertTransactionCandidate(t *testing.T, plan PlanResult, id string) *TransactionCandidate {
	t.Helper()
	for _, candidate := range plan.Candidates {
		if candidate.Transaction != nil && candidate.Transaction.ID == id {
			return candidate.Transaction
		}
	}
	t.Fatalf("transaction candidate id=%q not found in %#v", id, plan.Candidates)
	return nil
}
