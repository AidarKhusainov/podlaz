package daemon

import (
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestLifecycleDiagnosticContextLeavesNFTUnprovenWhenActiveTUNHasNoNFTAuthority(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := txstate.NewTransaction("tx-active-missing-nft", "profile-test", planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}

	got := lifecycleDiagnosticContext(runtimeDir, xrayState{
		Connection:    "active",
		Mode:          planner.ModeTun,
		ProfileID:     tx.ProfileID,
		TransactionID: tx.ID,
	})
	if got.NFTTable != doctor.ManagedResourceUnproven || got.NFTPlan != nil {
		t.Fatalf("missing desired/rollback nftables metadata must remain unproven, got %#v", got)
	}
}
