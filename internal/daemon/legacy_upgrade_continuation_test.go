package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestLegacyUpgradeMigrationReconstructsExactCurrentBootContinuation(t *testing.T) {
	runtimeDir := t.TempDir()
	bootID := "boot-a"
	p := profile.Profile{
		ID:               "legacy-profile",
		Name:             "Legacy profile",
		Source:           profile.SourceSubscription,
		Engine:           profile.EngineXray,
		Server:           "vpn.example.test",
		Port:             443,
		Protocol:         "vless",
		UserIdentity:     "00000000-0000-4000-8000-000000000001",
		Transport:        "grpc",
		Security:         "reality",
		Encryption:       "none",
		Flow:             "xtls-rprx-vision",
		ServerName:       "edge.example.test",
		Fingerprint:      "chrome",
		HostHeader:       "authority.example.test",
		ServiceName:      "vpn-service",
		RealityPublicKey: "example-public-key",
		RealityShortID:   "abcd1234",
		RealitySpiderX:   "/",
	}

	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	opts := engine.DefaultXrayTunConfigOptions()
	opts.OutboundAddressOverride = "203.0.113.10"
	config, err := engine.GenerateXrayTunConfig(p, opts)
	if err != nil {
		t.Fatalf("generate legacy runtime config: %v", err)
	}
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatalf("write legacy runtime config: %v", err)
	}

	clock := func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) }
	txStore := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: clock}
	tx := txstate.NewTransaction("legacy-upgrade", p.ID, planner.ModeTun, clock())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.TUN = txstate.TUNDesiredState{InterfaceName: "podlaz0", MTU: 1500, Owner: txstate.TransactionOwner}
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, ProcessLabel: "xray", Owner: txstate.TransactionOwner}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: configPath, Owner: txstate.TransactionOwner}}
	tx.Rollback.TUN = []txstate.TUNRollback{{InterfaceName: "podlaz0", Owner: txstate.TransactionOwner}}
	tx.Labels = tunTransactionDiagnosticLabels(p)
	if _, err := txStore.Save(tx); err != nil {
		t.Fatalf("save legacy transaction: %v", err)
	}
	writeLegacyUpgradeMarkerForTest(t, runtimeDir, bootID)

	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID(bootID))
	migrated, err := migrateLegacyUpgradeContinuation(runtimeDir, store)
	if err != nil {
		t.Fatalf("migrate legacy upgrade continuation: %v", err)
	}
	if !migrated {
		t.Fatal("expected exact legacy session migration")
	}
	request, ok, err := store.LoadCurrent()
	if err != nil || !ok {
		t.Fatalf("load migrated continuation: ok=%v err=%v", ok, err)
	}
	if request.Mode != planner.ModeTun || request.Profile.ID != p.ID || request.Profile.Server != p.Server || request.Profile.Port != p.Port {
		t.Fatalf("migrated identity/endpoint mismatch: %#v", request)
	}
	if request.Profile.UserIdentity != p.UserIdentity || request.Profile.Transport != "grpc" || request.Profile.Security != "reality" || request.Profile.Flow != p.Flow {
		t.Fatalf("migrated VLESS settings mismatch: %#v", request.Profile)
	}
	if request.Profile.ServerName != p.ServerName || request.Profile.Fingerprint != p.Fingerprint || request.Profile.ServiceName != p.ServiceName || request.Profile.HostHeader != p.HostHeader {
		t.Fatalf("migrated stream settings mismatch: %#v", request.Profile)
	}
	if request.Profile.RealityPublicKey != p.RealityPublicKey || request.Profile.RealityShortID != p.RealityShortID || request.Profile.RealitySpiderX != p.RealitySpiderX {
		t.Fatalf("migrated Reality settings mismatch: %#v", request.Profile)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, legacyUpgradeMarkerFileName)); !os.IsNotExist(err) {
		t.Fatalf("successful migration must consume legacy upgrade marker: %v", err)
	}
}

func TestLegacyUpgradeMigrationRejectsPreviousBootMarker(t *testing.T) {
	runtimeDir := t.TempDir()
	writeLegacyUpgradeMarkerForTest(t, runtimeDir, "boot-old")
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-new"))

	migrated, err := migrateLegacyUpgradeContinuation(runtimeDir, store)
	if err != nil {
		t.Fatalf("previous-boot marker should be discarded, got %v", err)
	}
	if migrated {
		t.Fatal("legacy continuation must not cross reboot boundary")
	}
	if _, ok, err := store.LoadCurrent(); err != nil || ok {
		t.Fatalf("previous-boot marker must not create continuation, ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, legacyUpgradeMarkerFileName)); !os.IsNotExist(err) {
		t.Fatalf("previous-boot marker must be removed: %v", err)
	}
}

func TestLegacyUpgradeMigrationRejectsConfigNotOwnedByExactTransaction(t *testing.T) {
	runtimeDir := t.TempDir()
	bootID := "boot-a"
	clock := func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) }
	txStore := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: clock}
	tx := txstate.NewTransaction("legacy-ambiguous", "legacy-profile", planner.ModeTun, clock())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: filepath.Join(runtimeDir, generatedDirName, generatedXrayName), Owner: txstate.TransactionOwner}
	tx.Rollback.TUN = []txstate.TUNRollback{{InterfaceName: "podlaz0", Owner: txstate.TransactionOwner}}
	if _, err := txStore.Save(tx); err != nil {
		t.Fatal(err)
	}
	writeLegacyUpgradeMarkerForTest(t, runtimeDir, bootID)

	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID(bootID))
	if migrated, err := migrateLegacyUpgradeContinuation(runtimeDir, store); err == nil || migrated {
		t.Fatalf("config without exact generated-config ownership must not migrate, migrated=%v err=%v", migrated, err)
	}
	if _, ok, err := store.LoadCurrent(); err != nil || ok {
		t.Fatalf("ambiguous legacy state must not create continuation, ok=%v err=%v", ok, err)
	}
}

func writeLegacyUpgradeMarkerForTest(t *testing.T, runtimeDir, bootID string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(runtimeDir, legacyUpgradeMarkerFileName), []byte(bootID+"\n"), 0o600); err != nil {
		t.Fatalf("write legacy upgrade marker: %v", err)
	}
}
