package daemon

import "github.com/AidarKhusainov/podlaz/internal/api"

// beginLifecycleTransition invalidates the currently published health proof
// before a serialized TUN connect/replace starts. It deliberately preserves the
// fingerprint and returns the previous health so a failed replacement can
// restore the still-active old session exactly.
func (r *tunRevalidationRuntime) beginLifecycleTransition() *api.TunHealthStatus {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	previous := cloneTunHealth(r.health)
	r.health = &api.TunHealthStatus{
		State:             api.TunHealthRevalidating,
		NetworkGeneration: currentTunGeneration(r.health),
		Classification:    api.TunHealthUplinkRevalidating,
	}
	return previous
}

func (r *tunRevalidationRuntime) restoreHealth(previous *api.TunHealthStatus) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.health = cloneTunHealth(previous)
	r.mu.Unlock()
}

func cloneTunHealth(health *api.TunHealthStatus) *api.TunHealthStatus {
	if health == nil {
		return nil
	}
	copy := *health
	return &copy
}
