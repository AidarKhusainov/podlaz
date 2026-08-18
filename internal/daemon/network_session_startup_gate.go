package daemon

import (
	"context"
	"errors"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

var errNetworkSessionStartupRecoveryPending = errors.New("network session startup recovery is incomplete; run podlaz recover and retry")

type networkSessionStartupMutationGate struct {
	lifecycle lifecycleService

	mu      sync.RWMutex
	blocked bool
}

func newNetworkSessionStartupMutationGate(lifecycle lifecycleService) *networkSessionStartupMutationGate {
	return &networkSessionStartupMutationGate{lifecycle: lifecycle}
}

func (g *networkSessionStartupMutationGate) Block() {
	g.mu.Lock()
	g.blocked = true
	g.mu.Unlock()
}

func (g *networkSessionStartupMutationGate) Release() {
	g.mu.Lock()
	g.blocked = false
	g.mu.Unlock()
}

func (g *networkSessionStartupMutationGate) Blocked() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.blocked
}

func (g *networkSessionStartupMutationGate) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	if g.Blocked() {
		return api.LifecycleResponse{}, errNetworkSessionStartupRecoveryPending
	}
	return g.lifecycle.Connect(ctx, request)
}

func (g *networkSessionStartupMutationGate) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	if g.Blocked() {
		return api.LifecycleResponse{}, errNetworkSessionStartupRecoveryPending
	}
	return g.lifecycle.Disconnect(ctx)
}
