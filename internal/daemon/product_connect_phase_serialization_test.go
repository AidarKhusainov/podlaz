package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type phaseSerializedLifecycle struct {
	entered chan string
	release map[string]chan struct{}
}

func (s *phaseSerializedLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	s.entered <- request.Profile.ID
	select {
	case <-s.release[request.Profile.ID]:
	case <-ctx.Done():
		return api.LifecycleResponse{}, ctx.Err()
	}
	return api.LifecycleResponse{Connection: "active", Mode: request.Mode, Proxy: "active", TUN: "active"}, nil
}

func (s *phaseSerializedLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

func TestConnectingStatusTracksActiveSerializedOperationNotQueuedCaller(t *testing.T) {
	tracker := &productLifecyclePhaseTracker{}
	lock := newLifecycleOperationLock()
	first := testContinuationRequest()
	second := first
	second.Profile.ID = "second-profile"
	second.Profile.Name = "Second profile"

	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	spy := &phaseSerializedLifecycle{
		entered: make(chan string, 2),
		release: map[string]chan struct{}{
			first.Profile.ID:  firstRelease,
			second.Profile.ID: secondRelease,
		},
	}
	locked := lock.wrapWithProductPhase(spy, tracker)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, err := locked.Connect(ctx, first)
		firstDone <- err
	}()
	select {
	case entered := <-spy.entered:
		if entered != first.Profile.ID {
			t.Fatalf("first admitted profile=%q", entered)
		}
	case <-ctx.Done():
		t.Fatal("first connect was not admitted")
	}

	startSecond := make(chan struct{})
	go func() {
		close(startSecond)
		_, err := locked.Connect(ctx, second)
		secondDone <- err
	}()
	<-startSecond
	deadline := time.Now().Add(time.Second)
	for {
		lock.mutationMu.Lock()
		pending := lock.pendingMutations
		lock.mutationMu.Unlock()
		if pending == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued connect was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	phase := tracker.decorate(api.StatusResponse{Connection: "inactive"})
	if phase.LifecyclePhase != api.LifecycleConnecting || phase.ProfileID != first.Profile.ID {
		t.Fatalf("queued second request hid running first transition: %#v", phase)
	}

	close(firstRelease)
	if err := <-firstDone; err != nil {
		t.Fatalf("first connect: %v", err)
	}
	select {
	case entered := <-spy.entered:
		if entered != second.Profile.ID {
			t.Fatalf("second admitted profile=%q", entered)
		}
	case <-ctx.Done():
		t.Fatal("second connect did not acquire lifecycle lock")
	}
	phase = tracker.decorate(api.StatusResponse{Connection: "inactive"})
	if phase.LifecyclePhase != api.LifecycleConnecting || phase.ProfileID != second.Profile.ID {
		t.Fatalf("first completion erased second operation status: %#v", phase)
	}

	close(secondRelease)
	if err := <-secondDone; err != nil {
		t.Fatalf("second connect: %v", err)
	}
	phase = tracker.decorate(api.StatusResponse{Connection: "inactive"})
	if phase.LifecyclePhase != "" || phase.Connection != "inactive" {
		t.Fatalf("finished operations still published as Connecting: %#v", phase)
	}
}
