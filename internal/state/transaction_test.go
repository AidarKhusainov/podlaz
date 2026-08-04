package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTransactionStorePersistsVersionedOwnedState(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	store := TransactionStore{
		RuntimeDir: t.TempDir(),
		Now: func() time.Time {
			return now
		},
	}
	tx := NewTransaction("tx-1", "profile-1", "tun", now)
	tx.Rollback = fullRollbackMetadata()

	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save transaction: %v", err)
	}
	if filepath.Base(path) != "tx-1.json" {
		t.Fatalf("unexpected transaction path %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected 0600 transaction file permissions, got %o", got)
	}

	loaded, loadedPath, err := store.Load("tx-1")
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if loadedPath != path {
		t.Fatalf("expected loaded path %q, got %q", path, loadedPath)
	}
	if loaded.SchemaVersion != TransactionSchemaVersion || loaded.Owner != TransactionOwner {
		t.Fatalf("transaction must be versioned and owned, got %#v", loaded)
	}
	if !loaded.Rollback.Available() {
		t.Fatalf("expected rollback metadata to be available")
	}
}

func TestTransactionStateTransitionsAreIdempotentAndValidated(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	tx := NewTransaction("tx-2", "profile-1", "tun", now)

	changed, err := Transition(&tx, TransactionPlanned, now.Add(time.Second))
	if err != nil {
		t.Fatalf("same-state transition should be valid: %v", err)
	}
	if changed {
		t.Fatalf("same-state transition should be idempotent")
	}
	if _, err := Transition(&tx, TransactionCommitted, now.Add(time.Second)); err == nil {
		t.Fatalf("planned -> committed must be rejected")
	}

	states := []TransactionState{
		TransactionApplying,
		TransactionApplied,
		TransactionVerifying,
		TransactionCommitted,
		TransactionRollingBack,
		TransactionRolledBack,
	}
	for _, next := range states {
		changed, err = Transition(&tx, next, now.Add(time.Second))
		if err != nil {
			t.Fatalf("transition to %s failed: %v", next, err)
		}
		if !changed {
			t.Fatalf("transition to %s should change state", next)
		}
	}
}

func TestScanTransactionsReportsPendingAndInvalidState(t *testing.T) {
	runtimeDir := t.TempDir()
	store := TransactionStore{RuntimeDir: runtimeDir}
	tx := NewTransaction("tx-apply", "profile-1", "tun", time.Now().UTC())
	tx.State = TransactionApplying
	tx.Rollback = RollbackMetadata{
		TUN: []TUNRollback{{
			InterfaceName: "podlaz0",
			Owner:         TransactionOwner,
		}},
	}
	if _, err := store.Save(tx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, TransactionDirName, "bad.json"), []byte(`{"schema_version":"bad"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	summaries, warnings := ScanTransactions(runtimeDir)
	if len(summaries) != 1 {
		t.Fatalf("expected one valid transaction summary, got %#v", summaries)
	}
	summary := summaries[0]
	if summary.StatusLine() != "pending apply" {
		t.Fatalf("expected pending apply status, got %q", summary.StatusLine())
	}
	if !summary.RollbackAvailable || !summary.RequiresCleanup {
		t.Fatalf("expected rollback and cleanup flags, got %#v", summary)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "bad.json") {
		t.Fatalf("expected invalid transaction warning, got %#v", warnings)
	}
}

func TestTransactionRejectsPersistentSecretFields(t *testing.T) {
	for _, key := range []string{"tok" + "en", "pass" + "word", "private" + "_key"} {
		t.Run(key, func(t *testing.T) {
			tx := NewTransaction("tx-secret", "profile-1", "tun", time.Now().UTC())
			tx.Labels = map[string]string{key: "redacted"}
			if err := ValidateTransaction(tx); err == nil {
				t.Fatal("expected transaction validation to reject secret field")
			}
		})
	}
}

func TestTransactionRejectsPersistentSecretValues(t *testing.T) {
	tx := NewTransaction("tx-secret-value", "profile-1", "tun", time.Now().UTC())
	tx.Labels = map[string]string{"debug": "tok" + "en=redacted"}
	if err := ValidateTransaction(tx); err == nil {
		t.Fatal("expected transaction validation to reject secret-looking value")
	}
}

func TestRepeatedRollbackPlanningIsStable(t *testing.T) {
	runtimeDir := t.TempDir()
	store := TransactionStore{RuntimeDir: runtimeDir}
	tx := NewTransaction("tx-rollback", "profile-1", "tun", time.Now().UTC())
	tx.State = TransactionFailed
	tx.Rollback = fullRollbackMetadata()
	if _, err := store.Save(tx); err != nil {
		t.Fatal(err)
	}

	first, firstWarnings := store.Scan()
	second, secondWarnings := store.Scan()
	if len(firstWarnings) != 0 || len(secondWarnings) != 0 {
		t.Fatalf("expected no scan warnings, got %v and %v", firstWarnings, secondWarnings)
	}
	if len(first) != 1 || len(second) != 1 || first[0] != second[0] {
		t.Fatalf("rollback planning should be stable and idempotent, got %#v then %#v", first, second)
	}
	if first[0].StatusLine() != "failed (requires cleanup)" {
		t.Fatalf("expected failed cleanup status, got %#v", first[0])
	}
}

func fullRollbackMetadata() RollbackMetadata {
	return RollbackMetadata{
		TUN: []TUNRollback{{
			InterfaceName: "podlaz0",
			Owner:         TransactionOwner,
		}},
		Routes: []RouteRollback{{
			Table: "podlaz",
			CIDR:  "0.0.0.0/0",
			Dev:   "podlaz0",
			Owner: TransactionOwner,
		}},
		PolicyRules: []PolicyRuleRollback{{
			Priority: 51820,
			Table:    "podlaz",
			Owner:    TransactionOwner,
		}},
		DNS: []DNSRollback{{
			Backend:  "systemd-resolved",
			Link:     "podlaz0",
			Previous: []string{"1.1.1.1"},
			Owner:    TransactionOwner,
		}},
		NFTables: []NFTablesRollback{{
			Family: "inet",
			Table:  "podlaz",
			Owner:  TransactionOwner,
		}},
		GeneratedConfigs: []GeneratedConfigRollback{{
			Path:  "/run/podlaz/generated/xray.json",
			Owner: TransactionOwner,
		}},
		ChildProcesses: []ChildProcessRollback{{
			PID:       1234,
			Label:     "xray",
			ConfigRef: "/run/podlaz/generated/xray.json",
			Owner:     TransactionOwner,
		}},
	}
}

func TestTransactionStorePersistsBoundTunAddressOwnership(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	store := TransactionStore{RuntimeDir: t.TempDir(), Now: func() time.Time { return now }}
	tx := NewTransaction("tx-address", "example-profile", "tun", now)
	tx.DesiredPlan.TUNAddress = TUNAddressDesiredState{
		Family:        "ipv4",
		InterfaceName: "podlaz0",
		CIDR:          "198.51.100.1/32",
		Scope:         "global",
		LinkIndex:     7,
		LinkKind:      "tun",
		Owner:         "podlaz:tun-address",
	}
	tx.Rollback.TUNAddresses = []TUNAddressRollback{{
		Family:        "ipv4",
		InterfaceName: "podlaz0",
		CIDR:          "198.51.100.1/32",
		Scope:         "global",
		LinkIndex:     7,
		LinkKind:      "tun",
		Owner:         "podlaz:tun-address",
	}}
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save TUN address transaction: %v", err)
	}
	loaded, _, err := store.Load(tx.ID)
	if err != nil {
		t.Fatalf("load TUN address transaction: %v", err)
	}
	if loaded.DesiredPlan.TUNAddress.LinkIndex != 7 || len(loaded.Rollback.TUNAddresses) != 1 || loaded.Rollback.TUNAddresses[0].CIDR != "198.51.100.1/32" {
		t.Fatalf("unexpected persisted TUN address ownership: %#v", loaded)
	}
}

func TestLegacyTransactionWithoutTunAddressMetadataRemainsReadable(t *testing.T) {
	runtimeDir := t.TempDir()
	path := filepath.Join(runtimeDir, TransactionDirName, "legacy.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "schema_version": "podlaz.transaction.v1",
  "owner": "podlaz",
  "id": "legacy",
  "mode": "tun",
  "state": "failed",
  "created_at": "2026-08-03T12:00:00Z",
  "updated_at": "2026-08-03T12:00:00Z",
  "desired_plan": {"tun": {"interface_name": "podlaz0", "owner": "xray:tun-inbound"}},
  "rollback": {"dns": [{"backend": "systemd-resolved", "link": "podlaz0", "owner": "podlaz:dns-link"}]}
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadTransactionFile(path)
	if err != nil {
		t.Fatalf("load legacy transaction: %v", err)
	}
	if loaded.DesiredPlan.TUNAddress != (TUNAddressDesiredState{}) || len(loaded.Rollback.TUNAddresses) != 0 {
		t.Fatalf("legacy state must not gain guessed address authority: %#v", loaded)
	}
}

func TestCommittedTransactionRequiresCleanupUntilActiveRuntimeIsProven(t *testing.T) {
	tx := NewTransaction("tx-committed", "profile-1", "tun", time.Now().UTC())
	tx.State = TransactionCommitted
	if !tx.RequiresCleanup() {
		t.Fatal("persisted committed transaction must remain recoverable after restart")
	}
}
