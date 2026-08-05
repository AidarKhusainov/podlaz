package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestPreflightTunOwnershipBlocksStaleRouteTableOnly(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.PolicyRouting = []netsnapshot.PolicyRoutingSignal{{Kind: "route", Table: netsnapshot.DefaultRouteTableID, Raw: "default dev podlaz0 table 51820"}}

	err := preflightTunOwnership(s, api.HandoffBlock)
	assertRuntimeStaleBlockerContains(t, err, "route-table 51820")
}

func TestPreflightTunOwnershipBlocksStalePolicyRuleOnly(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.PolicyRouting = []netsnapshot.PolicyRoutingSignal{{Kind: "rule", Priority: podlazTunRulePriority, Table: netsnapshot.DefaultRouteTableID, Raw: "10000: from all lookup 51820"}}

	err := preflightTunOwnership(s, api.HandoffBlock)
	assertRuntimeStaleBlockerContains(t, err, "policy-rule 10000")
}

func TestPodlazRuntimeRoutingStaleResourcesTreatsSupportedMissingRouteTableAsAbsent(t *testing.T) {
	withFakeIPCommand(t, `#!/bin/sh
if [ "$1 $2 $3 $4 $5" = "-4 route show table 51820" ]; then
  printf 'Error: ipv4: FIB table does not exist.\nDump terminated\n' >&2
  exit 2
fi
if [ "$1 $2 $3" = "-4 rule show" ]; then
  exit 0
fi
exit 64
`)

	resources := podlazRuntimeRoutingStaleResources(context.Background())
	if len(resources) != 0 {
		t.Fatalf("supported missing route table must prove absence, got %#v", resources)
	}
}

func TestPodlazRuntimeRoutingStaleResourcesKeepsUnknownRouteInspectionFailClosed(t *testing.T) {
	withFakeIPCommand(t, `#!/bin/sh
if [ "$1 $2 $3 $4 $5" = "-4 route show table 51820" ]; then
  printf 'permission denied\n' >&2
  exit 2
fi
if [ "$1 $2 $3" = "-4 rule show" ]; then
  exit 0
fi
exit 64
`)

	resources := podlazRuntimeRoutingStaleResources(context.Background())
	if len(resources) != 1 {
		t.Fatalf("unknown route inspection must publish one blocker, got %#v", resources)
	}
	got := resources[0]
	if got.Kind != "runtime-inspection" || got.Name != "route-table-"+netsnapshot.DefaultRouteTableID || got.Status != netsnapshot.StatusUnknown {
		t.Fatalf("unexpected unknown route inspection resource: %#v", got)
	}
	if !strings.Contains(got.Detail, "permission denied") {
		t.Fatalf("unknown inspection detail should preserve stderr, got %q", got.Detail)
	}
}

func TestPrepareTunHandoffIgnoresRolledBackTransactionFile(t *testing.T) {
	runtimeDir := t.TempDir()
	writeRuntimeTransactionState(t, runtimeDir, "rolled", txstate.TransactionRolledBack)
	manager := &XrayManager{RuntimeDir: runtimeDir}

	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	if err != nil {
		t.Fatalf("rolled_back transaction file must not block clean handoff: %v", err)
	}
}

func TestPrepareTunHandoffBlocksInactiveCommittedTransactionFile(t *testing.T) {
	runtimeDir := t.TempDir()
	writeRuntimeTransactionState(t, runtimeDir, "committed", txstate.TransactionCommitted)
	manager := &XrayManager{RuntimeDir: runtimeDir}

	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	assertRuntimeStaleBlockerContains(t, err, "transaction-file committed.json", "recover --execute --yes")
}

func TestPrepareTunHandoffBlocksCleanupRequiredTransactionFiles(t *testing.T) {
	for _, state := range []txstate.TransactionState{
		txstate.TransactionPlanned,
		txstate.TransactionApplying,
		txstate.TransactionVerifying,
		txstate.TransactionFailed,
	} {
		t.Run(string(state), func(t *testing.T) {
			runtimeDir := t.TempDir()
			writeRuntimeTransactionState(t, runtimeDir, "stale", state)
			manager := &XrayManager{RuntimeDir: runtimeDir}

			_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
			assertRuntimeStaleBlockerContains(t, err, "transaction-file stale.json", "recover --execute --yes")
		})
	}
}

func TestPrepareTunHandoffBlocksInvalidTransactionFile(t *testing.T) {
	runtimeDir := writeInvalidRuntimeTransactionFile(t)
	manager := &XrayManager{RuntimeDir: runtimeDir}

	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	assertRuntimeStaleBlockerContains(t, err, "transaction-file invalid-or-unreadable")
}

func TestPrepareTunHandoffReplacePodlazBlocksRoutingAndTransactionStateAfterRecovery(t *testing.T) {
	originalRecover := controlledPodlazRecover
	originalRouting := podlazRuntimeRoutingStaleResources
	recoverCalls := 0
	controlledPodlazRecover = func(context.Context, string) error {
		recoverCalls++
		return nil
	}
	podlazRuntimeRoutingStaleResources = func(context.Context) []netsnapshot.StaleResource {
		return []netsnapshot.StaleResource{
			{Kind: "route-table", Name: netsnapshot.DefaultRouteTableID, Status: netsnapshot.StatusDetected, Detail: "default dev podlaz0 table 51820"},
			{Kind: "policy-rule", Name: podlazTunRulePriority, Status: netsnapshot.StatusDetected, Detail: "10000: from all lookup 51820"},
		}
	}
	t.Cleanup(func() {
		controlledPodlazRecover = originalRecover
		podlazRuntimeRoutingStaleResources = originalRouting
	})

	runtimeDir := t.TempDir()
	writeRuntimeTransactionState(t, runtimeDir, "stale", txstate.TransactionFailed)
	manager := &XrayManager{RuntimeDir: runtimeDir}
	refreshCalls := 0
	manager.snapshotCollector = func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
		refreshCalls++
		return netsnapshot.FakeResolvedDesktop()
	}

	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffReplacePodlaz, netsnapshot.Options{})
	if recoverCalls != 1 {
		t.Fatalf("controlled recovery calls = %d, want 1", recoverCalls)
	}
	if refreshCalls != 1 {
		t.Fatalf("snapshot refresh calls = %d, want 1", refreshCalls)
	}
	assertRuntimeStaleBlockerContains(t, err, "route-table 51820", "policy-rule 10000", "transaction-file stale.json")
}

func withFakeIPCommand(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ip")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})
}

func writeRuntimeTransactionState(t *testing.T, runtimeDir, id string, state txstate.TransactionState) {
	t.Helper()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	tx := txstate.NewTransaction(id, "test-profile", planner.ModeTun, now)
	tx.State = state
	if state == txstate.TransactionFailed {
		tx.FailureReason = "synthetic safe failure"
	}
	if _, err := store.Save(tx); err != nil {
		t.Fatal(err)
	}
}

func writeInvalidRuntimeTransactionFile(t *testing.T) string {
	t.Helper()
	runtimeDir := t.TempDir()
	dir := filepath.Join(runtimeDir, txstate.TransactionDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return runtimeDir
}

func assertRuntimeStaleBlockerContains(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected stale podlaz blocker before planning/apply")
	}
	body := err.Error()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("expected blocker to contain %q, got:\n%s", want, body)
		}
	}
}
