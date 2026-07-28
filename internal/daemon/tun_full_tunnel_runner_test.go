package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

var (
	errRunnerBeginFailed        = errors.New("begin failed")
	errRunnerApplyFailed        = errors.New("apply failed")
	errRunnerCoreStartFailed    = errors.New("core start failed")
	errRunnerCoreMetadataFailed = errors.New("core metadata failed")
	errRunnerCoreStartupFailed  = errors.New("core exited during startup")
	errRunnerConnectivityFailed = errors.New("connectivity failed")
	errRunnerCommitFailed       = errors.New("commit failed")
)

func TestFullTunnelTransactionRunnerCommitsActiveState(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)

	active, err := h.runner().run(context.Background())
	if err != nil {
		t.Fatalf("run full-tunnel transaction failed: %v", err)
	}

	if active.Connection != "active" || active.Mode != planner.ModeTun || active.TransactionID == "" {
		t.Fatalf("unexpected active state: %#v", active)
	}
	if h.committedState.TransactionID != active.TransactionID {
		t.Fatalf("expected committed active state %#v, got %#v", active, h.committedState)
	}
	if h.coreStarted != 1 || h.connectivityVerified != 1 || h.commitCalled != 1 {
		t.Fatalf("unexpected runner calls: core=%d verify=%d commit=%d", h.coreStarted, h.connectivityVerified, h.commitCalled)
	}
	if h.coreStopped != 0 {
		t.Fatalf("successful run must not stop runtime: core=%d", h.coreStopped)
	}
	if strings.Join(h.executor.calls, ",") != "apply,verify" {
		t.Fatalf("unexpected executor calls: %#v", h.executor.calls)
	}
	h.requireTransactionState(t, txstate.TransactionCommitted, false)
}

func TestFullTunnelTransactionRunnerStartsXrayBeforeApplyingHostNetworking(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	var order []string
	h.onCoreStarted = func() { order = append(order, "core") }
	h.onNetworkApplied = func() { order = append(order, "network") }

	_, err := h.runner().run(context.Background())
	if err != nil {
		t.Fatalf("run full-tunnel transaction: %v", err)
	}
	if strings.Join(order, ",") != "core,network" {
		t.Fatalf("expected Xray TUN inbound to start before host networking apply, got %#v", order)
	}
}

func TestFullTunnelTransactionRunnerRollsBackHostNetworkingBeforeStoppingXray(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.verifyConnectivityErr = errRunnerConnectivityFailed
	var order []string
	h.onRollback = func() { order = append(order, "rollback") }
	h.onCoreStopped = func() { order = append(order, "stop-core") }

	_, err := h.runner().run(context.Background())
	if !errors.Is(err, errRunnerConnectivityFailed) {
		t.Fatalf("expected connectivity failure, got %v", err)
	}
	if strings.Join(order, ",") != "rollback,stop-core" {
		t.Fatalf("expected host networking rollback before Xray stop, got %#v", order)
	}
}

func TestFullTunnelTransactionRunnerFailureBranchesRollbackAppliedState(t *testing.T) {
	tests := []struct {
		name                string
		configure           func(*fullTunnelRunnerHarness)
		wantErr             string
		wantErrIs           error
		wantExecutorCalls   string
		wantCoreStarted     int
		wantCoreStopped     int
		wantVerifyCalls     int
		wantCommitCalls     int
		wantRolledBackState bool
	}{
		{
			name: "transaction begin failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.beginErr = errRunnerBeginFailed
			},
			wantErr:   "begin failed",
			wantErrIs: errRunnerBeginFailed,
		},
		{
			name: "core start failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.startCoreErr = errRunnerCoreStartFailed
			},
			wantErr:             "core start failed",
			wantErrIs:           errRunnerCoreStartFailed,
			wantExecutorCalls:   "rollback",
			wantCoreStarted:     1,
			wantRolledBackState: true,
		},
		{
			name: "connection became active after transaction begin",
			configure: func(h *fullTunnelRunnerHarness) {
				h.startCoreErr = errFullTunnelConnectionBecameActive
			},
			wantErr:             "connection already active; rolled back newly opened TUN transaction",
			wantErrIs:           errFullTunnelConnectionBecameActive,
			wantExecutorCalls:   "rollback",
			wantCoreStarted:     1,
			wantRolledBackState: true,
		},
		{
			name: "core metadata failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.saveCoreMetadataErr = errRunnerCoreMetadataFailed
			},
			wantErr:             "core metadata failed",
			wantErrIs:           errRunnerCoreMetadataFailed,
			wantExecutorCalls:   "rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     1,
			wantRolledBackState: true,
		},
		{
			name: "core startup verification failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.verifyCoreErr = errRunnerCoreStartupFailed
			},
			wantErr:             "rollback completed",
			wantErrIs:           errRunnerCoreStartupFailed,
			wantExecutorCalls:   "rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     1,
			wantRolledBackState: true,
		},
		{
			name: "host network apply failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.executor.applyErr = errRunnerApplyFailed
			},
			wantErr:             "apply failed",
			wantErrIs:           errRunnerApplyFailed,
			wantExecutorCalls:   "apply,rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     1,
			wantRolledBackState: true,
		},
		{
			name: "connectivity verification failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.verifyConnectivityErr = errRunnerConnectivityFailed
			},
			wantErr:             "rolled back applied",
			wantErrIs:           errRunnerConnectivityFailed,
			wantExecutorCalls:   "apply,verify,rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     1,
			wantVerifyCalls:     1,
			wantRolledBackState: true,
		},
		{
			name: "core exited before commit",
			configure: func(h *fullTunnelRunnerHarness) {
				h.commitErr = errFullTunnelCoreExitedBeforeCommit
			},
			wantErr:             "rollback completed",
			wantErrIs:           errFullTunnelCoreExitedBeforeCommit,
			wantExecutorCalls:   "apply,verify,rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     0,
			wantVerifyCalls:     1,
			wantCommitCalls:     1,
			wantRolledBackState: true,
		},
		{
			name: "commit failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.commitErr = errRunnerCommitFailed
			},
			wantErr:             "commit failed",
			wantErrIs:           errRunnerCommitFailed,
			wantExecutorCalls:   "apply,verify,rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     1,
			wantVerifyCalls:     1,
			wantCommitCalls:     1,
			wantRolledBackState: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newFullTunnelRunnerHarness(t)
			tt.configure(h)

			_, err := h.runner().run(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("expected error to match %v, got %v", tt.wantErrIs, err)
			}
			if calls := strings.Join(h.executor.calls, ","); calls != tt.wantExecutorCalls {
				t.Fatalf("unexpected executor calls: got %q want %q", calls, tt.wantExecutorCalls)
			}
			if h.coreStarted != tt.wantCoreStarted || h.coreStopped != tt.wantCoreStopped {
				t.Fatalf("unexpected core calls: started=%d stopped=%d", h.coreStarted, h.coreStopped)
			}
			if h.connectivityVerified != tt.wantVerifyCalls {
				t.Fatalf("unexpected connectivity verification calls: got %d want %d", h.connectivityVerified, tt.wantVerifyCalls)
			}
			if h.commitCalled != tt.wantCommitCalls {
				t.Fatalf("unexpected commit calls: got %d want %d", h.commitCalled, tt.wantCommitCalls)
			}
			if tt.wantRolledBackState {
				h.requireTransactionState(t, txstate.TransactionRolledBack, false)
			}
		})
	}
}

func TestFullTunnelTransactionRunnerConnectivityFailureMarksRollbackCompleted(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.verifyConnectivityErr = newTunVerificationError("dns", "DNS through the tunnel did not resolve example.com before timeout", errRunnerConnectivityFailed)

	_, err := h.runner().run(context.Background())
	if err == nil {
		t.Fatal("expected connectivity verification failure")
	}
	if !strings.Contains(err.Error(), "Rollback completed; no podlaz-owned network changes were left applied.") {
		t.Fatalf("expected friendly rollback completion marker, got:\n%s", err.Error())
	}
	if !errors.Is(err, errRunnerConnectivityFailed) {
		t.Fatalf("expected wrapped connectivity cause, got %v", err)
	}
	h.requireTransactionState(t, txstate.TransactionRolledBack, false)
}

type fullTunnelRunnerHarness struct {
	runtimeDir string
	executor   *recordingTunExecutor

	beginErr              error
	startCoreErr          error
	saveCoreMetadataErr   error
	verifyCoreErr         error
	verifyConnectivityErr error
	commitErr             error

	coreStarted          int
	coreStopped          int
	connectivityVerified int
	commitCalled         int
	committedState       xrayState

	onCoreStarted    func()
	onCoreStopped    func()
	onNetworkApplied func()
	onRollback       func()
}

func newFullTunnelRunnerHarness(t *testing.T) *fullTunnelRunnerHarness {
	t.Helper()
	return &fullTunnelRunnerHarness{
		runtimeDir: t.TempDir(),
		executor:   &recordingTunExecutor{},
	}
}

func (h *fullTunnelRunnerHarness) runner() *fullTunnelTransactionRunner {
	coreDone := make(chan struct{})
	return &fullTunnelTransactionRunner{
		runtimeDir: h.runtimeDir,
		profile:    profile.Profile{ID: "test-profile", Name: "Test Profile"},
		plan:       transactionPlanForTest(),
		corePlan: tunCoreRuntimePlan{
			RuntimeConfigPath: filepath.Join(h.runtimeDir, generatedDirName, generatedXrayName),
			Status:            "test TUN core runtime",
		},
		executor: h.executor,
		now:      fixedClock(),
		beginNetworkTransaction: func(ctx context.Context, runtimeDir string, p profile.Profile, plan planner.TunPlan, now func() time.Time) (tunTransactionResult, error) {
			if h.beginErr != nil {
				return tunTransactionResult{}, h.beginErr
			}
			return beginTunTransaction(ctx, runtimeDir, p, plan, now)
		},
		applyNetworkTransaction: func(ctx context.Context, result tunTransactionResult, executor tunPlanExecutor) error {
			if h.onNetworkApplied != nil {
				h.onNetworkApplied()
			}
			return applyVerifyTunTransactionDeferredRollback(ctx, result, executor)
		},
		startCore: func(context.Context) (fullTunnelCoreHandle, error) {
			h.coreStarted++
			if h.onCoreStarted != nil {
				h.onCoreStarted()
			}
			if h.startCoreErr != nil {
				return fullTunnelCoreHandle{}, h.startCoreErr
			}
			return fullTunnelCoreHandle{done: coreDone}, nil
		},
		stopCore: func(fullTunnelCoreHandle) error {
			h.coreStopped++
			if h.onCoreStopped != nil {
				h.onCoreStopped()
			}
			return nil
		},
		saveCoreMetadata: func(store txstate.TransactionStore, transactionID, runtimeConfigPath string, pid int, now time.Time) error {
			if h.saveCoreMetadataErr != nil {
				return h.saveCoreMetadataErr
			}
			return saveCoreRollbackMetadata(store, transactionID, runtimeConfigPath, pid, now)
		},
		verifyCoreStarted: func(<-chan struct{}) error {
			return h.verifyCoreErr
		},
		verifyConnectivity: func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
			h.connectivityVerified++
			return h.verifyConnectivityErr
		},
		commitActiveState: func(store txstate.TransactionStore, transactionID string, _ fullTunnelCoreHandle, active xrayState) error {
			h.commitCalled++
			if h.commitErr != nil {
				return h.commitErr
			}
			if err := commitTunTransaction(store, transactionID); err != nil {
				return err
			}
			h.committedState = active
			return nil
		},
		rollbackTransaction: func(ctx context.Context, transactionID string, plan planner.TunPlan, executor tunPlanExecutor, stopChildren tunRollbackChildStopper) error {
			if h.onRollback != nil {
				h.onRollback()
			}
			return rollbackVerifiedTunTransactionWithChildStopper(ctx, h.runtimeDir, transactionID, plan, executor, stopChildren)
		},
	}
}

func (h *fullTunnelRunnerHarness) requireTransactionState(t *testing.T, state txstate.TransactionState, requiresCleanup bool) {
	t.Helper()
	summaries, warnings := txstate.ScanTransactions(h.runtimeDir)
	if len(warnings) > 0 {
		t.Fatalf("unexpected transaction scan warnings: summaries=%#v warnings=%#v", summaries, warnings)
	}
	if len(summaries) == 0 && state == txstate.TransactionRolledBack && !requiresCleanup {
		statuses, statusWarnings := transactionStatuses(h.runtimeDir)
		if len(statusWarnings) > 0 || len(statuses) != 0 {
			t.Fatalf("unexpected status transaction scan after removed rollback transaction: statuses=%#v warnings=%#v", statuses, statusWarnings)
		}
		return
	}
	if len(summaries) != 1 {
		t.Fatalf("unexpected transaction scan: summaries=%#v warnings=%#v", summaries, warnings)
	}
	if summaries[0].State != state || summaries[0].RequiresCleanup != requiresCleanup {
		t.Fatalf("unexpected transaction state: got %#v want state=%s requires_cleanup=%t", summaries[0], state, requiresCleanup)
	}

	statuses, statusWarnings := transactionStatuses(h.runtimeDir)
	if len(statusWarnings) > 0 || len(statuses) != 1 {
		t.Fatalf("unexpected status transaction scan: statuses=%#v warnings=%#v", statuses, statusWarnings)
	}
	if statuses[0].State != string(state) || statuses[0].RequiresCleanup != requiresCleanup {
		t.Fatalf("unexpected status transaction state: got %#v want state=%s requires_cleanup=%t", statuses[0], state, requiresCleanup)
	}
}
