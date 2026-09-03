package daemon

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestResumeNetworkSessionReportsStableFailureStages(t *testing.T) {
	tests := []struct {
		name    string
		stage   string
		prepare func(t *testing.T, continuation *networkSessionContinuationStore) (lifecycleService, networkSessionRecoveryFunc)
	}{
		{
			name:  "state load",
			stage: api.NetworkSessionResumeStageStateLoad,
			prepare: func(t *testing.T, continuation *networkSessionContinuationStore) (lifecycleService, networkSessionRecoveryFunc) {
				t.Helper()
				if err := continuation.Save(testContinuationRequest()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(continuation.stateStore().path(), 0o644); err != nil {
					t.Fatal(err)
				}
				return networkSessionRecordingLifecycle{events: &[]string{}}, successfulNetworkSessionRecovery
			},
		},
		{
			name:  "legacy migration",
			stage: api.NetworkSessionResumeStageLegacyMigration,
			prepare: func(t *testing.T, continuation *networkSessionContinuationStore) (lifecycleService, networkSessionRecoveryFunc) {
				t.Helper()
				continuation.migrateLegacy = func(string, networkSessionContinuationStore) (bool, error) {
					return false, errors.New("migration failed")
				}
				return networkSessionRecordingLifecycle{events: &[]string{}}, successfulNetworkSessionRecovery
			},
		},
		{
			name:  "privacy reconcile",
			stage: api.NetworkSessionResumeStagePrivacyReconcile,
			prepare: func(t *testing.T, continuation *networkSessionContinuationStore) (lifecycleService, networkSessionRecoveryFunc) {
				t.Helper()
				if err := continuation.Save(testContinuationRequest()); err != nil {
					t.Fatal(err)
				}
				continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error {
					return errors.New("privacy reconcile failed")
				}
				return networkSessionRecordingLifecycle{events: &[]string{}}, successfulNetworkSessionRecovery
			},
		},
		{
			name:  "exact recovery",
			stage: api.NetworkSessionResumeStageExactRecovery,
			prepare: func(t *testing.T, continuation *networkSessionContinuationStore) (lifecycleService, networkSessionRecoveryFunc) {
				t.Helper()
				if err := continuation.Save(testContinuationRequest()); err != nil {
					t.Fatal(err)
				}
				continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
				continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
					return api.RecoveryResponse{Mode: "execute", Warnings: []api.RecoveryWarning{{Target: "transaction state", Message: "incomplete"}}}
				}
				return networkSessionRecordingLifecycle{events: &[]string{}}, successfulNetworkSessionRecovery
			},
		},
		{
			name:  "generic recovery",
			stage: api.NetworkSessionResumeStageGenericRecovery,
			prepare: func(t *testing.T, continuation *networkSessionContinuationStore) (lifecycleService, networkSessionRecoveryFunc) {
				t.Helper()
				if err := continuation.Save(testContinuationRequest()); err != nil {
					t.Fatal(err)
				}
				continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
				continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
				return networkSessionRecordingLifecycle{events: &[]string{}}, func(context.Context, api.StatusResponse) api.RecoveryResponse {
					return api.RecoveryResponse{Mode: "execute", Warnings: []api.RecoveryWarning{{Target: "recovery scan", Message: "incomplete"}}}
				}
			},
		},
		{
			name:  "connect replay",
			stage: api.NetworkSessionResumeStageConnectReplay,
			prepare: func(t *testing.T, continuation *networkSessionContinuationStore) (lifecycleService, networkSessionRecoveryFunc) {
				t.Helper()
				if err := continuation.Save(testContinuationRequest()); err != nil {
					t.Fatal(err)
				}
				continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
				continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
				return resumeFailingLifecycle{err: withTunFailurePhase("preflight", noTunTransactionID, "not-started", errors.New("pre-Xray setup failed"))}, successfulNetworkSessionRecovery
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
			lifecycle, recover := tt.prepare(t, &continuation)

			resumed, err := resumeNetworkSession(
				context.Background(),
				continuation,
				lifecycle,
				func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
				recover,
			)
			if err == nil || resumed {
				t.Fatalf("resume failure: resumed=%v err=%v", resumed, err)
			}
			failure, ok := networkSessionResumeFailure(err)
			if !ok {
				t.Fatalf("resume error has no stable classification: %T %v", err, err)
			}
			if failure.ResumeStage != tt.stage {
				t.Fatalf("resume stage=%q want=%q", failure.ResumeStage, tt.stage)
			}

			diagnostic, exists, loadErr := newNetworkSessionResumeDiagnosticStore(runtimeDir, fixedBootID("boot-a")).Load()
			if loadErr != nil {
				t.Fatalf("load resume diagnostic: %v", loadErr)
			}
			if !exists {
				t.Fatal("resume failure must persist a bounded diagnostic")
			}
			if diagnostic.ResumeStage != tt.stage {
				t.Fatalf("diagnostic stage=%q want=%q", diagnostic.ResumeStage, tt.stage)
			}
		})
	}
}

func TestResumeNetworkSessionPersistsPreXrayFailureWithoutSecrets(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	request := testContinuationRequest()
	request.Profile.ID = "profile-private-value"
	request.Profile.Name = "Private profile"
	request.Profile.Server = "private-endpoint.example.test"
	if err := continuation.Save(request); err != nil {
		t.Fatal(err)
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
	secretError := errors.New("generated config for private-endpoint.example.test contained private-token-value")

	_, err := resumeNetworkSession(
		context.Background(),
		continuation,
		resumeFailingLifecycle{err: withTunFailurePhase("preflight", noTunTransactionID, "not-started", secretError)},
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		successfulNetworkSessionRecovery,
	)
	if err == nil {
		t.Fatal("expected pre-Xray replay failure")
	}

	store := newNetworkSessionResumeDiagnosticStore(runtimeDir, fixedBootID("boot-a"))
	record, exists, loadErr := store.Load()
	if loadErr != nil || !exists {
		t.Fatalf("load diagnostic: exists=%v err=%v", exists, loadErr)
	}
	if record.ResumeStage != api.NetworkSessionResumeStageConnectReplay || record.TUNFailurePhase != "preflight" {
		t.Fatalf("unexpected diagnostic: %#v", record)
	}
	if record.RollbackStatus != "not-started" || record.TransactionPresent {
		t.Fatalf("pre-transaction diagnostic metadata=%#v", record)
	}

	data, readErr := os.ReadFile(store.path())
	if readErr != nil {
		t.Fatal(readErr)
	}
	text := string(data)
	for _, forbidden := range []string{"profile-private-value", "Private profile", "private-endpoint.example.test", "private-token-value", "generated config"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("resume diagnostic leaked %q: %s", forbidden, text)
		}
	}
}

func TestResumeNetworkSessionCanRetryAfterReplayFailure(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
	lifecycle := &resumeRetryLifecycle{}

	if resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err == nil || resumed {
		t.Fatalf("first resume must fail: resumed=%v err=%v", resumed, err)
	}
	if resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err != nil || !resumed {
		t.Fatalf("retry must resume the retained session: resumed=%v err=%v", resumed, err)
	}
	if lifecycle.attempts != 2 {
		t.Fatalf("connect attempts=%d want=2", lifecycle.attempts)
	}
}

func successfulNetworkSessionRecovery(context.Context, api.StatusResponse) api.RecoveryResponse {
	return api.RecoveryResponse{Mode: "execute"}
}

func inactiveNetworkSessionStatus(context.Context) api.StatusResponse {
	return api.StatusResponse{Connection: "inactive"}
}

type resumeFailingLifecycle struct{ err error }

func (l resumeFailingLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, l.err
}

func (resumeFailingLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

type resumeRetryLifecycle struct{ attempts int }

func (l *resumeRetryLifecycle) Connect(_ context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	l.attempts++
	if l.attempts == 1 {
		return api.LifecycleResponse{}, withTunFailurePhase("preflight", noTunTransactionID, "not-started", errors.New("transient pre-Xray failure"))
	}
	return api.LifecycleResponse{Connection: "active", Mode: request.Mode, Proxy: "inactive", TUN: "active"}, nil
}

func (*resumeRetryLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}
