package daemon

import (
	"context"
	"fmt"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

// collectTunResourceSnapshot collects presentation/diagnostic host state only.
// Collision-sensitive allocation authority is collected separately from
// rtnetlink so diagnostic parsing can never imply that a resource is free.
func (m *XrayManager) collectTunResourceSnapshot(ctx context.Context, opts netsnapshot.Options) netsnapshot.Snapshot {
	protectedOpts, err := m.protectedSnapshotOptions(opts)
	if err != nil {
		// A protected continuation must never fall back to resolving the profile
		// hostname over ordinary direct networking when exact bootstrap authority
		// is unreadable. Return intentionally incomplete diagnostic evidence so
		// the existing preflight fails closed before mutation.
		return netsnapshot.Snapshot{
			OS:       "linux",
			Warnings: []string{"protected Network Session bootstrap authority is unavailable; refusing direct DNS fallback"},
		}
	}
	return m.collectTunSnapshot(ctx, protectedOpts)
}

func (m *XrayManager) collectTunAllocationEvidence(ctx context.Context, diagnostic netsnapshot.Snapshot) (netsnapshot.TunAllocationEvidence, error) {
	if m.allocationEvidenceCollector != nil {
		evidence, err := m.allocationEvidenceCollector(ctx)
		if err != nil {
			return netsnapshot.TunAllocationEvidence{}, fmt.Errorf("collect TUN allocation evidence: %w", err)
		}
		return evidence, nil
	}

	if m.snapshotCollector != nil {
		// snapshotCollector is an unexported deterministic test seam. Preserve
		// existing in-memory daemon tests without allowing production mutation to
		// fall back from rtnetlink to presentation-oriented snapshot evidence.
		evidence, err := netsnapshot.TunAllocationEvidenceFromSnapshot(diagnostic)
		if err != nil {
			return netsnapshot.TunAllocationEvidence{}, fmt.Errorf("collect TUN allocation evidence from injected snapshot: %w", err)
		}
		return evidence, nil
	}

	evidence, err := netsnapshot.CollectTunAllocationEvidence(ctx)
	if err != nil {
		return netsnapshot.TunAllocationEvidence{}, fmt.Errorf("collect TUN allocation evidence: %w", err)
	}
	return evidence, nil
}
