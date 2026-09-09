package recovery

import (
	"strings"
	"testing"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestExactNftablesRollbackPlanRejectsIncompleteOrAmbiguousAuthority(t *testing.T) {
	base := exactNftablesRecoveryTransaction()
	entry := base.Rollback.NFTables[0]

	tests := []struct {
		name    string
		mutate  func(*txstate.Transaction)
		wantErr string
	}{
		{
			name: "desired without rollback authority",
			mutate: func(tx *txstate.Transaction) {
				tx.Rollback.NFTables = nil
			},
			wantErr: "cardinality=0",
		},
		{
			name: "rollback authority without desired composition",
			mutate: func(tx *txstate.Transaction) {
				tx.DesiredPlan.NFT = txstate.NFTPlan{}
			},
			wantErr: "desired identity does not match rollback authority",
		},
		{
			name: "desired identity mismatch",
			mutate: func(tx *txstate.Transaction) {
				tx.DesiredPlan.NFT.Table = "podlaz-other"
			},
			wantErr: "desired identity does not match rollback authority",
		},
		{
			name: "duplicate rollback authority",
			mutate: func(tx *txstate.Transaction) {
				tx.Rollback.NFTables = append(tx.Rollback.NFTables, tx.Rollback.NFTables[0])
			},
			wantErr: "cardinality=2",
		},
		{
			name: "missing chains",
			mutate: func(tx *txstate.Transaction) {
				tx.DesiredPlan.NFT.Chains = nil
			},
			wantErr: "composition has no chains",
		},
		{
			name: "incomplete chain metadata",
			mutate: func(tx *txstate.Transaction) {
				tx.DesiredPlan.NFT.Chains[0].Hook = ""
			},
			wantErr: "chain metadata is incomplete",
		},
		{
			name: "duplicate chain identity",
			mutate: func(tx *txstate.Transaction) {
				duplicate := tx.DesiredPlan.NFT.Chains[0]
				duplicate.Rules = nil
				tx.DesiredPlan.NFT.Chains[0].Rules = nil
				tx.DesiredPlan.NFT.Chains = append(tx.DesiredPlan.NFT.Chains, duplicate)
			},
			wantErr: "is duplicated",
		},
		{
			name: "ambiguous multi-chain rule mapping",
			mutate: func(tx *txstate.Transaction) {
				second := tx.DesiredPlan.NFT.Chains[0]
				second.Name = "forward"
				second.Rules = nil
				tx.DesiredPlan.NFT.Chains = append(tx.DesiredPlan.NFT.Chains, second)
			},
			wantErr: "rule-to-chain mapping is ambiguous",
		},
		{
			name: "rule without ownership marker",
			mutate: func(tx *txstate.Transaction) {
				tx.DesiredPlan.NFT.Chains[0].Rules[0] = `oifname "lo" accept`
			},
			wantErr: "has no ownership marker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := base
			tx.Rollback.NFTables = append([]txstate.NFTablesRollback(nil), base.Rollback.NFTables...)
			tx.DesiredPlan.NFT.Chains = append([]txstate.NFTChainPlan(nil), base.DesiredPlan.NFT.Chains...)
			for i := range tx.DesiredPlan.NFT.Chains {
				tx.DesiredPlan.NFT.Chains[i].Rules = append([]string(nil), base.DesiredPlan.NFT.Chains[i].Rules...)
			}
			tt.mutate(&tx)

			_, err := exactNftablesRollbackPlan(tx, entry)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("exact rollback reconstruction error=%v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestExactNftablesRollbackPlanReconstructsPersistedComposition(t *testing.T) {
	tx := exactNftablesRecoveryTransaction()
	entry := tx.Rollback.NFTables[0]

	plan, err := exactNftablesRollbackPlan(tx, entry)
	if err != nil {
		t.Fatalf("reconstruct exact nftables rollback plan: %v", err)
	}
	if plan.Family != entry.Family || plan.Table != entry.Table {
		t.Fatalf("reconstructed identity=%s %s, want %s %s", plan.Family, plan.Table, entry.Family, entry.Table)
	}
	if len(plan.Chains) != 1 || len(plan.Rules) != 1 {
		t.Fatalf("reconstructed composition lost chains/rules: %#v", plan)
	}
}
