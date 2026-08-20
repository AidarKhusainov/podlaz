package daemon

import (
	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

type tunLocalProofState uint8

const (
	tunLocalProofUnknown tunLocalProofState = iota
	tunLocalProofProven
	tunLocalProofViolated
)

type tunMandatoryEvidence struct {
	SessionOwnership tunLocalProofState
	CoreTUN          tunLocalProofState
	OwnedComposition tunLocalProofState
	PrivacyEnvelope  tunLocalProofState
	UplinkPath       tunLocalProofState
	NetworkManager   tunLocalProofState
	ResolvedDNS      tunLocalProofState
}

func tunMandatoryEvidenceFromSnapshot(snapshot netsnapshot.Snapshot) tunMandatoryEvidence {
	evidence := tunMandatoryEvidence{}
	if snapshot.DefaultIPv4.Status == netsnapshot.StatusDetected &&
		snapshot.ServerRoute.Status == netsnapshot.StatusDetected &&
		snapshot.IPv4Addresses.Inspection.Status == netsnapshot.StatusDetected {
		evidence.UplinkPath = tunLocalProofProven
	}

	switch snapshot.NetworkManager.Finding.Status {
	case netsnapshot.StatusMissing, netsnapshot.StatusUnsupported:
		evidence.NetworkManager = tunLocalProofProven
	case netsnapshot.StatusDetected:
		if snapshot.NetworkManager.ActiveConnectionsInspection.Status == netsnapshot.StatusDetected {
			evidence.NetworkManager = tunLocalProofProven
		}
	}

	switch snapshot.DNS.Resolved.Status {
	case netsnapshot.StatusDetected:
		evidence.ResolvedDNS = tunLocalProofProven
	case netsnapshot.StatusMissing, netsnapshot.StatusUnsupported:
		evidence.ResolvedDNS = tunLocalProofViolated
	}
	return maybeInjectE2ETunReconciliationResolvedUnknown(evidence)
}

type tunProbeEvidence struct {
	Group    string
	Provider string
	Success  bool
	Cause    error
}

type tunEvidenceSet struct {
	Mandatory tunMandatoryEvidence
	Probes    []tunProbeEvidence
}

func (e tunEvidenceSet) mandatoryUnknown() bool {
	for _, state := range e.mandatoryStates() {
		if state == tunLocalProofUnknown {
			return true
		}
	}
	return false
}

func (e tunEvidenceSet) mandatoryViolated() bool {
	for _, state := range e.mandatoryStates() {
		if state == tunLocalProofViolated {
			return true
		}
	}
	return false
}

func (e tunEvidenceSet) mandatoryStates() []tunLocalProofState {
	m := e.Mandatory
	return []tunLocalProofState{
		m.SessionOwnership,
		m.CoreTUN,
		m.OwnedComposition,
		m.PrivacyEnvelope,
		m.UplinkPath,
		m.NetworkManager,
		m.ResolvedDNS,
	}
}

type tunReconciliationDecisionKind string

const (
	tunDecisionVerified         tunReconciliationDecisionKind = "verified"
	tunDecisionRetry            tunReconciliationDecisionKind = "retry"
	tunDecisionAwaitEvidence    tunReconciliationDecisionKind = "await-evidence"
	tunDecisionReconcile        tunReconciliationDecisionKind = "reconcile"
	tunDecisionBlockedOwnership tunReconciliationDecisionKind = "blocked-ownership"
	tunDecisionTerminal         tunReconciliationDecisionKind = "terminal"
	tunDecisionSuperseded       tunReconciliationDecisionKind = "superseded"
)

type tunAutomaticDisposition struct {
	Kind                       tunReconciliationDecisionKind
	PublicationRevision        uint64
	ExpectedMutationGeneration uint64
	NetworkSessionID           string
	TransactionID              string
	Generation                 uint64
	Classification             api.TunHealthClassification
	Plan                       planner.TunPlan
	Cause                      error
}
