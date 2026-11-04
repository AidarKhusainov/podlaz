package daemon

import (
	"context"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

// prepareTunCoexistence validates only Podlaz-owned lifecycle authority for a
// new session. Unrelated TUN, routing, split-DNS, NetworkManager, and firewall state
// remains baseline. Proven foreign default DNS ownership is not compatible
// with claiming Podlaz's own route-only default domain.
func (m *XrayManager) prepareTunCoexistence(_ context.Context, s netsnapshot.Snapshot, handoff string, _ netsnapshot.Options) (netsnapshot.Snapshot, error) {
	if api.NormalizeHandoffPolicy(handoff) == api.HandoffAsk {
		return s, &tunHandoffBlocker{
			Policy:    api.HandoffAsk,
			Conflicts: []string{"--handoff=ask is interactive and is not supported by daemon/non-interactive connect"},
			NextStep:  "Choose a non-interactive handoff policy and retry; unrelated host networking will otherwise be treated as baseline state.",
		}
	}

	// Direct callers with an intentionally empty diagnostic fixture have no
	// DNS evidence to classify. The connect lifecycle rejects such incomplete
	// observations before any host-network mutation.
	if s.DNS.Resolved.Status != "" {
		if err := netexecutor.CheckDefaultDNSOwnershipSnapshot(s.DNS, netsnapshot.DefaultTunName); err != nil {
			return s, err
		}
	}

	remaining, _ := m.transactionFileStaleState()
	if len(remaining) != 0 {
		return s, fmt.Errorf("%d exact Podlaz transaction state item(s) still require recovery; refusing a conflicting new network mutation", len(remaining))
	}
	return s, nil
}
