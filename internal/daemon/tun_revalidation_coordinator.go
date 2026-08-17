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
	wake     chan struct{}
	run      tunRevalidationRunFunc
	terminal tunRevalidationTerminalFunc

	mu                  sync.Mutex
	publicationRevision uint64
	pendingTrigger      tunRevalidationTrigger
	pendingRevision     uint64
	activeTrigger       tunRevalidationTrigger
	activeRevision      uint64
	activeCancel        context.CancelFunc
}

func newTunRevalidationCoordinator(run func(context.Context, tunRevalidationTrigger)) *tunRevalidationCoordinator {
	return newTunRevalidationOutcomeCoordinator(func(ctx context.Context, trigger tunRevalidationTrigger) tunRevalidationOutcome {
		if run != nil {
			run(ctx, trigger)
		}
		return tunRevalidationOutcome{}
	}, nil)
}

func newTunRevalidationOutcomeCoordinator(run tunRevalidationRunFunc, terminal tunRevalidationTerminalFunc) *tunRevalidationCoordinator {
	if run == nil {
		run = func(context.Context, tunRevalidationTrigger) tunRevalidationOutcome { return tunRevalidationOutcome{} }
	}
	return &tunRevalidationCoordinator{
		wake:     make(chan struct{}, 1),
		run:      run,
		terminal: terminal,
	}
}

// Notify is edge-triggered. The one-element wake channel bounds queued work,
// while pendingTrigger merges event semantics under a mutex. Every accepted
// hint also advances a private publication revision. An older in-flight proof
// may finish, but it cannot become authoritative after this point.
//
// Generation-one proof dominates all other hints because a just-committed TUN
// must establish its first current-health proof before ordinary fingerprint
// decisions apply. Resume and source resync then dominate ordinary
// link/address/route hints.
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
			outcome := c.run(probeCtx, trigger)
			cancel()
			c.clearActive()
			if c.terminal != nil && outcome.needsLifecycleCleanup() && c.claimTerminalPublication(revision) {
				c.terminal(ctx, outcome)
			}
		}
	}
}

// InterruptForMutation publishes cancellation without acquiring lifecycle
// mutation authority and requeues the active trigger before cancelling it. The
// fresh attempt therefore survives connect/disconnect/recovery precedence and
// will run only after the mutation queue becomes idle. Requeueing is not new
// network evidence, so it preserves the active publication revision.
func (c *tunRevalidationCoordinator) InterruptForMutation() {
	if c == nil {
		return
	}
	c.mu.Lock()
	cancel := c.activeCancel
	if cancel != nil && c.activeTrigger != "" {
		c.pendingTrigger = mergeTunRevalidationTrigger(c.pendingTrigger, c.activeTrigger)
		if c.activeRevision > c.pendingRevision {
			c.pendingRevision = c.activeRevision
		}
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		c.signalWake()
	}
}

// CancelActive is for terminal cancellation such as daemon shutdown. Unlike a
// lifecycle mutation it intentionally does not requeue the current trigger.
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
