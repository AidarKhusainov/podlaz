package daemon

import (
	"context"
	"sync"
)

type tunRevalidationTrigger string

const (
	tunRevalidationTriggerResume  tunRevalidationTrigger = "resume"
	tunRevalidationTriggerLink    tunRevalidationTrigger = "link"
	tunRevalidationTriggerAddress tunRevalidationTrigger = "address"
	tunRevalidationTriggerRoute   tunRevalidationTrigger = "route"
)

type tunRevalidationCoordinator struct {
	wake chan struct{}
	run  func(context.Context, tunRevalidationTrigger)

	mu             sync.Mutex
	pendingTrigger tunRevalidationTrigger
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
// while pendingTrigger merges event semantics under a mutex. Resume dominates
// ordinary link/address/route hints because suspend invalidates proof freshness
// even when the resulting uplink fingerprint is unchanged.
func (c *tunRevalidationCoordinator) Notify(trigger tunRevalidationTrigger) {
	if c == nil || trigger == "" {
		return
	}
	c.mu.Lock()
	c.pendingTrigger = mergeTunRevalidationTrigger(c.pendingTrigger, trigger)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func mergeTunRevalidationTrigger(current, next tunRevalidationTrigger) tunRevalidationTrigger {
	if current == tunRevalidationTriggerResume || next == tunRevalidationTriggerResume {
		return tunRevalidationTriggerResume
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
			c.setActiveCancel(cancel)
			c.run(probeCtx, trigger)
			cancel()
			c.clearActiveCancel()
		}
	}
}

// CancelActive never acquires the lifecycle mutation lock. Disconnect and
// daemon shutdown can therefore interrupt long-running probes before waiting
// for the serialized mutation boundary.
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

func (c *tunRevalidationCoordinator) setActiveCancel(cancel context.CancelFunc) {
	c.mu.Lock()
	c.activeCancel = cancel
	c.mu.Unlock()
}

func (c *tunRevalidationCoordinator) clearActiveCancel() {
	c.mu.Lock()
	c.activeCancel = nil
	c.mu.Unlock()
}

func tunSleepSignalTrigger(preparingForSleep bool) (tunRevalidationTrigger, bool) {
	if preparingForSleep {
		return "", false
	}
	return tunRevalidationTriggerResume, true
}
