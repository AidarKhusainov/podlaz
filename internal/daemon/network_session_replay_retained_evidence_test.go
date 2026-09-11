package daemon

import (
	"context"
	"errors"
	"testing"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFullTunnelReplayRetainsRolledBackTransactionEvidence(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.executor.applyErr = errRunnerApplyFailed

	_, err := h.runner().run(withNetworkSessionReplayContext(context.Background()))
	if err == nil || !errors.Is(err, errRunnerApplyFailed) {
		t.Fatalf("expected network apply failure, got %v", err)
	}

	phase, transactionID, rollbackStatus := tunFailureLogFields(err)
	if phase != "network-apply" || transactionID == "" || transactionID == noTunTransactionID || rollbackStatus != "completed" {
		t.Fatalf("replay rollback fields=(%q,%q,%q)", phase, transactionID, rollbackStatus)
	}

	tx, _, loadErr := (txstate.TransactionStore{RuntimeDir: h.runtimeDir}).Load(transactionID)
	if loadErr != nil {
		t.Fatalf("load retained rolled-back transaction evidence: %v", loadErr)
	}
	if tx.State != txstate.TransactionRolledBack || tx.RequiresRecovery() {
		t.Fatalf("retained replay transaction state=%q requires_recovery=%t", tx.State, tx.RequiresRecovery())
	}
}

func TestPersistReplayFailureBindsRetainedTransactionIdentity(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.executor.applyErr = withNetworkSessionReplaySemantics(
		networkSessionReplayDispositionTerminal,
		networkSessionCandidateMutationUnresolved,
		errRunnerApplyFailed,
	)

	_, replayErr := h.runner().run(withNetworkSessionReplayContext(context.Background()))
	if replayErr == nil {
		t.Fatal("expected network apply failure")
	}
	_, transactionID, _ := tunFailureLogFields(replayErr)
	if transactionID == "" || transactionID == noTunTransactionID {
		t.Fatalf("missing replay transaction id: %q", transactionID)
	}

	continuation := newNetworkSessionContinuationStore(h.runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	attemptState, exists, err := continuation.stateStore().BeginRecoveryAttempt()
	if err != nil || !exists {
		t.Fatalf("begin recovery attempt: exists=%v err=%v", exists, err)
	}
	if err := persistNetworkSessionReplayFailure(context.Background(), continuation, attemptState, false, replayErr); err == nil {
		t.Fatal("persistNetworkSessionReplayFailure must preserve the replay failure")
	}

	record := loadReplayEvidenceDiagnostic(t, continuation)
	if record.Current == nil {
		t.Fatalf("missing current replay evidence: %#v", record)
	}
	if record.Current.TransactionID != transactionID {
		t.Fatalf("retained replay transaction identity=%q want=%q", record.Current.TransactionID, transactionID)
	}
}
