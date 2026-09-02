package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestTerminalCleanupUsesExactTransactionRollbackAfterCoreExit(t *testing.T) {
	regularDisconnects := 0
	exactTransaction := ""
	cleaner := tunTerminalDataPlaneCleaner{
		current: func() (xrayState, *tunRuntimeProcessIdentity) {
			return xrayState{
				Connection:    api.ConnectionCoreExited,
				Mode:          planner.ModeTun,
				TransactionID: "tx-degraded",
			}, nil
		},
		disconnect: func(context.Context) (api.LifecycleResponse, error) {
			regularDisconnects++
			return api.LifecycleResponse{}, nil
		},
		disconnectTransaction: func(_ context.Context, transactionID string) (api.LifecycleResponse, error) {
			exactTransaction = transactionID
			return api.LifecycleResponse{}, nil
		},
	}

	if _, err := cleaner.Cleanup(context.Background(), "tx-degraded"); err != nil {
		t.Fatalf("cleanup degraded transaction: %v", err)
	}
	if regularDisconnects != 0 {
		t.Fatalf("degraded cleanup used regular manager disconnect %d times, want 0", regularDisconnects)
	}
	if exactTransaction != "tx-degraded" {
		t.Fatalf("exact rollback transaction=%q, want tx-degraded", exactTransaction)
	}
}

func TestTerminalCleanupRejectsStaleTransactionAuthority(t *testing.T) {
	mutated := false
	cleaner := tunTerminalDataPlaneCleaner{
		current: func() (xrayState, *tunRuntimeProcessIdentity) {
			return xrayState{Connection: api.ConnectionCoreExited, Mode: planner.ModeTun, TransactionID: "tx-new"}, nil
		},
		disconnect: func(context.Context) (api.LifecycleResponse, error) {
			mutated = true
			return api.LifecycleResponse{}, nil
		},
		disconnectTransaction: func(context.Context, string) (api.LifecycleResponse, error) {
			mutated = true
			return api.LifecycleResponse{}, nil
		},
	}

	if _, err := cleaner.Cleanup(context.Background(), "tx-stale"); err == nil {
		t.Fatal("stale terminal transaction unexpectedly acquired cleanup authority")
	}
	if mutated {
		t.Fatal("stale terminal transaction mutated data plane")
	}
}
