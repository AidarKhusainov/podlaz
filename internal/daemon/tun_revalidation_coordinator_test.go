package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTunRevalidationCoordinatorCoalescesDuplicateEventStorm(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	coordinator := newTunRevalidationCoordinator(func(ctx context.Context, _ tunRevalidationTrigger) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-ctx.Done():
		case <-release:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)

	coordinator.Notify(tunRevalidationTriggerRoute)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first revalidation did not start")
	}
	for i := 0; i < 100; i++ {
		coordinator.Notify(tunRevalidationTriggerRoute)
		coordinator.Notify(tunRevalidationTriggerAddress)
		coordinator.Notify(tunRevalidationTriggerLink)
	}
	close(release)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("coalesced follow-up revalidation did not start")
	}
	time.Sleep(25 * time.Millisecond)
	if got := calls.Load(); got != 2 {
		t.Fatalf("duplicate event storm started %d revalidations, want 2", got)
	}
}

func TestTunRevalidationCoordinatorPreservesResumeAcrossCoalescing(t *testing.T) {
	started := make(chan tunRevalidationTrigger, 2)
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	coordinator := newTunRevalidationCoordinator(func(ctx context.Context, trigger tunRevalidationTrigger) {
		call := calls.Add(1)
		started <- trigger
		if call == 1 {
			select {
			case <-ctx.Done():
			case <-releaseFirst:
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)
	coordinator.Notify(tunRevalidationTriggerLink)

	select {
	case trigger := <-started:
		if trigger != tunRevalidationTriggerLink {
			t.Fatalf("first trigger=%q, want %q", trigger, tunRevalidationTriggerLink)
		}
	case <-time.After(time.Second):
		t.Fatal("first revalidation did not start")
	}

	coordinator.Notify(tunRevalidationTriggerRoute)
	coordinator.Notify(tunRevalidationTriggerResume)
	close(releaseFirst)

	select {
	case trigger := <-started:
		if trigger != tunRevalidationTriggerResume {
			t.Fatalf("coalesced trigger=%q, want resume to dominate", trigger)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced follow-up revalidation did not start")
	}
}

func TestTunRevalidationCoordinatorCancellationDoesNotNeedLifecycleLock(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	coordinator := newTunRevalidationCoordinator(func(ctx context.Context, _ tunRevalidationTrigger) {
		close(started)
		<-ctx.Done()
		close(cancelled)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)
	coordinator.Notify(tunRevalidationTriggerResume)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("revalidation did not start")
	}
	coordinator.CancelActive()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("active revalidation did not observe cancellation")
	}
}

func TestTunRevalidationCoordinatorResumeSignalOnlyTriggersAfterWake(t *testing.T) {
	if trigger, ok := tunSleepSignalTrigger(true); ok || trigger != "" {
		t.Fatalf("PrepareForSleep(true) unexpectedly triggered revalidation: %q", trigger)
	}
	trigger, ok := tunSleepSignalTrigger(false)
	if !ok || trigger != tunRevalidationTriggerResume {
		t.Fatalf("PrepareForSleep(false) trigger = %q, %v; want %q, true", trigger, ok, tunRevalidationTriggerResume)
	}
}
