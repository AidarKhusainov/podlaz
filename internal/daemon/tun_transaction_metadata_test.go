package daemon

import (
	"context"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunTransactionRecordsRollbackOnlyForAppliedSteps(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &recordingTunExecutor{steps: []netexecutor.Step{
		{Kind: "tun-device", Target: "podlaz0", Owner: netexecutor.OwnerTunDevice},
		{Kind: "route", Target: "podlaz default", Owner: netexecutor.OwnerRoute},
	}}
	result, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err != nil {
		t.Fatalf("run TUN transaction failed: %v", err)
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if len(tx.Rollback.Routes) != 1 || tx.Rollback.Routes[0].CIDR != "default" {
		t.Fatalf("expected rollback metadata to include only applied route, got %#v", tx.Rollback.Routes)
	}
	if len(tx.Rollback.PolicyRules) != 0 {
		t.Fatalf("expected skipped policy rule to be absent from rollback metadata, got %#v", tx.Rollback.PolicyRules)
	}
	if len(tx.Rollback.TUN) != 1 {
		t.Fatalf("expected applied TUN device rollback metadata, got %#v", tx.Rollback.TUN)
	}
}
