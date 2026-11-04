package daemon

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type verifiedConnectSpy struct {
	calls          atomic.Int32
	store          networkSessionStateStore
	protectOnFirst bool
	firstEntered   chan struct{}
	releaseFirst   <-chan struct{}
}

func (s *verifiedConnectSpy) Connect(ctx context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	if s.calls.Add(1) == 1 {
		if s.protectOnFirst {
			p := testArmedPrivacyProtection()
			if err := s.store.SetProtection(&p); err != nil {
				return api.LifecycleResponse{}, err
			}
		}
		if s.firstEntered != nil {
			close(s.firstEntered)
		}
		if s.releaseFirst != nil {
			select {
			case <-s.releaseFirst:
			case <-ctx.Done():
				return api.LifecycleResponse{}, ctx.Err()
			}
		}
	}
	return api.LifecycleResponse{
		Connection: "active", Mode: planner.ModeTun,
		ProfileID: req.Profile.ID, ProfileName: req.Profile.Name,
		Proxy: "active", TUN: "active",
	}, nil
}

func (s *verifiedConnectSpy) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

func verifiedConnectStatus(req api.ConnectRequest) api.StatusResponse {
	return api.StatusResponse{
		Daemon: "running", Service: api.ServiceManual, Connection: "active",
		Mode: planner.ModeTun, ProfileID: req.Profile.ID, ProfileName: req.Profile.Name,
		RuntimeDirectory: "/run/podlaz", ActiveTransactionID: "tx-verified",
		Proxy: "active", TUN: "active",
		TunHealth: &api.TunHealthStatus{State: api.TunHealthVerified, NetworkGeneration: 1},
	}
}

func TestVerifiedRepeatedConnectDoesNotRewriteDurableAuthorityOrRestartCore(t *testing.T) {
	continuation := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID("boot-a"))
	original := testContinuationRequest()
	if err := continuation.Save(original); err != nil {
		t.Fatal(err)
	}
	protection := testArmedPrivacyProtection()
	if err := continuation.stateStore().SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(continuation.stateStore().path())
	if err != nil {
		t.Fatal(err)
	}
	spy := &verifiedConnectSpy{}
	lifecycle := newNetworkSessionLifecycle(spy, continuation)
	lifecycle.verifiedStatus = func(context.Context) api.StatusResponse {
		return verifiedConnectStatus(original)
	}

	for _, policy := range []string{api.HandoffBlock, api.HandoffReplacePodlaz} {
		request := original
		request.Handoff = policy
		response, err := newLifecycleOperationLock().wrap(lifecycle).Connect(context.Background(), request)
		if err != nil || response.Connection != "active" || response.ProfileID != original.Profile.ID {
			t.Fatalf("idempotent %s connect: response=%#v err=%v", policy, response, err)
		}
	}
	if got := spy.calls.Load(); got != 0 {
		t.Fatalf("idempotent connect restarted core %d times", got)
	}
	after, err := os.ReadFile(continuation.stateStore().path())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("idempotent connect rewrote durable Network Session or its protection")
	}
}

func TestVerifiedConnectNoOpRequiresExactLiveAndDurableEvidence(t *testing.T) {
	for _, tt := range []struct {
		name          string
		changeRequest func(*api.ConnectRequest)
		changeStatus  func(*api.StatusResponse)
		noProtection  bool
	}{
		{name: "different profile id", changeRequest: func(req *api.ConnectRequest) {
			req.Profile.ID = "other-profile"
			req.Profile.Name = "Other profile"
		}},
		{name: "same id changed endpoint", changeRequest: func(req *api.ConnectRequest) {
			req.Profile.Server = "changed.example.test"
		}},
		{name: "missing armed privacy authority", noProtection: true},
		{name: "missing verified health", changeStatus: func(s *api.StatusResponse) {
			s.TunHealth = nil
		}},
		{name: "revalidating generation", changeStatus: func(s *api.StatusResponse) {
			s.TunHealth = &api.TunHealthStatus{State: api.TunHealthRevalidating, Classification: api.TunHealthUplinkRevalidating, NetworkGeneration: 2}
		}},
		{name: "missing active transaction", changeStatus: func(s *api.StatusResponse) {
			s.ActiveTransactionID = ""
		}},
		{name: "status inspection incomplete", changeStatus: func(s *api.StatusResponse) {
			s.InspectionWarnings = []api.RecoveryWarning{{Target: "ownership", Message: "incomplete"}}
		}},
		{name: "startup scan incomplete", changeStatus: func(s *api.StatusResponse) {
			s.StartupScan = &api.StartupScanStatus{Status: api.StartupScanStatusIncomplete}
		}},
		{name: "status from another generation", changeStatus: func(s *api.StatusResponse) {
			s.ProfileID = "other-profile"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			continuation := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID("boot-a"))
			original := testContinuationRequest()
			if err := continuation.Save(original); err != nil {
				t.Fatal(err)
			}
			if !tt.noProtection {
				protection := testArmedPrivacyProtection()
				if err := continuation.stateStore().SetProtection(&protection); err != nil {
					t.Fatal(err)
				}
			}
			request := original
			request.Handoff = api.HandoffReplacePodlaz
			if tt.changeRequest != nil {
				tt.changeRequest(&request)
			}
			status := verifiedConnectStatus(original)
			if tt.changeStatus != nil {
				tt.changeStatus(&status)
			}
			spy := &verifiedConnectSpy{}
			lifecycle := newNetworkSessionLifecycle(spy, continuation)
			lifecycle.verifiedStatus = func(context.Context) api.StatusResponse { return status }
			if _, err := lifecycle.Connect(context.Background(), request); err != nil {
				t.Fatalf("non-noop connect: %v", err)
			}
			if got := spy.calls.Load(); got != 1 {
				t.Fatalf("unverified/changed intent incorrectly reported no-op: core calls=%d", got)
			}
		})
	}
}

func TestProtectedMismatchIsTypedConflictWithoutTouchingAuthority(t *testing.T) {
	continuation := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID("boot-a"))
	original := testContinuationRequest()
	if err := continuation.Save(original); err != nil {
		t.Fatal(err)
	}
	protection := testArmedPrivacyProtection()
	if err := continuation.stateStore().SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	before, _, err := continuation.stateStore().Load()
	if err != nil {
		t.Fatal(err)
	}
	blocked := original
	blocked.Profile.ID = "different"
	blocked.Profile.Server = "different.example.test"
	_, err = continuation.stateStore().BeginOrResume(blocked)
	if !errors.Is(err, errProtectedNetworkSessionChangeWithoutHandoff) {
		t.Fatalf("protected mismatch error=%v", err)
	}
	if got := daemonAPIHTTPStatusCode(err); got != 409 {
		t.Fatalf("blocked switch HTTP status=%d, want 409", got)
	}
	after, _, err := continuation.stateStore().Load()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("blocked switch altered exact protection authority: err=%v", err)
	}
}

type incompleteReplacementSpy struct {
	calls []string
	cause error
}

func (s *incompleteReplacementSpy) Connect(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	s.calls = append(s.calls, req.Profile.ID)
	return api.LifecycleResponse{}, withTunFailurePhase("network-apply", "synthetic-tx", "failed", s.cause)
}

func (s *incompleteReplacementSpy) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}

func TestIncompleteProtectedReplacementKeepsAuthorityAndDoesNotStartOldCore(t *testing.T) {
	continuation := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID("boot-a"))
	original := testContinuationRequest()
	if err := continuation.Save(original); err != nil {
		t.Fatal(err)
	}
	protection := testArmedPrivacyProtection()
	if err := continuation.stateStore().SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	target := original
	target.Profile.ID = "target"
	target.Profile.Server = "target.example.test"
	target.Handoff = api.HandoffReplacePodlaz
	cause := errors.New("private candidate rollback details")
	spy := &incompleteReplacementSpy{cause: cause}
	lifecycle := newNetworkSessionLifecycle(spy, continuation)

	_, err := lifecycle.Connect(context.Background(), target)
	if !errors.Is(err, cause) || daemonAPIHTTPStatusCode(err) != 503 {
		t.Fatalf("incomplete replacement must return typed 503 retaining private cause: %v", err)
	}
	if strings.Contains(err.Error(), "private candidate") {
		t.Fatalf("public incomplete rollback error exposed private cause: %s", err)
	}
	if len(spy.calls) != 1 || spy.calls[0] != target.Profile.ID {
		t.Fatalf("incomplete rollback started old core: calls=%#v", spy.calls)
	}
	state, exists, loadErr := continuation.stateStore().Load()
	if loadErr != nil || !exists || state.Replacement == nil ||
		state.Protection == nil || state.Request.Profile.ID != target.Profile.ID {
		t.Fatalf("incomplete replacement lost recoverable authority: exists=%v err=%v state=%#v", exists, loadErr, state)
	}
}

func TestSerializedConnectsUsePostAdmissionStateForNoOpAndConflict(t *testing.T) {
	for _, tt := range []struct {
		name          string
		secondProfile bool
		wantConflict  bool
	}{
		{name: "same profile is a single generation"},
		{name: "stale second profile cannot bypass protected handoff", secondProfile: true, wantConflict: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			continuation := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID("boot-a"))
			first := testContinuationRequest()
			second := first
			if tt.secondProfile {
				second.Profile.ID = "second"
				second.Profile.Server = "second.example.test"
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			spy := &verifiedConnectSpy{
				store: continuation.stateStore(), protectOnFirst: true,
				firstEntered: entered, releaseFirst: release,
			}
			lifecycle := newNetworkSessionLifecycle(spy, continuation)
			lifecycle.verifiedStatus = func(context.Context) api.StatusResponse {
				return verifiedConnectStatus(first)
			}
			locked := newLifecycleOperationLock().wrap(lifecycle)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			firstDone := make(chan error, 1)
			go func() {
				_, err := locked.Connect(ctx, first)
				firstDone <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("first generation did not enter")
			}

			secondDone := make(chan error, 1)
			go func() {
				_, err := locked.Connect(ctx, second)
				secondDone <- err
			}()
			select {
			case err := <-secondDone:
				t.Fatalf("queued second connect returned before first completed: %v", err)
			case <-time.After(25 * time.Millisecond):
			}
			close(release)
			if err := <-firstDone; err != nil {
				t.Fatalf("first connect: %v", err)
			}
			secondErr := <-secondDone
			if tt.wantConflict && (!errors.Is(secondErr, errProtectedNetworkSessionChangeWithoutHandoff) || daemonAPIHTTPStatusCode(secondErr) != 409) {
				t.Fatalf("stale second connect error=%v, want typed 409", secondErr)
			}
			if !tt.wantConflict && secondErr != nil {
				t.Fatalf("same-profile concurrent connect: %v", secondErr)
			}
			if got := spy.calls.Load(); got != 1 {
				t.Fatalf("serialized operations started %d cores, want one", got)
			}
			state, exists, err := continuation.stateStore().Load()
			if err != nil || !exists || state.Request.Profile.ID != first.Profile.ID ||
				state.Protection == nil || state.Replacement != nil {
				t.Fatalf("concurrent connect lost first protected generation: state=%#v err=%v", state, err)
			}
		})
	}
}

// Unknown failure after entering protected replacement cannot erase pending
// durable rollback authority or start the old generation over candidate state.
func TestUnknownProtectedReplacementFailureWithPendingAuthorityFailsClosed(t *testing.T) {
	continuation := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID("boot-a"))
	previous := testContinuationRequest()
	if err := continuation.Save(previous); err != nil {
		t.Fatal(err)
	}
	protection := testArmedPrivacyProtection()
	if err := continuation.stateStore().SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	target := previous
	target.Profile.ID = "replacement"
	target.Profile.Server = "replacement.example.test"
	target.Handoff = api.HandoffReplacePodlaz
	spy := &protectedPrivacyFailureSpy{err: errors.New("candidate outcome unknown")}
	_, err := newNetworkSessionLifecycle(spy, continuation).Connect(context.Background(), target)
	var blocked protectedReplacementCleanupError
	if !errors.As(err, &blocked) || daemonAPIHTTPStatusCode(err) != 503 {
		t.Fatalf("unknown candidate outcome returned non-cleanup error: %v", err)
	}
	if len(spy.calls) != 1 || spy.calls[0] != target.Profile.ID {
		t.Fatalf("started previous core with pending candidate: %#v", spy.calls)
	}
	state, exists, loadErr := continuation.stateStore().Load()
	if loadErr != nil || !exists || state.Replacement == nil || state.Protection == nil {
		t.Fatalf("pending protected rollback authority missing: state=%#v err=%v", state, loadErr)
	}
}

type protectedPrivacyFailureSpy struct {
	calls []string
	err   error
}

func (s *protectedPrivacyFailureSpy) Connect(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	s.calls = append(s.calls, req.Profile.ID)
	return api.LifecycleResponse{}, s.err
}

func (s *protectedPrivacyFailureSpy) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}

// Exact barrier cleanup can fail even when the transaction rollback completed
// or when a later stage is labelled "not-started". Neither permits a reconnect.
func TestIncompletePrivacyReconciliationDominatesCompletedTransactionAndPreflight(t *testing.T) {
	for _, phase := range []string{"completed", "not-started"} {
		t.Run(phase, func(t *testing.T) {
			continuation := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID("boot-a"))
			previous := testContinuationRequest()
			if err := continuation.Save(previous); err != nil {
				t.Fatal(err)
			}
			protection := testArmedPrivacyProtection()
			if err := continuation.stateStore().SetProtection(&protection); err != nil {
				t.Fatal(err)
			}
			target := previous
			target.Profile.ID = "target"
			target.Handoff = api.HandoffReplacePodlaz
			private := errors.New("private failed nft composition")
			cause := protectedReplacementCleanupError{
				cause: withTunFailurePhase("network-apply", "tx-a", phase, private),
			}
			spy := &protectedPrivacyFailureSpy{err: cause}
			_, err := newNetworkSessionLifecycle(spy, continuation).Connect(context.Background(), target)
			if !errors.Is(err, private) || daemonAPIHTTPStatusCode(err) != 503 {
				t.Fatalf("incomplete privacy reconciliation was downgraded: %v", err)
			}
			if len(spy.calls) != 1 {
				t.Fatalf("previous data-plane started over incomplete barrier: %#v", spy.calls)
			}
			if strings.Contains(err.Error(), "private failed nft") {
				t.Fatalf("sanitized HTTP error exposed private cleanup details: %v", err)
			}
			state, exists, loadErr := continuation.stateStore().Load()
			if loadErr != nil || !exists || state.Replacement == nil || state.Protection == nil {
				t.Fatalf("lost durable barrier reconciliation authority: state=%#v err=%v", state, loadErr)
			}
		})
	}
}
