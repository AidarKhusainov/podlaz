package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestSuccessfulReplayFinalizesRetainedRolledBackEvidenceFromPriorRetryableAttempt(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }

	txStore := txstate.TransactionStore{RuntimeDir: runtimeDir}
	retained := txstate.NewTransaction("tun-retryable-retained", "profile-test", "tun", time.Now().UTC())
	retained.State = txstate.TransactionRolledBack
	retainedPath, err := txStore.Save(retained)
	if err != nil {
		t.Fatal(err)
	}
	transactionDir := filepath.Dir(retainedPath)
	if err := os.Chmod(transactionDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(transactionDir, 0o700) }()

	firstFailure := withNetworkSessionReplaySemantics(
		networkSessionReplayDispositionRetryable,
		networkSessionCandidateMutationRolledBack,
		withTunFailurePhase("network-apply", retained.ID, "completed", errors.New("typed transient replay failure")),
	)
	lifecycle := &scriptedReplayEvidenceLifecycle{errs: []error{firstFailure, nil}}

	if result, err := resumeNetworkSessionResult(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err == nil || result != networkSessionResumeUnknown {
		t.Fatalf("first retryable replay must report retained evidence cleanup failure: result=%q err=%v", result, err)
	}
	if _, err := os.Stat(retainedPath); err != nil {
		t.Fatalf("retained rolled-back evidence disappeared despite failed cleanup: %v", err)
	}
	if err := os.Chmod(transactionDir, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := resumeNetworkSessionResult(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery)
	if err != nil || result != networkSessionResumeResumed {
		t.Fatalf("second replay must resume successfully: result=%q err=%v", result, err)
	}
	if _, err := os.Stat(retainedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful replay retained stale rolled-back transaction evidence: %v", err)
	}
	if _, exists, err := newNetworkSessionResumeDiagnosticStore(runtimeDir, fixedBootID("boot-a")).Load(); err != nil || exists {
		t.Fatalf("successful replay retained stale diagnostic: exists=%v err=%v", exists, err)
	}
}
