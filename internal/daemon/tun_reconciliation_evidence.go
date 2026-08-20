package daemon

import (
	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
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
