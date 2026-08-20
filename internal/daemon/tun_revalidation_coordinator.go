package daemon

import (
	"context"
	"sync"
)

type tunRevalidationTrigger string

const (
	tunRevalidationTriggerInitial      tunRevalidationTrigger = "initial"
	tunRevalidationTriggerResume       tunRevalidationTrigger = "resume"
	tunRevalidationTriggerSourceResync tunRevalidationTrigger = "source-resync"
	tunRevalidationTriggerLink         tunRevalidationTrigger = "link"
	tunRevalidationTriggerAddress      tunRevalidationTrigger = "address"
	tunRevalidationTriggerRoute        tunRevalidationTrigger = "route"
)

type tunRevalidationRunFunc func(context.Context, tunRevalidationTrigger) tunRevalidationOutcome
type tunRevalidationTerminalFunc func(context.Context, tunRevalidationOutcome)
type tunAutomaticDispositionRunFunc func(context.Context, tunRevalidationTrigger) tunAutomaticDisposition
type tunAutomaticMutationAdmitFunc func(uint64) (*lifecycleAutomaticAdmission, bool)
type tunAutomaticDispositionHandleFunc func(context.Context, *lifecycleAutomaticAdmission, tunAutomaticDisposition)

type tunRevalidationCoordinatorRunResult struct {
	legacyOutcome tunRevalidationOutcome
	disposition   tunAutomaticDisposition
	automatic     bool
}

type tunRevalidationCoordinatorRunFunc func(context.Context, tunRevalidationTrigger) tunRevalidationCoordinatorRunResult

type tunRevalidationPublicationContextKey struct{}

type tunRevalidationPublicationToken struct {
	revision uint64
	current  func() uint64
}

func tunRevalidationPublicationTokenFromContext(ctx context.Context) (tunRevalidationPublicationToken, bool) {
	if ctx == nil {
		return tunRevalidationPublicationToken{}, false
	}
	token, ok := ctx.Value(tunRevalidationPublicationContextKey{}).(tunRevalidationPublicationToken)
	if !ok || token.current == nil {
		return tunRevalidationPublicationToken{}, false
	}
	return token, true
}

func (t tunRevalidationPublicationToken) isCurrent() bool {
	return t.current == nil || t.current() == t.revision
}

type tunRevalidationCoordinator struct {
	wake chan struct{}
	run  tunRevalidationCoordinatorRunFunc

	// terminal is the pre-#262 compatibility path. Production moves to the
	// unified automatic handoff before this field is removed.
	terminal tunRevalidationTerminalFunc

	admitAutomatic  tunAutomaticMutationAdmitFunc
	handleAutomatic tunAutomaticDispositionHandleFunc

	mu                  sync.Mutex
	publicationRevision uint64
	pendingTrigger      tunRevalidationTrigger
	pendingRevision     uint64
	activeTrigger       tunRevalidationTrigger
	activeRevision      uint64
	activeCancel        context.CancelFunc
}

func newTunRevalidationCoordinator(run func(context.Context, tunRevalidationTrigger)) *tunRevalidationCoordinator {
	return newTunRevalidationCoordinatorWithRun(func(ctx context.Context, trigger tunRevalidationTrigger) tunRevalidationCoordinatorRunResult {
		if run != nil {
			run(ctx, trigger)
		}
		return tunRevalidationCoordinatorRunResult{}
	})
}

func newTunRevalidationOutcomeCoordinator(run tunRevalidationRunFunc, terminal tunRevalidationTerminalFunc) *tunRevalidationCoordinator {
	if run == nil {
		run = func(context.Context, tunRevalidationTrigger) tunRevalidationOutcome { return tunRevalidationOutcome{} }
	}
	coordinator := newTunRevalidationCoordinatorWithRun(func(ctx context.Context, trigger tunRevalidationTrigger) tunRevalidationCoordinatorRunResult {
		return tunRevalidationCoordinatorRunResult{legacyOutcome: run(ctx, trigger)}
	})
	coordinator.terminal = terminal
	return coordinator
}

func newTunAutomaticDispositionCoordinator(
	run tunAutomaticDispositionRunFunc,
	admit tunAutomaticMutationAdmitFunc,
	handle tunAutomaticDispositionHandleFunc,
) *tunRevalidationCoordinator {
	if run == nil {
		run = func(context.Context, tunRevalidationTrigger) tunAutomaticDisposition {
			return tunAutomaticDisposition{}
		}
	}
	coordinator := newTunRevalidationCoordinatorWithRun(func(ctx context.Context, trigger tunRevalidationTrigger) tunRevalidationCoordinatorRunResult {
		return tunRevalidationCoordinatorRunResult{
			disposition: run(ctx, trigger),
			automatic:   true,
		}
	})
	coordinator.admitAutomatic = admit
	coordinator.handleAutomatic = handle
	return coordinator
}

func newTunRevalidationCoordinatorWithRun(run tunRevalidationCoordinatorRunFunc) *tunRevalidationCoordinator {
	if run == nil {
		run = func(context.Context, tunRevalidationTrigger) tunRevalidationCoordinatorRunResult {
			return tunRevalidationCoordinatorRunResult{}
		}
	}
	return &tunRevalidationCoordinator{
		wake: make(chan struct{}, 1),
		run:  run,
	}
}

func (c *tunRevalidationCoordinator) Notify(trigger tunRevalidationTrigger) {
	if c == nil || trigger == "" {
		return
	}
	c.mu.Lock()
	c.publicationRevision = nextTunCoordinatorRevision(c.publicationRevision)
	c.pendingRevision = c.publicationRevision
	c.pendingTrigger = mergeTunRevalidationTrigger(c.pendingTrigger, trigger)
	c.mu.Unlock()
	c.signalWake()
}

func (c *tunRevalidationCoordinator) signalWake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func mergeTunRevalidationTrigger(current, next tunRevalidationTrigger) tunRevalidationTrigger {
	if current == tunRevalidationTriggerInitial || next == tunRevalidationTriggerInitial {
		return tunRevalidationTriggerInitial
	}
	if current == tunRevalidationTriggerResume || next == tunRevalidationTriggerResume {
		return tunRevalidationTriggerResume
	}
	if current == tunRevalidationTriggerSourceResync || next == tunRevalidationTriggerSourceResync {
		return tunRevalidationTriggerSourceResync
	}
	if current != "" {
		return current
	}
	return next
}

func (c *tunRevalidationCoordinator) Run(ctx context.Context) {
	if c == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			c.CancelActive()
			return
		case <-c.wake:
			if ctx.Err() != nil {
				c.CancelActive()
				return
			}
			trigger, revision := c.takePendingWork()
			if trigger == "" {
				continue
			}
			probeCtx, cancel := context.WithCancel(ctx)
			probeCtx = context.WithValue(probeCtx, tunRevalidationPublicationContextKey{}, tunRevalidationPublicationToken{
				revision: revision,
				current:  c.currentPublicationRevision,
			})
			c.setActive(trigger, revision, cancel)
			result := c.run(probeCtx, trigger)
			cancel()

			if result.automatic {
				if !result.disposition.needsAutomaticMutation() || ctx.Err() != nil {
					c.clearActive()
					continue
				}
				admission, disposition, ok := c.claimAndAdmitAutomaticDisposition(revision, result.disposition)
				c.clearActive()
				if !ok {
					continue
				}
				if c.handleAutomatic == nil {
					admission.Release()
					continue
				}
				c.handleAutomatic(ctx, admission, disposition)
				continue
			}

			c.clearActive()
			outcome := result.legacyOutcome
			if c.terminal != nil && outcome.needsLifecycleCleanup() && c.claimTerminalPublication(revision) {
				c.terminal(ctx, outcome)
			}
		}
	}
}

func (d tunAutomaticDisposition) needsAutomaticMutation() bool {
	return d.Kind == tunDecisionReconcile || d.Kind == tunDecisionTerminal
}

func (c *tunRevalidationCoordinator) claimAndAdmitAutomaticDisposition(
	revision uint64,
	disposition tunAutomaticDisposition,
) (*lifecycleAutomaticAdmission, tunAutomaticDisposition, bool) {
	if c == nil || c.admitAutomatic == nil {
		return nil, disposition, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.publicationRevision != revision {
		return nil, disposition, false
	}
	if disposition.PublicationRevision != 0 && disposition.PublicationRevision != revision {
		return nil, disposition, false
	}
	disposition.PublicationRevision = revision
	admission, ok := c.admitAutomatic(disposition.ExpectedMutationGeneration)
	if !ok || admission == nil {
		// Admission can lose to an explicit lifecycle mutation after observation
		// completed. Preserve the already-published reconciliation work while the
		// publication/admission mutex is still held; InterruptForMutation will
		// deliver the wake after the explicit mutation releases its token.
		c.requeueActiveLocked()
		return nil, disposition, false
	}
	return admission, disposition, true
}

func (c *tunRevalidationCoordinator) requeueActiveLocked() {
	if c.activeTrigger == "" {
		return
	}
	c.pendingTrigger = mergeTunRevalidationTrigger(c.pendingTrigger, c.activeTrigger)
	if c.activeRevision > c.pendingRevision {
		c.pendingRevision = c.activeRevision
	}
}

func (c *tunRevalidationCoordinator) InterruptForMutation() {
	if c == nil {
		return
	}
	c.mu.Lock()
	cancel := c.activeCancel
	c.requeueActiveLocked()
	hasPending := c.pendingTrigger != ""
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cancel != nil || hasPending {
		c.signalWake()
	}
}

func (c *tunRevalidationCoordinator) CancelActive() {
	if c == nil {
		return
	}
	c.mu.Lock()
	cancel := c.activeCancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *tunRevalidationCoordinator) takePendingWork() (tunRevalidationTrigger, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	trigger := c.pendingTrigger
	revision := c.pendingRevision
	c.pendingTrigger = ""
	c.pendingRevision = 0
	return trigger, revision
}

func (c *tunRevalidationCoordinator) takePendingTrigger() tunRevalidationTrigger {
	trigger, _ := c.takePendingWork()
	return trigger
}

func (c *tunRevalidationCoordinator) setActive(trigger tunRevalidationTrigger, revision uint64, cancel context.CancelFunc) {
	c.mu.Lock()
	c.activeTrigger = trigger
	c.activeRevision = revision
	c.activeCancel = cancel
	c.mu.Unlock()
}

func (c *tunRevalidationCoordinator) clearActive() {
	c.mu.Lock()
	c.activeTrigger = ""
	c.activeRevision = 0
	c.activeCancel = nil
	c.mu.Unlock()
}

func (c *tunRevalidationCoordinator) currentPublicationRevision() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publicationRevision
}

func (c *tunRevalidationCoordinator) claimTerminalPublication(revision uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publicationRevision == revision
}

func nextTunCoordinatorRevision(current uint64) uint64 {
	current++
	if current == 0 {
		return 1
	}
	return current
}

func tunSleepSignalTrigger(preparingForSleep bool) (tunRevalidationTrigger, bool) {
	if preparingForSleep {
		return "", false
	}
	return tunRevalidationTriggerResume, true
}
