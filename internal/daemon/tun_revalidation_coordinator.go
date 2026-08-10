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
	pending chan tunRevalidationTrigger
	run     func(context.Context, tunRevalidationTrigger)

	mu           sync.Mutex
	activeCancel context.CancelFunc
}

func newTunRevalidationCoordinator(run func(context.Context, tunRevalidationTrigger)) *tunRevalidationCoordinator {
	if run == nil {
		run = func(context.Context, tunRevalidationTrigger) {}
	}
	return &tunRevalidationCoordinator{
		pending: make(chan tunRevalidationTrigger, 1),
		run:     run,
	}
}

// Notify is edge-triggered. A one-element queue deliberately collapses event
// storms because events are only hints to collect a fresh authoritative
// fingerprint; the event payload itself is never treated as state.
func (c *tunRevalidationCoordinator) Notify(trigger tunRevalidationTrigger) {
	if c == nil || trigger == "" {
		return
	}
	select {
	case c.pending <- trigger:
	default:
	}
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
		case trigger := <-c.pending:
			probeCtx, cancel := context.WithCancel(ctx)
			c.setActiveCancel(cancel)
			c.run(probeCtx, trigger)
			cancel()
			c.clearActiveCancel(cancel)
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

func (c *tunRevalidationCoordinator) setActiveCancel(cancel context.CancelFunc) {
	c.mu.Lock()
	c.activeCancel = cancel
	c.mu.Unlock()
}

func (c *tunRevalidationCoordinator) clearActiveCancel(cancel context.CancelFunc) {
	c.mu.Lock()
	if c.activeCancel != nil {
		c.activeCancel = nil
	}
	c.mu.Unlock()
}

func tunSleepSignalTrigger(preparingForSleep bool) (tunRevalidationTrigger, bool) {
	if preparingForSleep {
		return "", false
	}
	return tunRevalidationTriggerResume, true
}
