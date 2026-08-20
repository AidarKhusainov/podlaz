package daemon

import (
	"context"
	"strings"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

// collectTunResourceSnapshot augments the normal read-only TUN snapshot with
// canonical numeric route/rule identities required for collision-free
// allocation. Allocation itself remains pure and does not execute host commands.
func (m *XrayManager) collectTunResourceSnapshot(ctx context.Context, opts netsnapshot.Options) netsnapshot.Snapshot {
	s := m.collectTunSnapshot(ctx, opts)
	if m.snapshotCollector == nil {
		return netsnapshot.EnsureTunAllocationEvidence(ctx, s)
	}
	return m.ensureTunPolicyRuleInventory(ctx, s)
}

func (m *XrayManager) ensureTunPolicyRuleInventory(ctx context.Context, s netsnapshot.Snapshot) netsnapshot.Snapshot {
	if s.IPv4PolicyRules.Inspection.Status != "" {
		return s
	}

	if m.snapshotCollector != nil {
		// Existing test/legacy in-memory collectors predate the authoritative
		// inventory. Their PolicyRouting signals are safe as explicit fixture
		// evidence, but an explicitly unknown inventory never falls back.
		s.IPv4PolicyRules = netsnapshot.PolicyRuleInventory{
			Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "IPv4 policy-rule fixture inventory available"},
			Rules:      policyRuleSignalsOnly(s.PolicyRouting),
		}
		return s
	}

	return netsnapshot.EnsureTunAllocationEvidence(ctx, s)
}

func policyRuleSignalsOnly(signals []netsnapshot.PolicyRoutingSignal) []netsnapshot.PolicyRoutingSignal {
	out := make([]netsnapshot.PolicyRoutingSignal, 0, len(signals))
	for _, signal := range signals {
		if strings.TrimSpace(signal.Kind) == "rule" {
			out = append(out, signal)
		}
	}
	return out
}
