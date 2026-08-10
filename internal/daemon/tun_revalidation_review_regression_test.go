package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type blockingReviewLifecycle struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingReviewLifecycle) Connect(ctx context.Context, _ api.ConnectRequest) (api.LifecycleResponse, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return api.LifecycleResponse{}, ctx.Err()
	case <-s.release:
		return api.LifecycleResponse{Connection: "active"}, nil
	}
}

func (s *blockingReviewLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive"}, nil
}

func TestTunRevalidationEventObservedDuringLifecycleMutationRunsAfterMutation(t *testing.T) {
	lock := newLifecycleOperationLock()
	lifecycle := &blockingReviewLifecycle{started: make(chan struct{}), release: make(chan struct{})}
	locked := lock.wrap(lifecycle)

	mutationDone := make(chan error, 1)
	go func() {
		_, err := locked.Connect(context.Background(), api.ConnectRequest{})
		mutationDone <- err
	}()
	select {
	case <-lifecycle.started:
	case <-time.After(time.Second):
		t.Fatal("lifecycle mutation did not start")
	}

	attempted := make(chan struct{}, 1)
	ran := make(chan struct{}, 1)
	coordinator := newTunRevalidationCoordinator(func(ctx context.Context, _ tunRevalidationTrigger) {
		attempted <- struct{}{}
		if err := lock.runRevalidation(ctx, func() { ran <- struct{}{} }); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("run revalidation: %v", err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)

	coordinator.Notify(tunRevalidationTriggerRoute)
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("coordinator did not consume event during mutation")
	}
	select {
	case <-ran:
		t.Fatal("revalidation ran concurrently with lifecycle mutation")
	default:
	}

	close(lifecycle.release)
	select {
	case err := <-mutationDone:
		if err != nil {
			t.Fatalf("lifecycle mutation failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lifecycle mutation did not finish")
	}
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("event consumed during mutation was not revalidated after mutation")
	}
}

func TestTunRevalidationInterruptedByLifecycleMutationIsRequeued(t *testing.T) {
	lock := newLifecycleOperationLock()
	lifecycle := &blockingReviewLifecycle{started: make(chan struct{}), release: make(chan struct{})}
	locked := lock.wrap(lifecycle)

	firstStarted := make(chan struct{})
	secondRan := make(chan struct{})
	var runs atomic.Int32
	coordinator := newTunRevalidationCoordinator(func(ctx context.Context, _ tunRevalidationTrigger) {
		_ = lock.runRevalidation(ctx, func() {
			if runs.Add(1) == 1 {
				close(firstStarted)
				<-ctx.Done()
				return
			}
			close(secondRan)
		})
	})
	lock.setRevalidationCancel(coordinator.InterruptForMutation)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)

	coordinator.Notify(tunRevalidationTriggerRoute)
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first revalidation did not start")
	}

	if _, err := locked.Disconnect(context.Background()); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	select {
	case <-secondRan:
	case <-time.After(time.Second):
		t.Fatal("revalidation interrupted by lifecycle mutation was not requeued")
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("revalidation runs=%d, want exactly 2", got)
	}
}

func TestTunRevalidationInitializeVerifiesCapturedObservationBeforePublishingVerified(t *testing.T) {
	observation := tunRevalidationObservation{
		fingerprint: tunUplinkFingerprint{
			Interface:      "wlan0",
			InterfaceIndex: 3,
			Gateway:        "192.0.2.1",
			Addresses:      "192.0.2.55/24",
		},
	}
	var verifyCalls atomic.Int32
	var verifiedObservation tunRevalidationObservation
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) { return observation, nil },
		func(_ context.Context, got tunRevalidationObservation) error {
			verifiedObservation = got
			verifyCalls.Add(1)
			return nil
		},
	)

	runtime.Initialize(context.Background())
	if got := verifyCalls.Load(); got != 1 {
		t.Fatalf("initialize verification calls=%d, want 1", got)
	}
	if verifiedObservation.fingerprint != observation.fingerprint {
		t.Fatalf("initialize verified fingerprint=%#v, want exact captured observation %#v", verifiedObservation.fingerprint, observation.fingerprint)
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 1, "")
}

func TestTunRevalidationInitializeFailsClosedWhenCapturedObservationVerificationFails(t *testing.T) {
	observation := tunRevalidationObservation{
		fingerprint: tunUplinkFingerprint{
			Interface:      "wlan0",
			InterfaceIndex: 3,
			Gateway:        "192.0.2.1",
			Addresses:      "192.0.2.55/24",
		},
	}
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) { return observation, nil },
		func(context.Context, tunRevalidationObservation) error {
			return newTunRevalidationVerificationError(api.TunHealthOwnedStateInvalid, errors.New("owned state changed after commit"))
		},
	)

	runtime.Initialize(context.Background())
	assertTunHealth(t, runtime.Health(), api.TunHealthDegraded, 1, api.TunHealthOwnedStateInvalid)
}
