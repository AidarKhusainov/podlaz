package daemon

import (
	"context"
	"os/exec"
	"strings"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

// collectTunResourceSnapshot augments the normal read-only TUN snapshot with
// the complete policy-rule inventory required for collision-free allocation.
// Allocation itself remains pure and does not execute host commands.
func (m *XrayManager) collectTunResourceSnapshot(ctx context.Context, opts netsnapshot.Options) netsnapshot.Snapshot {
	return m.ensureTunPolicyRuleInventory(ctx, m.collectTunSnapshot(ctx, opts))
}

func (m *XrayManager) ensureTunPolicyRuleInventory(ctx context.Context, s netsnapshot.Snapshot) netsnapshot.Snapshot {
	if s.IPv4PolicyRules.Inspection.Status != "" {
		return s
	}

	if m.snapshotCollector != nil {
		// Existing test/legacy in-memory collectors predate the authoritative
		// inventory. Their PolicyRouting signals are safe as explicit fixture
		// evidence, but an explicitly unknown inventory below never falls back.
		s.IPv4PolicyRules = netsnapshot.PolicyRuleInventory{
			Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "IPv4 policy-rule fixture inventory available"},
			Rules:      policyRuleSignalsOnly(s.PolicyRouting),
		}
		return s
	}

	ipPath, err := exec.LookPath("ip")
	if err != nil {
		s.IPv4PolicyRules = netsnapshot.PolicyRuleInventory{Inspection: netsnapshot.Finding{Status: netsnapshot.StatusUnknown, Summary: "IPv4 policy-rule inventory unavailable", Detail: "ip command is unavailable"}}
		return s
	}
	out, ok, detail := runReadOnlyCommand(ctx, ipPath, "-4", "rule", "show")
	if !ok {
		s.IPv4PolicyRules = netsnapshot.PolicyRuleInventory{Inspection: netsnapshot.Finding{Status: netsnapshot.StatusUnknown, Summary: "IPv4 policy-rule inventory unavailable", Detail: detail}}
		return s
	}
	rules, err := netsnapshot.ParseIPv4PolicyRules(out)
	if err != nil {
		s.IPv4PolicyRules = netsnapshot.PolicyRuleInventory{Inspection: netsnapshot.Finding{Status: netsnapshot.StatusUnknown, Summary: "IPv4 policy-rule inventory malformed", Detail: err.Error()}}
		return s
	}
	s.IPv4PolicyRules = netsnapshot.PolicyRuleInventory{
		Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "IPv4 policy-rule inventory available"},
		Rules:      rules,
	}
	return s
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
