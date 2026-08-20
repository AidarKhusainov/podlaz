package daemon

import (
	"fmt"
	"strings"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

// protectedSnapshotOptions substitutes only the exact concrete bootstrap
// endpoint already authorized by the current protected Network Session. It
// never broadens DNS/firewall permissions and never reuses authority for a
// different saved profile/server request.
func (m *XrayManager) protectedSnapshotOptions(opts netsnapshot.Options) (netsnapshot.Options, error) {
	requestedServer := strings.TrimSpace(opts.Server)
	if requestedServer == "" {
		return opts, nil
	}
	store := newNetworkSessionStateStore(m.runtimeDir(), nil)
	state, exists, err := store.Load()
	if err != nil {
		return opts, fmt.Errorf("load protected Network Session bootstrap state: %w", err)
	}
	if !exists || state.Intent != networkSessionIntentResume || state.Protection == nil {
		return opts, nil
	}
	if strings.TrimSpace(state.Request.Profile.Server) != requestedServer {
		return opts, nil
	}
	bootstrap, ok, err := networkSessionBootstrapServer(store, state.Request.Profile.ID)
	if err != nil {
		return opts, err
	}
	if !ok {
		return opts, nil
	}
	opts.Server = bootstrap
	return opts, nil
}
