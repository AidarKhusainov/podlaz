package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFullTunnelTransactionRunnerRunsPreflightBeforeTransactionMutation(t *testing.T) {
	runtimeDir := t.TempDir()
	preflightErr := errors.New("preflight failed")
	beginCalled := false
	runner := &fullTunnelTransactionRunner{
		runtimeDir: runtimeDir,
		profile:    profile.Profile{ID: "test-profile", Name: "Test Profile"},
		plan:       transactionPlanForTest(),
		corePlan:   tunCoreRuntimePlan{Status: "test TUN core runtime"},
		executor:   &recordingTunExecutor{},
		now:        fixedClock(),
		preflightCore: func(context.Context) error {
			return preflightErr
		},
		beginNetworkTransaction: func(context.Context, string, profile.Profile, planner.TunPlan, func() time.Time) (tunTransactionResult, error) {
			beginCalled = true
			return tunTransactionResult{}, errors.New("begin must not be called after preflight failure")
		},
	}

	_, err := runner.run(context.Background())
	if !errors.Is(err, preflightErr) {
		t.Fatalf("expected preflight error before transaction mutation, got %v", err)
	}
	if beginCalled {
		t.Fatal("preflight failure must stop before beginTunTransaction")
	}
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(summaries) != 0 || len(warnings) != 0 {
		t.Fatalf("unsupported Xray TUN preflight must not leave transaction artifacts: summaries=%#v warnings=%#v", summaries, warnings)
	}
}

func TestFullTunnelTransactionRunnerPreflightOrder(t *testing.T) {
	var order []string
	runtimeDir := t.TempDir()
	runner := &fullTunnelTransactionRunner{
		runtimeDir: runtimeDir,
		profile:    profile.Profile{ID: "test-profile", Name: "Test Profile"},
		plan:       transactionPlanForTest(),
		corePlan:   tunCoreRuntimePlan{Status: "test TUN core runtime"},
		executor:   &recordingTunExecutor{},
		now:        fixedClock(),
		preflightCore: func(context.Context) error {
			order = append(order, "preflight")
			return nil
		},
		beginNetworkTransaction: func(ctx context.Context, runtimeDir string, p profile.Profile, plan planner.TunPlan, now func() time.Time) (tunTransactionResult, error) {
			order = append(order, "begin")
			return beginTunTransaction(ctx, runtimeDir, p, plan, now)
		},
		startCore: func(context.Context) (fullTunnelCoreHandle, error) {
			return fullTunnelCoreHandle{}, errors.New("stop after begin")
		},
	}

	_, err := runner.run(context.Background())
	if err == nil {
		t.Fatal("expected runner to stop after transaction begin")
	}
	if got := strings.Join(order, ","); got != "preflight,begin" {
		t.Fatalf("wrong preflight order: %s", got)
	}
}
