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
	errRunnerApplyFailed           = errors.New("apply failed")
	errRunnerCoreStartFailed       = errors.New("core start failed")
	errRunnerCoreMetadataFailed    = errors.New("core metadata failed")
	errRunnerCoreStartupFailed     = errors.New("core exited during startup")
	errRunnerAdapterStartFailed    = errors.New("adapter start failed")
	errRunnerAdapterMetadataFailed = errors.New("adapter metadata failed")
	errRunnerConnectivityFailed    = errors.New("connectivity failed")
	errRunnerCommitFailed          = errors.New("commit failed")
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
	if h.coreStarted != 1 || h.adapterStarted != 1 || h.connectivityVerified != 1 || h.commitCalled != 1 {
		t.Fatalf("unexpected runner calls: core=%d adapter=%d verify=%d commit=%d", h.coreStarted, h.adapterStarted, h.connectivityVerified, h.commitCalled)
	}
	if h.coreStopped != 0 || h.adapterStopped != 0 {
		t.Fatalf("successful run must not stop runtime: core=%d adapter=%d", h.coreStopped, h.adapterStopped)
	}
	if strings.Join(h.executor.calls, ",") != "apply,verify" {
		t.Fatalf("unexpected executor calls: %#v", h.executor.calls)
	}
	h.requireTransactionState(t, txstate.TransactionCommitted, false)
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
		wantAdapterStarted  int
		wantAdapterStopped  int
		wantVerifyCalls     int
		wantCommitCalls     int
		wantRolledBackState bool
	}{
		{
			name: "execution apply failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.executor.applyErr = errRunnerApplyFailed
			},
			wantErr:             "rolled back applied",
			wantErrIs:           errRunnerApplyFailed,
			wantExecutorCalls:   "apply,rollback",
			wantRolledBackState: true,
		},
		{
			name: "core start failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.startCoreErr = errRunnerCoreStartFailed
			},
			wantErr:             "core start failed",
			wantErrIs:           errRunnerCoreStartFailed,
			wantExecutorCalls:   "apply,verify,rollback",
			wantCoreStarted:     1,
			wantRolledBackState: true,
		},
		{
			name: "connection became active after network transaction",
			configure: func(h *fullTunnelRunnerHarness) {
				h.startCoreErr = errFullTunnelConnectionBecameActive
			},
			wantErr:             "connection already active; rolled back newly applied TUN transaction",
			wantExecutorCalls:   "apply,verify,rollback",
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
			wantExecutorCalls:   "apply,verify,rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     1,
			wantRolledBackState: true,
		},
		{
			name: "core startup verification failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.verifyCoreErr = errRunnerCoreStartupFailed
			},
			wantErr:             "rolled back applied",
			wantErrIs:           errRunnerCoreStartupFailed,
			wantExecutorCalls:   "apply,verify,rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     1,
			wantRolledBackState: true,
		},
		{
			name: "adapter start failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.startAdapterErr = errRunnerAdapterStartFailed
			},
			wantErr:             "adapter start failed",
			wantErrIs:           errRunnerAdapterStartFailed,
			wantExecutorCalls:   "apply,verify,rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     1,
			wantAdapterStarted:  1,
			wantRolledBackState: true,
		},
		{
			name: "adapter metadata failure",
			configure: func(h *fullTunnelRunnerHarness) {
				h.saveAdapterMetadataErr = errRunnerAdapterMetadataFailed
			},
			wantErr:             "adapter metadata failed",
			wantErrIs:           errRunnerAdapterMetadataFailed,
			wantExecutorCalls:   "apply,verify,rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     1,
			wantAdapterStarted:  1,
			wantAdapterStopped:  1,
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
			wantAdapterStarted:  1,
			wantAdapterStopped:  1,
			wantVerifyCalls:     1,
			wantRolledBackState: true,
		},
		{
			name: "core exited before commit",
			configure: func(h *fullTunnelRunnerHarness) {
				h.commitErr = errFullTunnelCoreExitedBeforeCommit
			},
			wantErr:             "rolled back applied podlaz-owned networking state",
			wantExecutorCalls:   "apply,verify,rollback",
			wantCoreStarted:     1,
			wantCoreStopped:     0,
			wantAdapterStarted:  1,
			wantAdapterStopped:  1,
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
			wantAdapterStarted:  1,
			wantAdapterStopped:  1,
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
			if h.adapterStarted != tt.wantAdapterStarted || h.adapterStopped != tt.wantAdapterStopped {
				t.Fatalf("unexpected adapter calls: started=%d stopped=%d", h.adapterStarted, h.adapterStopped)
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

type fullTunnelRunnerHarness struct {
	runtimeDir string
	executor   *recordingTunExecutor

	startCoreErr          error
	saveCoreMetadataErr   error
	verifyCoreErr         error
	startAdapterErr       error
	saveAdapterMetadataErr error
	verifyConnectivityErr error
	commitErr             error

	coreStarted          int
	coreStopped          int
	adapterStarted       int
	adapterStopped       int
	connectivityVerified int
	commitCalled         int
	committedState       xrayState
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
			SOCKSEndpoint:     "127.0.0.1:10080",
			Status:            "test TUN core runtime",
		},
		executor: h.executor,
		now:      fixedClock(),
		startCore: func(context.Context) (fullTunnelCoreHandle, error) {
			h.coreStarted++
			if h.startCoreErr != nil {
				return fullTunnelCoreHandle{}, h.startCoreErr
			}
			return fullTunnelCoreHandle{done: coreDone}, nil
		},
		stopCore: func(fullTunnelCoreHandle) error {
			h.coreStopped++
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
		startAdapter: func(context.Context, tunAdapterRuntimePlan) (fullTunnelAdapterHandle, error) {
			h.adapterStarted++
			if h.startAdapterErr != nil {
				return fullTunnelAdapterHandle{}, h.startAdapterErr
			}
			return fullTunnelAdapterHandle{}, nil
		},
		stopAdapter: func() error {
			h.adapterStopped++
			return nil
		},
		saveAdapterMetadata: func(store txstate.TransactionStore, transactionID string, pid int, now time.Time) error {
			if h.saveAdapterMetadataErr != nil {
				return h.saveAdapterMetadataErr
			}
			return saveTunAdapterRollbackMetadata(store, transactionID, pid, now)
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
	}
}

func (h *fullTunnelRunnerHarness) requireTransactionState(t *testing.T, state txstate.TransactionState, requiresCleanup bool) {
	t.Helper()
	summaries, warnings := txstate.ScanTransactions(h.runtimeDir)
	if len(warnings) > 0 || len(summaries) != 1 {
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
