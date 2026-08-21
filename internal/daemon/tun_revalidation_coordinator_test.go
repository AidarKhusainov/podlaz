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
			t.Fatalf("coalesced trigger=%q, want %q", trigger, tunRevalidationTriggerResume)
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

func TestAutomaticDispositionNewerPublicationSupersedesReconcileBeforeAdmission(t *testing.T) {
	lock := newLifecycleOperationLock()
	expectedGeneration := lock.lifecycleMutationSnapshot().generation
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	var runs atomic.Int32
	var admitCalls atomic.Int32
	handled := make(chan struct{}, 1)

	coordinator := newTunAutomaticDispositionCoordinator(
		func(ctx context.Context, _ tunRevalidationTrigger) tunAutomaticDisposition {
			if runs.Add(1) == 1 {
				close(firstStarted)
				select {
				case <-ctx.Done():
					return tunAutomaticDisposition{}
				case <-releaseFirst:
				}
				return tunAutomaticDisposition{
					Kind:                       tunDecisionReconcile,
					ExpectedMutationGeneration: expectedGeneration,
					NetworkSessionID:           "session-a",
				}
			}
			close(secondStarted)
			return tunAutomaticDisposition{}
		},
		func(expected uint64) (*lifecycleAutomaticAdmission, bool) {
			admitCalls.Add(1)
			return lock.tryAdmitAutomaticMutation(expected)
		},
		func(_ context.Context, admission *lifecycleAutomaticAdmission, _ tunAutomaticDisposition) {
			admission.Release()
			handled <- struct{}{}
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)
	coordinator.Notify(tunRevalidationTriggerResume)
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first automatic-disposition round did not start")
	}

	coordinator.Notify(tunRevalidationTriggerRoute)
	close(releaseFirst)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("newer publication was not consumed after stale reconcile")
	}
	if got := admitCalls.Load(); got != 0 {
		t.Fatalf("stale reconcile attempted automatic admission %d times, want 0", got)
	}
	select {
	case <-handled:
		t.Fatal("stale reconcile reached automatic handler")
	default:
	}
}

func TestStaleTerminalMutationGenerationIsSupersededBeforeAdmission(t *testing.T) {
	lock := newLifecycleOperationLock()
	expectedGeneration := lock.lifecycleMutationSnapshot().generation
	runStarted := make(chan struct{})
	releaseRun := make(chan struct{})
	admitAttempted := make(chan struct{})
	handled := make(chan struct{}, 1)

	coordinator := newTunAutomaticDispositionCoordinator(
		func(ctx context.Context, _ tunRevalidationTrigger) tunAutomaticDisposition {
			close(runStarted)
			select {
			case <-ctx.Done():
				return tunAutomaticDisposition{}
			case <-releaseRun:
				return tunAutomaticDisposition{
					Kind:                       tunDecisionTerminal,
					ExpectedMutationGeneration: expectedGeneration,
					NetworkSessionID:           "session-a",
				}
			}
		},
		func(expected uint64) (*lifecycleAutomaticAdmission, bool) {
			close(admitAttempted)
			return lock.tryAdmitAutomaticMutation(expected)
		},
		func(_ context.Context, admission *lifecycleAutomaticAdmission, _ tunAutomaticDisposition) {
			admission.Release()
			handled <- struct{}{}
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)
	coordinator.Notify(tunRevalidationTriggerResume)
	select {
	case <-runStarted:
	case <-time.After(time.Second):
		t.Fatal("terminal round did not start")
	}

	finishExplicit, err := lock.beginExternalMutation()
	if err != nil {
		t.Fatalf("declare explicit mutation: %v", err)
	}
	defer finishExplicit()
	close(releaseRun)

	select {
	case <-admitAttempted:
	case <-time.After(time.Second):
		t.Fatal("terminal disposition did not reach lifecycle admission check")
	}
	select {
	case <-handled:
		t.Fatal("stale terminal reached automatic handler after lifecycle generation changed")
	default:
	}
}

func TestAutomaticDispositionPublicationClaimAndLifecycleAdmissionAreAtomic(t *testing.T) {
	lock := newLifecycleOperationLock()
	expectedGeneration := lock.lifecycleMutationSnapshot().generation
	admitEntered := make(chan struct{})
	releaseAdmit := make(chan struct{})
	handled := make(chan tunAutomaticDisposition, 1)
	var runs atomic.Int32

	coordinator := newTunAutomaticDispositionCoordinator(
		func(context.Context, tunRevalidationTrigger) tunAutomaticDisposition {
			if runs.Add(1) != 1 {
				return tunAutomaticDisposition{}
			}
			return tunAutomaticDisposition{
				Kind:                       tunDecisionTerminal,
				ExpectedMutationGeneration: expectedGeneration,
				NetworkSessionID:           "session-a",
			}
		},
		func(expected uint64) (*lifecycleAutomaticAdmission, bool) {
			close(admitEntered)
			<-releaseAdmit
			return lock.tryAdmitAutomaticMutation(expected)
		},
		func(_ context.Context, admission *lifecycleAutomaticAdmission, disposition tunAutomaticDisposition) {
			handled <- disposition
			admission.Release()
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)
	coordinator.Notify(tunRevalidationTriggerResume)
	select {
	case <-admitEntered:
	case <-time.After(time.Second):
		t.Fatal("automatic admission did not start")
	}

	notifyStarted := make(chan struct{})
	notifyDone := make(chan struct{})
	go func() {
		close(notifyStarted)
		coordinator.Notify(tunRevalidationTriggerRoute)
		close(notifyDone)
	}()
	<-notifyStarted
	select {
	case <-notifyDone:
		t.Fatal("new publication advanced while coordinator was between publication claim and lifecycle admission")
	default:
	}

	close(releaseAdmit)
	var disposition tunAutomaticDisposition
	select {
	case disposition = <-handled:
	case <-time.After(time.Second):
		t.Fatal("admitted automatic disposition did not reach handler")
	}
	if disposition.PublicationRevision == 0 {
		t.Fatal("coordinator did not stamp automatic disposition with publication revision")
	}
	select {
	case <-notifyDone:
	case <-time.After(time.Second):
		t.Fatal("new publication remained blocked after automatic admission completed")
	}
}
