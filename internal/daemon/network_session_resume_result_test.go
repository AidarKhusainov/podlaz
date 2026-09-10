package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestApplyNetworkSessionResumeResultTerminalConvergenceDoesNotPublishResumeSuccess(t *testing.T) {
	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
	gate.Block()
	response := api.RecoveryResponse{
		Mode: "execute",
		NetworkSession: &api.NetworkSessionRecoveryState{
			Authority:         api.NetworkSessionRecoveryAuthorityPresent,
			Intent:            string(networkSessionIntentResume),
			StartupGate:       api.NetworkSessionStartupGateBlocked,
			LastResumeOutcome: api.NetworkSessionResumeOutcomeFailed,
			CleanupAuthority:  api.NetworkSessionCleanupAuthoritySessionProtection,
			NextAction:        api.NetworkSessionRecoveryActionRetryResume,
		},
	}

	got := applyNetworkSessionResumeResult(response, gate, networkSessionResumeTerminalConverged, nil)
	if gate.Blocked() {
		t.Fatal("terminal convergence must release startup mutation gate after caller finalization")
	}
	if got.NetworkSession != nil {
		t.Fatalf("terminal convergence published stale resume authority: %#v", got.NetworkSession)
	}
}

func TestBootAutostartTerminalResumeResultCommitsAttemptBeforeSessionFinalization(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attemptStore.Admit(manifest); err != nil {
		t.Fatal(err)
	}
	if err := continuation.Save(bootRequest(manifest.Configuration)); err != nil {
		t.Fatal(err)
	}

	result, err := runBootAutostartStartup(
		context.Background(),
		manifestStore,
		attemptStore,
		continuation,
		&bootAutostartRecordingLifecycle{},
		func(context.Context) (networkSessionResumeResult, error) {
			if err := continuation.disarm(networkSessionIntentTerminal); err != nil {
				return networkSessionResumeUnknown, err
			}
			return networkSessionResumeTerminalConverged, nil
		},
	)
	if err != nil || result != bootAutostartStartupTerminal {
		t.Fatalf("terminal resumed boot attempt: result=%q err=%v", result, err)
	}
	attempt, exists, err := attemptStore.LoadCurrent()
	if err != nil || !exists {
		t.Fatalf("load completed boot attempt: exists=%v err=%v", exists, err)
	}
	if attempt.State != bootAutostartAttemptTerminal || attempt.TerminalReason != bootAutostartTerminalSessionFailure {
		t.Fatalf("terminal boot attempt=%#v", attempt)
	}
	if _, exists, err := continuation.stateStore().Load(); err != nil || exists {
		t.Fatalf("terminal boot attempt retained finalized Network Session: exists=%v err=%v", exists, err)
	}
}

func TestSerializedStartupLifecycleOperationBlocksCompetingLockedLifecycle(t *testing.T) {
	lock := newLifecycleOperationLock()
	entered := make(chan struct{})
	release := make(chan struct{})
	inner := &blockingStartupOperationLifecycle{entered: entered, release: release}
	locked := lock.wrap(inner)

	startupDone := make(chan error, 1)
	go func() {
		_, err := runSerializedStartupLifecycleOperation(context.Background(), lock, func() (bootAutostartStartupResult, error) {
			_, err := inner.Connect(context.Background(), testContinuationRequest())
			return bootAutostartStartupContinued, err
		})
		startupDone <- err
	}()
	<-entered

	competingEntered := make(chan struct{})
	competingDone := make(chan error, 1)
	go func() {
		_, err := locked.Connect(context.Background(), testContinuationRequest())
		close(competingEntered)
		competingDone <- err
	}()

	select {
	case <-competingEntered:
		t.Fatal("competing lifecycle mutation interleaved while startup owned operation token")
	default:
	}
	close(release)
	if err := <-startupDone; err != nil {
		t.Fatalf("serialized startup operation: %v", err)
	}
	if err := <-competingDone; err != nil {
		t.Fatalf("competing lifecycle mutation after startup: %v", err)
	}
}

type blockingStartupOperationLifecycle struct {
	entered chan struct{}
	release chan struct{}
}

func (l *blockingStartupOperationLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	select {
	case <-l.entered:
	default:
		close(l.entered)
	}
	<-l.release
	return api.LifecycleResponse{Connection: "active"}, nil
}

func (*blockingStartupOperationLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive"}, nil
}
