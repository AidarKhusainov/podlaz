package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type issue245LifecycleService struct {
	disconnectCalls atomic.Int32
}

func (s *issue245LifecycleService) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}

func (s *issue245LifecycleService) Disconnect(context.Context) (api.LifecycleResponse, error) {
	s.disconnectCalls.Add(1)
	return api.LifecycleResponse{Connection: "inactive"}, nil
}

func TestLifecycleMutationCancelsActiveRevalidationBeforeWaitingForLock(t *testing.T) {
	lock := newLifecycleOperationLock()
	service := &issue245LifecycleService{}
	locked := lock.wrap(service)
	started := make(chan struct{})
	done := make(chan struct{})
	probeCtx, cancelProbe := context.WithCancel(context.Background())
	lock.setRevalidationCancel(cancelProbe)
	go func() {
		defer close(done)
		_ = lock.runRevalidation(probeCtx, func() {
			close(started)
			<-probeCtx.Done()
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("revalidation did not acquire lifecycle lock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := locked.Disconnect(ctx); err != nil {
		t.Fatalf("disconnect after revalidation cancellation: %v", err)
	}
	if service.disconnectCalls.Load() != 1 {
		t.Fatalf("disconnect calls=%d, want 1", service.disconnectCalls.Load())
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled revalidation did not release lifecycle lock")
	}
}

func TestLifecycleMutationBoundedWhenRevalidationIgnoresCancellation(t *testing.T) {
	lock := newLifecycleOperationLock()
	service := &issue245LifecycleService{}
	locked := lock.wrap(service)
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = lock.runRevalidation(context.Background(), func() {
			close(started)
			<-release
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("revalidation did not acquire lifecycle lock")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := locked.Disconnect(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("disconnect error=%v, want context deadline exceeded", err)
	}
	if service.disconnectCalls.Load() != 0 {
		t.Fatalf("disconnect mutated while revalidation held lock: calls=%d", service.disconnectCalls.Load())
	}
	close(release)
}

func TestPendingLifecycleMutationSuppressesNewRevalidation(t *testing.T) {
	lock := newLifecycleOperationLock()
	finishMutation := lock.beginMutation()
	defer finishMutation()

	ran := false
	if err := lock.runRevalidation(context.Background(), func() { ran = true }); err != nil {
		t.Fatalf("suppressed revalidation returned error: %v", err)
	}
	if ran {
		t.Fatal("revalidation ran while a lifecycle mutation was pending")
	}
}
