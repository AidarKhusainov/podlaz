package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestRecoveryDoesNotDeleteStandaloneNftablesTableByName(t *testing.T) {
	runner := newNftablesAuthorityRunner()
	candidate := Candidate{Kind: "nftables-table", Description: "nftables table", Target: "inet podlaz"}

	result := (NetworkSessionCleanupExecutor{Runner: runner, RuntimeDir: t.TempDir()}).Cleanup(context.Background(), candidate)

	if result.Status != "skipped" {
		t.Fatalf("standalone nftables observation must not become cleanup authority: %#v", result)
	}
	if runner.directDeletes != 0 || runner.guardedRemoves != 0 {
		t.Fatalf("standalone table identity caused mutation: direct=%d guarded=%d", runner.directDeletes, runner.guardedRemoves)
	}
}

func TestOSCleanupExecutorDoesNotDeleteNftablesTableByName(t *testing.T) {
	runner := newNftablesAuthorityRunner()
	candidate := Candidate{Kind: "nftables-table", Description: "nftables table", Target: "inet podlaz"}

	result := (OSCleanupExecutor{Runner: runner, RuntimeDir: t.TempDir()}).Cleanup(context.Background(), candidate)

	if result.Status != "skipped" {
		t.Fatalf("legacy OS cleanup must not turn table identity into authority: %#v", result)
	}
	if runner.directDeletes != 0 || runner.guardedRemoves != 0 {
		t.Fatalf("legacy OS cleanup caused nftables mutation: direct=%d guarded=%d", runner.directDeletes, runner.guardedRemoves)
	}
}

func TestNetworkSessionCleanupExecutorTreatsProvenAbsentStaleNftablesCandidateAsRecovered(t *testing.T) {
	runner := newNftablesAuthorityRunner()
	runner.tablePresent = false
	candidate := Candidate{Kind: "nftables-table", Description: "nftables table", Target: "inet podlaz"}

	result := (NetworkSessionCleanupExecutor{Runner: runner, RuntimeDir: t.TempDir()}).Cleanup(context.Background(), candidate)

	if result.Status != "recovered" {
		t.Fatalf("proven absent stale nftables candidate must converge without mutation: %#v", result)
	}
	if runner.directDeletes != 0 || runner.guardedRemoves != 0 {
		t.Fatalf("proven absent stale candidate caused mutation: direct=%d guarded=%d", runner.directDeletes, runner.guardedRemoves)
	}
}

func TestSkippedStandaloneNftablesCleanupIsIncomplete(t *testing.T) {
	result := ExecuteResult{Results: []CleanupResult{{
		Candidate: Candidate{Kind: "nftables-table", Description: "nftables table", Target: "inet podlaz"},
		Status:    "skipped",
		Message:   "exact transaction rollback authority is required",
	}}}
	if !result.HasIncompleteCleanup() {
		t.Fatal("skipped standalone nftables state must not be reported as converged recovery")
	}
}

func TestRecoveryPreservesTransactionWhenExactNftablesCompositionIsUnavailable(t *testing.T) {
	runtimeDir := t.TempDir()
	rollback := txstate.RollbackMetadata{NFTables: []txstate.NFTablesRollback{{
		Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall,
	}}}
	path, tx := saveTransaction(t, runtimeDir, rollback)
	runner := newNftablesAuthorityRunner()

	results := (NetworkSessionCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir}).CleanupMany(
		context.Background(), transactionCandidate(path, tx),
	)

	assertCleanupResult(t, results, "nftables-table", "skipped", "exact")
	assertCleanupResult(t, results, "transaction-state", "skipped", "preserved")
	if runner.directDeletes != 0 || runner.guardedRemoves != 0 {
		t.Fatalf("incomplete persisted composition caused nftables mutation: direct=%d guarded=%d", runner.directDeletes, runner.guardedRemoves)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transaction authority must remain after ambiguous nftables recovery: %v", err)
	}
}

func TestRecoveryUsesSemanticGenerationGuardForExactTransactionNftables(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := exactNftablesRecoveryTransaction()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save exact nftables transaction: %v", err)
	}
	runner := newNftablesAuthorityRunner()

	results := (NetworkSessionCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir}).CleanupMany(
		context.Background(), transactionCandidate(path, tx),
	)

	assertCleanupResult(t, results, "nftables-table", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	if runner.directDeletes != 0 {
		t.Fatalf("transaction recovery used name-only nft delete %d time(s)", runner.directDeletes)
	}
	if runner.guardedRemoves != 1 {
		t.Fatalf("generation-guarded nftables removals=%d, want 1", runner.guardedRemoves)
	}
	if runner.removedFamily != "inet" || runner.removedTable != "podlaz" || runner.removedHandle != 10 || runner.removedGeneration != 7 {
		t.Fatalf("unexpected guarded removal target: family=%s table=%s handle=%d generation=%d", runner.removedFamily, runner.removedTable, runner.removedHandle, runner.removedGeneration)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fully converged exact transaction must clear state, stat err=%v", err)
	}
}

func exactNftablesRecoveryTransaction() txstate.Transaction {
	now := time.Now().UTC()
	tx := txstate.NewTransaction("tx-nft-exact", "profile-example", planner.ModeTun, now)
	tx.State = txstate.TransactionApplying
	tx.DesiredPlan.NFT = txstate.NFTPlan{
		Family: "inet",
		Table:  "podlaz",
		Owner:  netexecutor.OwnerFirewall,
		Chains: []txstate.NFTChainPlan{{
			Name:     planner.FirewallOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Owner:    netexecutor.OwnerFirewall,
			Rules: []string{
				`oifname "lo" accept owner podlaz:firewall:loopback`,
			},
		}},
	}
	tx.Rollback = txstate.RollbackMetadata{NFTables: []txstate.NFTablesRollback{{
		Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall,
	}}}
	tx.AppliedSteps = []txstate.AppliedStep{{
		Kind: "nftables", Target: "inet podlaz", Owner: netexecutor.OwnerFirewall, AppliedAt: now,
	}}
	return tx
}

type nftablesAuthorityRunner struct {
	tablePresent      bool
	directDeletes     int
	guardedRemoves    int
	removedFamily     string
	removedTable      string
	removedHandle     uint64
	removedGeneration uint32
}

func newNftablesAuthorityRunner() *nftablesAuthorityRunner {
	return &nftablesAuthorityRunner{tablePresent: true}
}

func (r *nftablesAuthorityRunner) LookPath(file string) (string, error) {
	if file == "nft" {
		return "/usr/bin/nft", nil
	}
	return "", fmt.Errorf("command not found: %s", file)
}

func (r *nftablesAuthorityRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	if filepath.Base(name) != "nft" {
		return CommandResult{ExitCode: -1}, fmt.Errorf("unexpected command %s", name)
	}
	command := strings.Join(args, " ")
	switch command {
	case "delete table inet podlaz":
		r.directDeletes++
		r.tablePresent = false
		return CommandResult{}, nil
	case "-j list tables":
		if !r.tablePresent {
			return CommandResult{Stdout: nftablesAuthorityAbsenceJSON()}, nil
		}
		return CommandResult{Stdout: nftablesAuthorityPresenceJSON()}, nil
	case "-j list table inet podlaz":
		if !r.tablePresent {
			return CommandResult{ExitCode: 1, Stderr: "table absent"}, errors.New("table absent")
		}
		return CommandResult{Stdout: nftablesAuthorityTableJSON()}, nil
	default:
		return CommandResult{ExitCode: -1}, fmt.Errorf("unexpected nft command: %s", command)
	}
}

func (r *nftablesAuthorityRunner) NftablesGeneration(context.Context) (uint32, error) {
	return 7, nil
}

func (r *nftablesAuthorityRunner) NftablesRemoveTable(_ context.Context, family, table string, handle uint64, generation uint32) error {
	r.guardedRemoves++
	r.removedFamily = family
	r.removedTable = table
	r.removedHandle = handle
	r.removedGeneration = generation
	r.tablePresent = false
	return nil
}

func (r *nftablesAuthorityRunner) NftablesReplaceTable(context.Context, string, string, uint64, uint32, planner.TunFirewallPlan) error {
	return errors.New("unexpected nftables replacement during recovery")
}

func nftablesAuthorityPresenceJSON() string {
	return `{"nftables":[{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},{"table":{"family":"inet","name":"podlaz","handle":10}}]}`
}

func nftablesAuthorityAbsenceJSON() string {
	return `{"nftables":[{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}}]}`
}

func nftablesAuthorityTableJSON() string {
	return `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"podlaz","handle":10}},
{"chain":{"family":"inet","table":"podlaz","name":"output","handle":1,"type":"filter","hook":"output","prio":0,"policy":"accept"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":1,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:firewall:loopback"}}
]}`
}
