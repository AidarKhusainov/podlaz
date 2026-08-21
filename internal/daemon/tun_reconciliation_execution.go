package daemon

import (
	"context"
	"errors"
	"sync"
	"time"
)

type tunReconciliationTimer interface {
	Stop() bool
}

type tunReconciliationAfterFunc func(time.Duration, func()) tunReconciliationTimer

type tunReconciliationRetryScheduler struct {
	mu        sync.Mutex
	notify    func(tunRevalidationTrigger)
	after     tunReconciliationAfterFunc
	timer     tunReconciliationTimer
	sessionID string
	epoch     uint64
}

func newTunReconciliationRetryScheduler(notify func(tunRevalidationTrigger)) *tunReconciliationRetryScheduler {
	return newTunReconciliationRetrySchedulerWithTimer(notify, func(delay time.Duration, fn func()) tunReconciliationTimer {
		return time.AfterFunc(delay, fn)
	})
}

func newTunReconciliationRetrySchedulerWithTimer(
	notify func(tunRevalidationTrigger),
	after tunReconciliationAfterFunc,
) *tunReconciliationRetryScheduler {
	if after == nil {
		after = func(delay time.Duration, fn func()) tunReconciliationTimer { return time.AfterFunc(delay, fn) }
	}
	return &tunReconciliationRetryScheduler{notify: notify, after: after}
}

func (s *tunReconciliationRetryScheduler) Apply(decision tunReconciliationDecision) {
	if s == nil {
		return
	}
	switch decision.Kind {
	case tunDecisionRetry:
		s.Schedule(decision.NetworkSessionID, decision.RetryAfter)
	case tunDecisionVerified, tunDecisionReconcile, tunDecisionBlockedOwnership, tunDecisionTerminal:
		s.Cancel()
	case tunDecisionSuperseded:
		// A newer publication already owns the next decision. Do not let a stale
		// completion cancel or replace its timer.
	}
}

func (s *tunReconciliationRetryScheduler) Schedule(sessionID string, delay time.Duration) {
	if s == nil || sessionID == "" {
		return
	}
	if delay <= 0 {
		delay = defaultTunReconciliationRetry
	}
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.epoch++
	epoch := s.epoch
	s.sessionID = sessionID
	s.timer = s.after(delay, func() {
		s.mu.Lock()
		if s.epoch != epoch || s.sessionID != sessionID {
			s.mu.Unlock()
			return
		}
		s.timer = nil
		s.sessionID = ""
		notify := s.notify
		s.mu.Unlock()
		if notify != nil {
			notify(tunRevalidationTriggerSourceResync)
		}
	})
	s.mu.Unlock()
}

func (s *tunReconciliationRetryScheduler) Cancel() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.epoch++
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = nil
	s.sessionID = ""
	s.mu.Unlock()
}

type tunAutomaticDispositionExecutor struct {
	reconcile func(context.Context, string) error
	terminal  tunAutomaticTerminalHandler
	retry     *tunReconciliationRetryScheduler
}

func (e tunAutomaticDispositionExecutor) Handle(
	ctx context.Context,
	admission *lifecycleAutomaticAdmission,
	disposition tunAutomaticDisposition,
) {
	if admission == nil {
		return
	}
	switch disposition.Kind {
	case tunDecisionTerminal:
		if e.retry != nil {
			e.retry.Cancel()
		}
		disposition.Cause = reconciliationTerminalFallbackCause(disposition)
		e.terminal.Handle(ctx, admission, disposition)
	case tunDecisionReconcile:
		if e.retry != nil {
			e.retry.Cancel()
		}
		var err error
		if e.reconcile == nil {
			err = errors.New("missing admitted protected TUN reconciliation lifecycle")
		} else {
			err = e.reconcile(ctx, disposition.NetworkSessionID)
		}
		// ReconcileProtectedTun runs under the token already owned by admission.
		// Return that authority before any timer can notify the coordinator.
		admission.Release()
		if err != nil && e.retry != nil {
			e.retry.Schedule(disposition.NetworkSessionID, defaultTunReconciliationRetry)
		}
	default:
		admission.Release()
	}
}
