package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const startupScanRefreshTimeout = 5 * time.Second

const unexpectedCoreExitRefreshTimeout = startupScanRefreshTimeout

type startupScanRefreshingLifecycle struct {
	lifecycle *XrayManager
	refresh   func(context.Context)
}

func (l startupScanRefreshingLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	response, err := l.lifecycle.Connect(ctx, request)
	if err == nil {
		l.watchUnexpectedCoreExit()
	}
	l.refreshAfter(ctx)
	return response, err
}

func (l startupScanRefreshingLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	defer l.refreshAfter(ctx)
	if l.lifecycle == nil {
		return api.LifecycleResponse{}, fmt.Errorf("missing lifecycle manager")
	}

	store := newNetworkSessionStateStore(l.lifecycle.runtimeDir(), nil)
	state, exists, err := store.Load()
	if err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("load Network Session before disconnect: %w", err)
	}
	if !exists {
		return l.lifecycle.Disconnect(ctx)
	}

	reason := networkSessionTeardownExplicit
	switch {
	case isTerminalNetworkSessionTeardown(ctx), state.Intent == networkSessionIntentTerminal:
		reason = networkSessionTeardownTerminal
	case state.Intent == networkSessionIntentResume:
		reason = networkSessionTeardownRestart
	}

	protection := privacyEnvelopeLifecycle{
		store:    store,
		executor: netexecutor.PrivacyEnvelopeExecutor{},
	}
	remainingNetwork := newPostPodlazNetworkVerifier()
	coordinator := networkSessionTeardownCoordinator{
		store:            store,
		cleanupDataPlane: l.lifecycle.Disconnect,
		removeProtection: protection.RemoveAfterDataPlaneCleanup,
		verifyRemainingNetwork: func(ctx context.Context) error {
			return remainingNetwork.Verify(ctx)
		},
	}
	return coordinator.Teardown(ctx, reason)
}

func (l startupScanRefreshingLifecycle) Status(ctx context.Context) api.StatusResponse {
	return l.lifecycle.Status(ctx)
}

func (l startupScanRefreshingLifecycle) refreshAfter(ctx context.Context) {
	if l.refresh == nil {
		return
	}
	refreshCtx, cancel := boundedStartupScanRefreshContext(ctx)
	defer cancel()
	l.refresh(refreshCtx)
}

func boundedStartupScanRefreshContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), startupScanRefreshTimeout)
}

func (l startupScanRefreshingLifecycle) watchUnexpectedCoreExit() {
	if l.lifecycle == nil || l.refresh == nil {
		return
	}
	l.lifecycle.mu.Lock()
	done := l.lifecycle.done
	state := l.lifecycle.state
	l.lifecycle.mu.Unlock()

	if state.Connection == "error (core exited)" {
		if state.Mode == planner.ModeTun {
			go l.refreshAfterUnexpectedCoreExit()
		}
		return
	}
	if done == nil {
		return
	}
	go func() {
		<-done
		l.lifecycle.mu.Lock()
		state := l.lifecycle.state
		l.lifecycle.mu.Unlock()
		if state.Connection == "error (core exited)" && state.Mode == planner.ModeTun {
			l.refreshAfterUnexpectedCoreExit()
		}
	}()
}

func (l startupScanRefreshingLifecycle) refreshAfterUnexpectedCoreExit() {
	ctx, cancel := context.WithTimeout(context.Background(), unexpectedCoreExitRefreshTimeout)
	defer cancel()
	l.refresh(ctx)
}
