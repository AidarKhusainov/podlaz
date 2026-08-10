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

type tunRevalidationCoordinator struct {
	wake chan struct{}
	run  func(context.Context, tunRevalidationTrigger)

	mu             sync.Mutex
	pendingTrigger tunRevalidationTrigger
	activeTrigger  tunRevalidationTrigger
	activeCancel   context.CancelFunc
}

func newTunRevalidationCoordinator(run func(context.Context, tunRevalidationTrigger)) *tunRevalidationCoordinator {
	if run == nil {
		run = func(context.Context, tunRevalidationTrigger) {}
	}
	return &tunRevalidationCoordinator{
		wake: make(chan struct{}, 1),
		run:  run,
	}
}

// Notify is edge-triggered. The one-element wake channel bounds queued work,
// while pendingTrigger merges event semantics under a mutex. Generation-one
// proof dominates all other hints because a just-committed TUN must establish
// its first current-health proof before ordinary fingerprint decisions apply.
// Resume and source resync then dominate ordinary link/address/route hints.
func (c *tunRevalidationCoordinator) Notify(trigger tunRevalidationTrigger) {
	if c == nil || trigger == "" {
		return
	}
	c.mu.Lock()
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
			trigger := c.takePendingTrigger()
			if trigger == "" {
				continue
			}
			probeCtx, cancel := context.WithCancel(ctx)
			c.setActive(trigger, cancel)
			c.run(probeCtx, trigger)
			cancel()
			c.clearActive()
		}
	}
}

// InterruptForMutation publishes cancellation without acquiring lifecycle
// mutation authority and requeues the active trigger before cancelling it. The
// fresh attempt therefore survives connect/disconnect/recovery precedence and
// will run only after the mutation queue becomes idle.
func (c *tunRevalidationCoordinator) InterruptForMutation() {
	if c == nil {
		return
	}
	c.mu.Lock()
	cancel := c.activeCancel
	if cancel != nil && c.activeTrigger != "" {
		c.pendingTrigger = mergeTunRevalidationTrigger(c.pendingTrigger, c.activeTrigger)
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

func (c *tunRevalidationCoordinator) takePendingTrigger() tunRevalidationTrigger {
	c.mu.Lock()
	defer c.mu.Unlock()
	trigger := c.pendingTrigger
	c.pendingTrigger = ""
	return trigger
}

func (c *tunRevalidationCoordinator) setActive(trigger tunRevalidationTrigger, cancel context.CancelFunc) {
	c.mu.Lock()
	c.activeTrigger = trigger
	c.activeCancel = cancel
	c.mu.Unlock()
}

func (c *tunRevalidationCoordinator) clearActive() {
	c.mu.Lock()
	c.activeTrigger = ""
	c.activeCancel = nil
	c.mu.Unlock()
}

func tunSleepSignalTrigger(preparingForSleep bool) (tunRevalidationTrigger, bool) {
	if preparingForSleep {
		return "", false
	}
	return tunRevalidationTriggerResume, true
}
