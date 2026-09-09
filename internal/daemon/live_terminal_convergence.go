package daemon

import (
	"context"
	"errors"
)

// convergeLiveTerminalDataPlane is the raw supervisor/data-plane capability.
// It intentionally does not own Privacy Envelope removal, remaining-host
// verification, or Network Session authority cleanup.
func (m *XrayManager) convergeLiveTerminalDataPlane(ctx context.Context) error {
	if m == nil {
		return errors.New("live terminal data-plane convergence requires Xray manager")
	}
	_, err := m.Disconnect(ctx)
	return err
}

// startupScanRefreshingLifecycle normally coordinates full Network Session
// teardown from Disconnect. Terminal recovery must not call that method because
// exact recovery for every durable transaction must run before session
// protection can be removed. The separate capability therefore delegates only
// to the supervised Xray manager and deliberately skips refresh/finalization.
func (l startupScanRefreshingLifecycle) convergeLiveTerminalDataPlane(ctx context.Context) error {
	if l.lifecycle == nil {
		return errors.New("live terminal data-plane convergence requires lifecycle manager")
	}
	return l.lifecycle.convergeLiveTerminalDataPlane(ctx)
}

// Preserve the health/retry side effects owned by the revalidation wrapper
// while keeping terminal recovery on the data-plane-only capability below it.
func (l tunRevalidationLifecycle) convergeLiveTerminalDataPlane(ctx context.Context) error {
	if l.cancelRetry != nil {
		l.cancelRetry()
	}
	live, ok := l.lifecycle.(networkSessionLiveTerminalConverger)
	if !ok {
		return errors.New("live terminal data-plane convergence is unavailable")
	}
	if err := live.convergeLiveTerminalDataPlane(ctx); err != nil {
		return err
	}
	if l.runtime != nil {
		l.runtime.Clear()
	}
	return nil
}
