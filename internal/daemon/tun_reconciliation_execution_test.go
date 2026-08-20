package daemon

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeTunReconciliationTimer struct {
	fn      func()
	stopped bool
}

func (t *fakeTunReconciliationTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func TestRetryTimerCoalescesAndSuppressesStaleTimer(t *testing.T) {
	var timers []*fakeTunReconciliationTimer
	var notifications int
	scheduler := newTunReconciliationRetrySchedulerWithTimer(func(trigger tunRevalidationTrigger) {
		if trigger != tunRevalidationTriggerSourceResync {
			t.Fatalf("trigger = %q, want source-resync", trigger)
		}
		notifications++
	}, func(_ time.Duration, fn func()) tunReconciliationTimer {
		timer := &fakeTunReconciliationTimer{fn: fn}
		timers = append(timers, timer)
		return timer
	})

	scheduler.Schedule("session-a", time.Second)
	scheduler.Schedule("session-a", time.Second)
	if len(timers) != 2 {
		t.Fatalf("timers = %d, want 2", len(timers))
	}
	if !timers[0].stopped {
		t.Fatal("replaced retry timer was not stopped")
	}

	timers[0].fn()
	if notifications != 0 {
		t.Fatalf("stale timer notifications = %d, want 0", notifications)
	}
	timers[1].fn()
	if notifications != 1 {
		t.Fatalf("current timer notifications = %d, want 1", notifications)
	}
}

func TestVerifiedOrTerminalCycleCancelsRetryTimer(t *testing.T) {
	var timer *fakeTunReconciliationTimer
	scheduler := newTunReconciliationRetrySchedulerWithTimer(nil, func(_ time.Duration, fn func()) tunReconciliationTimer {
		timer = &fakeTunReconciliationTimer{fn: fn}
		return timer
	})
	scheduler.Apply(tunReconciliationDecision{
		Kind:             tunDecisionRetry,
		RetryAfter:       time.Second,
		NetworkSessionID: "session-a",
	})
	if timer == nil {
		t.Fatal("retry timer was not scheduled")
	}
	scheduler.Apply(tunReconciliationDecision{Kind: tunDecisionVerified, NetworkSessionID: "session-a"})
	if !timer.stopped {
		t.Fatal("verified decision did not cancel retry timer")
	}
}

func TestAdmittedReconcileUsesOwnedTokenAndReleasesIt(t *testing.T) {
	lock := newLifecycleOperationLock()
	snapshot := lock.lifecycleMutationSnapshot()
	admission, ok := lock.tryAdmitAutomaticMutation(snapshot.generation)
	if !ok || admission == nil {
		t.Fatal("automatic reconcile admission failed")
	}

	var reconciled string
	executor := tunAutomaticDispositionExecutor{
		reconcile: func(_ context.Context, sessionID string) error {
			reconciled = sessionID
			return nil
		},
	}
	executor.Handle(context.Background(), admission, tunAutomaticDisposition{
		Kind:             tunDecisionReconcile,
		NetworkSessionID: "session-a",
	})
	if reconciled != "session-a" {
		t.Fatalf("reconciled session = %q, want session-a", reconciled)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := lock.acquire(ctx); err != nil {
		t.Fatalf("operation token was not released: %v", err)
	}
	lock.release()
}

func TestReconcileFailureSchedulesFreshBoundedObservation(t *testing.T) {
	lock := newLifecycleOperationLock()
	admission, ok := lock.tryAdmitAutomaticMutation(lock.lifecycleMutationSnapshot().generation)
	if !ok {
		t.Fatal("automatic reconcile admission failed")
	}

	var timer *fakeTunReconciliationTimer
	var notifications int
	scheduler := newTunReconciliationRetrySchedulerWithTimer(func(tunRevalidationTrigger) {
		notifications++
	}, func(_ time.Duration, fn func()) tunReconciliationTimer {
		timer = &fakeTunReconciliationTimer{fn: fn}
		return timer
	})
	executor := tunAutomaticDispositionExecutor{
		reconcile: func(context.Context, string) error { return errors.New("controlled rebuild failure") },
		retry:     scheduler,
	}
	executor.Handle(context.Background(), admission, tunAutomaticDisposition{
		Kind:             tunDecisionReconcile,
		NetworkSessionID: "session-a",
	})
	if timer == nil {
		t.Fatal("failed reconcile did not schedule fresh observation")
	}
	if notifications != 0 {
		t.Fatalf("notifications before timer fire = %d, want 0", notifications)
	}
	timer.fn()
	if notifications != 1 {
		t.Fatalf("notifications after timer fire = %d, want 1", notifications)
	}
}
