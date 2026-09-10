package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestResumeNetworkSessionPersistsFirstAdmittedReplayEvidence(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }

	cause := withTunFailurePhase("preflight", noTunTransactionID, "not-started", errors.New("typed transient replay failure"))
	lifecycle := &scriptedReplayEvidenceLifecycle{errs: []error{
		withNetworkSessionReplaySemantics(networkSessionReplayDispositionRetryable, networkSessionCandidateMutationNotOpened, cause),
	}}

	if resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err == nil || resumed {
		t.Fatalf("replay failure: resumed=%v err=%v", resumed, err)
	}
	state := loadRecoveryEpochState(t, continuation.stateStore())
	if state.RecoveryEpoch != 1 {
		t.Fatalf("recovery epoch=%d want=1", state.RecoveryEpoch)
	}

	record := loadReplayEvidenceDiagnostic(t, continuation)
	if record.ReplayDisposition != string(networkSessionReplayDispositionRetryable) {
		t.Fatalf("top-level replay disposition=%q want=%q", record.ReplayDisposition, networkSessionReplayDispositionRetryable)
	}
	if record.Originating == nil || record.Current == nil {
		t.Fatalf("missing structured replay evidence: %#v", record)
	}
	for name, attempt := range map[string]*networkSessionReplayAttempt{"originating": record.Originating, "current": record.Current} {
		if attempt.SessionID != state.SessionID || attempt.RecoveryEpoch != 1 {
			t.Fatalf("%s identity=(%q,%d) want=(%q,1)", name, attempt.SessionID, attempt.RecoveryEpoch, state.SessionID)
		}
		if attempt.ReplayDisposition != networkSessionReplayDispositionRetryable || attempt.CandidateMutation != networkSessionCandidateMutationNotOpened {
			t.Fatalf("%s replay semantics=(%q,%q)", name, attempt.ReplayDisposition, attempt.CandidateMutation)
		}
	}
}

func TestResumeNetworkSessionPreservesOriginatingAndAdvancesCurrentReplayEvidence(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }

	first := withNetworkSessionReplaySemantics(
		networkSessionReplayDispositionRetryable,
		networkSessionCandidateMutationNotOpened,
		withTunFailurePhase("preflight", noTunTransactionID, "not-started", errors.New("typed transient replay failure")),
	)
	second := withNetworkSessionReplaySemantics(
		networkSessionReplayDispositionInterrupted,
		networkSessionCandidateMutationUnresolved,
		withTunFailurePhase("network-apply", "tun-test-replay", "unknown", errors.New("typed interrupted replay failure")),
	)
	lifecycle := &scriptedReplayEvidenceLifecycle{errs: []error{first, second}}

	for attempt := 1; attempt <= 2; attempt++ {
		if resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err == nil || resumed {
			t.Fatalf("replay attempt %d: resumed=%v err=%v", attempt, resumed, err)
		}
	}
	state := loadRecoveryEpochState(t, continuation.stateStore())
	if state.RecoveryEpoch != 2 {
		t.Fatalf("recovery epoch=%d want=2", state.RecoveryEpoch)
	}

	record := loadReplayEvidenceDiagnostic(t, continuation)
	if record.Originating == nil || record.Current == nil {
		t.Fatalf("missing structured replay evidence: %#v", record)
	}
	if record.Originating.RecoveryEpoch != 1 || record.Originating.ReplayDisposition != networkSessionReplayDispositionRetryable {
		t.Fatalf("originating replay evidence changed: %#v", record.Originating)
	}
	if record.Current.RecoveryEpoch != 2 || record.Current.ReplayDisposition != networkSessionReplayDispositionInterrupted || record.Current.CandidateMutation != networkSessionCandidateMutationUnresolved {
		t.Fatalf("current replay evidence=%#v", record.Current)
	}
	if record.ReplayDisposition != string(networkSessionReplayDispositionInterrupted) || record.RollbackStatus != "unknown" || !record.TransactionPresent {
		t.Fatalf("top-level current replay projection=%#v", record)
	}
}

func TestResumeNetworkSessionPersistsUnknownReplayAsIncompleteUnresolved(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
	lifecycle := &scriptedReplayEvidenceLifecycle{errs: []error{
		withTunFailurePhase("network-apply", "tun-unknown-replay", "completed", errors.New("unsupported replay failure")),
	}}

	if resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err == nil || resumed {
		t.Fatalf("replay failure: resumed=%v err=%v", resumed, err)
	}
	record := loadReplayEvidenceDiagnostic(t, continuation)
	if record.Current == nil {
		t.Fatalf("missing current replay evidence: %#v", record)
	}
	if record.Current.ReplayDisposition != networkSessionReplayDispositionIncomplete || record.Current.CandidateMutation != networkSessionCandidateMutationUnresolved {
		t.Fatalf("unknown replay semantics=(%q,%q)", record.Current.ReplayDisposition, record.Current.CandidateMutation)
	}
}

func loadReplayEvidenceDiagnostic(t *testing.T, continuation networkSessionContinuationStore) networkSessionResumeDiagnostic {
	t.Helper()
	record, exists, err := newNetworkSessionResumeDiagnosticStore(continuation.runtimeDir, continuation.readBootID).Load()
	if err != nil || !exists {
		t.Fatalf("load replay diagnostic: exists=%v err=%v", exists, err)
	}
	return record
}

type scriptedReplayEvidenceLifecycle struct {
	errs     []error
	attempts int
}

func (l *scriptedReplayEvidenceLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	if l.attempts >= len(l.errs) {
		return api.LifecycleResponse{}, errors.New("unexpected replay attempt")
	}
	err := l.errs[l.attempts]
	l.attempts++
	return api.LifecycleResponse{}, err
}

func (*scriptedReplayEvidenceLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive"}, nil
}
