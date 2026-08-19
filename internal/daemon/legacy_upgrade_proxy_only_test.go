package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestLegacyUpgradeProxyOnlyTransactionSkipsTunMigrationAndConvergesThroughExactRecovery(t *testing.T) {
	runtimeDir := t.TempDir()
	bootID := "boot-a"
	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	clock := func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) }
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: clock}
	tx := txstate.NewTransaction("legacy-proxy-only", "profile-example", planner.ModeProxyOnly, clock())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, ProcessLabel: "xray", Owner: txstate.TransactionOwner}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: configPath, Owner: txstate.TransactionOwner}}
	if _, err := store.Save(tx); err != nil {
		t.Fatal(err)
	}
	writeLegacyUpgradeMarkerForTest(t, runtimeDir, bootID)

	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID(bootID))
	events := []string{}
	resumed, err := resumeNetworkSession(
		context.Background(),
		continuation,
		networkSessionRecordingLifecycle{events: &events},
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			t.Fatal("no continuation exists, so generic recovery callback must not be needed after exact proxy-only cleanup")
			return api.RecoveryResponse{}
		},
	)
	if err != nil {
		t.Fatalf("proxy-only legacy startup convergence: %v", err)
	}
	if resumed {
		t.Fatal("legacy proxy-only transaction must not be converted into TUN reconnect intent")
	}
	if len(events) != 0 {
		t.Fatalf("legacy proxy-only state must not auto-connect: %#v", events)
	}
	if _, exists, err := continuation.LoadCurrent(); err != nil || exists {
		t.Fatalf("proxy-only migration must not create continuation, exists=%v err=%v", exists, err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, legacyUpgradeMarkerFileName)); !os.IsNotExist(err) {
		t.Fatalf("non-applicable proxy-only marker must be consumed, stat error: %v", err)
	}
	if _, _, err := store.Load(tx.ID); err == nil {
		t.Fatal("exact startup recovery must remove the cleanup-complete proxy-only transaction")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("exact startup recovery must remove transaction-owned generated config, stat error: %v", err)
	}
}
