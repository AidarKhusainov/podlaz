package daemon

import (
	"context"
	"testing"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFullTunnelTransactionRunnerPersistsTrackedChildStartTimeBeforeNetworkApply(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	runner := h.runner()
	runner.startCore = func(context.Context) (fullTunnelCoreHandle, error) {
		return fullTunnelCoreHandle{done: make(chan struct{}), pid: 4242}, nil
	}
	runner.readCoreStartTime = func(pid int) (string, error) {
		if pid != 4242 {
			t.Fatalf("read process start time for pid=%d", pid)
		}
		return "987654", nil
	}
	h.onNetworkApplied = func() {
		summaries, warnings := txstate.ScanTransactions(h.runtimeDir)
		if len(warnings) != 0 || len(summaries) != 1 {
			t.Fatalf("scan transaction before network apply: summaries=%#v warnings=%#v", summaries, warnings)
		}
		tx, _, err := (txstate.TransactionStore{RuntimeDir: h.runtimeDir}).Load(summaries[0].ID)
		if err != nil {
			t.Fatalf("load transaction before network apply: %v", err)
		}
		if len(tx.Rollback.ChildProcesses) != 1 {
			t.Fatalf("tracked child rollback metadata=%#v", tx.Rollback.ChildProcesses)
		}
		child := tx.Rollback.ChildProcesses[0]
		if child.PID != 4242 || child.StartTime != "987654" || child.ConfigRef == "" || child.Label != "xray" || child.Owner != txstate.TransactionOwner {
			t.Fatalf("incomplete tracked child identity: %#v", child)
		}
	}

	if _, err := runner.run(context.Background()); err != nil {
		t.Fatalf("run full-tunnel transaction: %v", err)
	}
}
