package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type automaticAdmissionLifecycle struct {
	connectStarted chan struct{}
	connectRelease chan struct{}
}

func (l *automaticAdmissionLifecycle) Connect(ctx context.Context, _ api.ConnectRequest) (api.LifecycleResponse, error) {
	close(l.connectStarted)
	select {
	case <-ctx.Done():
		return api.LifecycleResponse{}, ctx.Err()
	case <-l.connectRelease:
		return api.LifecycleResponse{Connection: "active"}, nil
	}
}

func (l *automaticAdmissionLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive"}, nil
}

func TestAutomaticAdmissionRejectsChangedMutationGeneration(t *testing.T) {
	lock := newLifecycleOperationLock()
	before := lock.lifecycleMutationSnapshot()

	finish, err := lock.beginExternalMutation()
	if err != nil {
		t.Fatalf("declare explicit mutation: %v", err)
	}
	finish()

	if admission, ok := lock.tryAdmitAutomaticMutation(before.generation); ok || admission != nil {
		t.Fatalf("stale generation admitted automatic mutation: admission=%#v ok=%v", admission, ok)
	}
}

func TestAutomaticAdmissionRejectsAlreadyPendingMutation(t *testing.T) {
	lock := newLifecycleOperationLock()
	finish, err := lock.beginExternalMutation()
	if err != nil {
		t.Fatalf("declare explicit mutation: %v", err)
	}
	defer finish()

	current := lock.lifecycleMutationSnapshot()
	if !current.pending {
		t.Fatal("explicit mutation was not registered as pending")
	}
	if admission, ok := lock.tryAdmitAutomaticMutation(current.generation); ok || admission != nil {
		t.Fatalf("automatic mutation jumped ahead of pending explicit mutation: admission=%#v ok=%v", admission, ok)
	}
}

func TestAutomaticAdmissionRejectsShutdownFence(t *testing.T) {
	lock := newLifecycleOperationLock()
	before := lock.lifecycleMutationSnapshot()
	lock.fenceMutations()

	if admission, ok := lock.tryAdmitAutomaticMutation(before.generation); ok || admission != nil {
		t.Fatalf("automatic mutation admitted after shutdown fence: admission=%#v ok=%v", admission, ok)
	}
}

func TestAutomaticAdmissionOwnsTokenBeforeSuccess(t *testing.T) {
	lock := newLifecycleOperationLock()
	before := lock.lifecycleMutationSnapshot()

	admission, ok := lock.tryAdmitAutomaticMutation(before.generation)
	if !ok || admission == nil {
		t.Fatalf("automatic mutation admission failed: admission=%#v ok=%v", admission, ok)
	}
	if got := len(lock.token); got != 0 {
		t.Fatalf("operation token remains available after successful automatic admission: len=%d", got)
	}
	state := lock.lifecycleMutationSnapshot()
	if !state.pending || state.generation != before.generation+1 {
		t.Fatalf("automatic admission mutation state=%#v, want pending generation=%d", state, before.generation+1)
	}

	admission.Release()
	if got := len(lock.token); got != 1 {
		t.Fatalf("operation token not returned after automatic admission release: len=%d", got)
	}
}

func TestAutomaticAdmissionFirstForcesLaterExplicitMutationToWait(t *testing.T) {
	lock := newLifecycleOperationLock()
	before := lock.lifecycleMutationSnapshot()
	admission, ok := lock.tryAdmitAutomaticMutation(before.generation)
	if !ok || admission == nil {
		t.Fatalf("automatic mutation admission failed: admission=%#v ok=%v", admission, ok)
	}

	lifecycle := &automaticAdmissionLifecycle{
		connectStarted: make(chan struct{}),
		connectRelease: make(chan struct{}),
	}
	locked := lock.wrap(lifecycle)
	done := make(chan error, 1)
	go func() {
		_, err := locked.Connect(context.Background(), api.ConnectRequest{})
		done <- err
	}()

	select {
	case <-lifecycle.connectStarted:
		t.Fatal("later explicit mutation entered lifecycle while automatic admission owned operation token")
	default:
	}

	admission.Release()
	select {
	case <-lifecycle.connectStarted:
	case <-time.After(time.Second):
		t.Fatal("later explicit mutation did not run after automatic admission released operation token")
	}
	close(lifecycle.connectRelease)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("later explicit mutation failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("later explicit mutation did not finish")
	}
}

func TestAutomaticAdmissionReleaseIsIdempotent(t *testing.T) {
	lock := newLifecycleOperationLock()
	before := lock.lifecycleMutationSnapshot()
	admission, ok := lock.tryAdmitAutomaticMutation(before.generation)
	if !ok || admission == nil {
		t.Fatalf("automatic mutation admission failed: admission=%#v ok=%v", admission, ok)
	}

	admission.Release()
	admission.Release()

	state := lock.lifecycleMutationSnapshot()
	if state.pending {
		t.Fatalf("automatic mutation remains pending after idempotent release: %#v", state)
	}
	if got := len(lock.token); got != 1 {
		t.Fatalf("operation token count after double release=%d, want 1", got)
	}
}
