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

func TestPrepareTunCoexistenceDoesNotBlockHistoricalRouteTableShape(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.PolicyRouting = []netsnapshot.PolicyRoutingSignal{{Kind: "route", Table: netsnapshot.DefaultRouteTableID, Raw: "default dev podlaz0 table 51820"}}
	m := NewXrayManager(t.TempDir())

	if _, err := m.prepareTunCoexistence(context.Background(), s, api.HandoffBlock, netsnapshot.Options{}); err != nil {
		t.Fatalf("historical route-table shape without exact transaction state must be baseline: %v", err)
	}
}

func TestPrepareTunCoexistenceDoesNotBlockHistoricalPolicyRuleShape(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.PolicyRouting = []netsnapshot.PolicyRoutingSignal{{Kind: "rule", Priority: podlazTunRulePriority, Table: netsnapshot.DefaultRouteTableID, Raw: "10000: from all lookup 51820"}}
	m := NewXrayManager(t.TempDir())

	if _, err := m.prepareTunCoexistence(context.Background(), s, api.HandoffBlock, netsnapshot.Options{}); err != nil {
		t.Fatalf("historical policy-rule shape without exact transaction state must be baseline: %v", err)
	}
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
		t.Fatalf("unknown route inspection must publish one diagnostic result, got %#v", resources)
	}
	got := resources[0]
	if got.Kind != "runtime-inspection" || got.Name != "route-table-"+netsnapshot.DefaultRouteTableID || got.Status != netsnapshot.StatusUnknown {
		t.Fatalf("unexpected unknown route inspection resource: %#v", got)
	}
	if !strings.Contains(got.Detail, "permission denied") {
		t.Fatalf("unknown inspection detail should preserve stderr, got %q", got.Detail)
	}
}

func TestPrepareTunCoexistenceIgnoresRolledBackTransactionFile(t *testing.T) {
	runtimeDir := t.TempDir()
	writeRuntimeTransactionState(t, runtimeDir, "rolled", txstate.TransactionRolledBack)
	manager := &XrayManager{RuntimeDir: runtimeDir}

	_, err := manager.prepareTunCoexistence(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	if err != nil {
		t.Fatalf("rolled_back transaction file must not block clean coexistence: %v", err)
	}
}

func TestPrepareTunCoexistenceBlocksInactiveCommittedTransactionFile(t *testing.T) {
	runtimeDir := t.TempDir()
	writeRuntimeTransactionState(t, runtimeDir, "committed", txstate.TransactionCommitted)
	manager := &XrayManager{RuntimeDir: runtimeDir}

	_, err := manager.prepareTunCoexistence(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	assertRuntimeStaleBlockerContains(t, err, "exact Podlaz transaction state", "require recovery")
}

func TestPrepareTunCoexistenceBlocksCleanupRequiredTransactionFiles(t *testing.T) {
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

			_, err := manager.prepareTunCoexistence(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
			assertRuntimeStaleBlockerContains(t, err, "exact Podlaz transaction state", "require recovery")
		})
	}
}

func TestPrepareTunCoexistenceBlocksInvalidTransactionFile(t *testing.T) {
	runtimeDir := writeInvalidRuntimeTransactionFile(t)
	manager := &XrayManager{RuntimeDir: runtimeDir}

	_, err := manager.prepareTunCoexistence(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	assertRuntimeStaleBlockerContains(t, err, "exact Podlaz transaction state", "require recovery")
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
		t.Fatal("expected exact transaction-state coexistence blocker")
	}
	body := err.Error()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("expected blocker to contain %q, got:\n%s", want, body)
		}
	}
}
