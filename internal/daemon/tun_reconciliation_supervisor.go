package daemon

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	defaultTunReconciliationDeadline = 45 * time.Second
	defaultTunNoProgressRounds       = 3
	defaultTunReconciliationRetry    = time.Second
)

type tunReconciliationRound struct {
	PublicationRevision        uint64
	ExpectedMutationGeneration uint64
	NetworkSessionID           string
	TransactionID              string
	Generation                 uint64
	Fingerprint                tunUplinkFingerprint
	Evidence                   tunEvidenceSet
	Plan                       planner.TunPlan
	Cause                      error
	HardUnsafe                 bool
	OwnershipBlocked           bool
	NeedsReconcile             bool
}

type tunReconciliationDecision struct {
	Kind           tunReconciliationDecisionKind
	Classification api.TunHealthClassification
	RetryAfter     time.Duration
	Disposition    *tunAutomaticDisposition
}

type tunReconciliationCycle struct {
	deadline        time.Time
	progressKey     string
	hasProgressKey  bool
	noProgressCount int
}

type tunReconciliationSupervisor struct {
	mu                  sync.Mutex
	now                 func() time.Time
	deadline            time.Duration
	maxNoProgressRounds int
	cycles              map[string]*tunReconciliationCycle
}

func newTunReconciliationSupervisor(now func() time.Time) *tunReconciliationSupervisor {
	return newTunReconciliationSupervisorWithPolicy(now, defaultTunReconciliationDeadline, defaultTunNoProgressRounds)
}

func newTunReconciliationSupervisorWithPolicy(now func() time.Time, deadline time.Duration, maxNoProgressRounds int) *tunReconciliationSupervisor {
	if now == nil {
		now = time.Now
	}
	if deadline <= 0 {
		deadline = defaultTunReconciliationDeadline
	}
	if maxNoProgressRounds <= 0 {
		maxNoProgressRounds = defaultTunNoProgressRounds
	}
	return &tunReconciliationSupervisor{
		now:                 now,
		deadline:            deadline,
		maxNoProgressRounds: maxNoProgressRounds,
		cycles:              make(map[string]*tunReconciliationCycle),
	}
}

func (s *tunReconciliationSupervisor) RunRound(round tunReconciliationRound) tunReconciliationDecision {
	if s == nil {
		return tunReconciliationDecision{Kind: tunDecisionBlockedOwnership, Classification: api.TunHealthOwnershipInvalid}
	}
	if strings.TrimSpace(round.NetworkSessionID) == "" || round.OwnershipBlocked || round.Evidence.Mandatory.SessionOwnership == tunLocalProofViolated {
		s.clearCycle(round.NetworkSessionID)
		return tunReconciliationDecision{Kind: tunDecisionBlockedOwnership, Classification: api.TunHealthOwnershipInvalid}
	}
	if round.HardUnsafe {
		s.clearCycle(round.NetworkSessionID)
		return s.automaticDecision(round, tunDecisionTerminal, terminalClassification(round))
	}
	if round.Evidence.mandatoryUnknown() {
		return s.retryOrBoundedTerminal(round, api.TunHealthNetworkConverging, false)
	}
	if round.NeedsReconcile || round.Evidence.mandatoryViolated() {
		return s.automaticDecision(round, tunDecisionReconcile, api.TunHealthOwnedStateReconciling)
	}
	if sufficientIndependentPositiveEvidence(round.Evidence.Probes) {
		s.clearCycle(round.NetworkSessionID)
		return tunReconciliationDecision{Kind: tunDecisionVerified}
	}

	persistentExternalFailure := independentFailedProviderCount(round.Evidence.Probes) >= 2
	return s.retryOrBoundedTerminal(round, api.TunHealthConnectivityFailed, persistentExternalFailure)
}

func (s *tunReconciliationSupervisor) retryOrBoundedTerminal(round tunReconciliationRound, classification api.TunHealthClassification, terminalEligible bool) tunReconciliationDecision {
	now := s.now()
	s.mu.Lock()
	cycle := s.cycles[round.NetworkSessionID]
	if cycle == nil {
		cycle = &tunReconciliationCycle{deadline: now.Add(s.deadline)}
		s.cycles[round.NetworkSessionID] = cycle
	}
	progressKey := tunReconciliationProgressKey(round)
	if cycle.hasProgressKey {
		if cycle.progressKey == progressKey {
			cycle.noProgressCount++
		} else {
			cycle.noProgressCount = 0
		}
	}
	cycle.progressKey = progressKey
	cycle.hasProgressKey = true
	boundaryReached := !now.Before(cycle.deadline) || cycle.noProgressCount >= s.maxNoProgressRounds
	if boundaryReached && terminalEligible {
		delete(s.cycles, round.NetworkSessionID)
		s.mu.Unlock()
		return s.automaticDecision(round, tunDecisionTerminal, terminalClassification(round))
	}
	s.mu.Unlock()
	return tunReconciliationDecision{
		Kind:           tunDecisionRetry,
		Classification: classification,
		RetryAfter:     defaultTunReconciliationRetry,
	}
}

func (s *tunReconciliationSupervisor) automaticDecision(round tunReconciliationRound, kind tunReconciliationDecisionKind, classification api.TunHealthClassification) tunReconciliationDecision {
	disposition := &tunAutomaticDisposition{
		Kind:                       kind,
		PublicationRevision:        round.PublicationRevision,
		ExpectedMutationGeneration: round.ExpectedMutationGeneration,
		NetworkSessionID:           round.NetworkSessionID,
		TransactionID:              round.TransactionID,
		Generation:                 round.Generation,
		Classification:             classification,
		Plan:                       round.Plan,
		Cause:                      round.Cause,
	}
	return tunReconciliationDecision{Kind: kind, Classification: classification, Disposition: disposition}
}

func (s *tunReconciliationSupervisor) clearCycle(sessionID string) {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	s.mu.Lock()
	delete(s.cycles, sessionID)
	s.mu.Unlock()
}

func sufficientIndependentPositiveEvidence(probes []tunProbeEvidence) bool {
	providers := make(map[string]struct{})
	for _, probe := range probes {
		provider := strings.TrimSpace(probe.Provider)
		if !probe.Success || provider == "" {
			continue
		}
		providers[provider] = struct{}{}
	}
	return len(providers) >= 2
}

func independentFailedProviderCount(probes []tunProbeEvidence) int {
	providers := make(map[string]struct{})
	for _, probe := range probes {
		provider := strings.TrimSpace(probe.Provider)
		if probe.Success || provider == "" {
			continue
		}
		providers[provider] = struct{}{}
	}
	return len(providers)
}

func terminalClassification(round tunReconciliationRound) api.TunHealthClassification {
	if round.Evidence.Mandatory.SessionOwnership == tunLocalProofViolated || round.OwnershipBlocked {
		return api.TunHealthOwnershipInvalid
	}
	if round.Evidence.mandatoryViolated() || round.HardUnsafe {
		return api.TunHealthOwnedStateInvalid
	}
	return api.TunHealthConnectivityFailed
}

func tunReconciliationProgressKey(round tunReconciliationRound) string {
	probes := make([]string, 0, len(round.Evidence.Probes))
	for _, probe := range round.Evidence.Probes {
		probes = append(probes, fmt.Sprintf("%s/%s=%t", strings.TrimSpace(probe.Provider), strings.TrimSpace(probe.Group), probe.Success))
	}
	sort.Strings(probes)
	return fmt.Sprintf(
		"uplink=%s/%d/%s/%s/%s/%s/%s;mandatory=%d,%d,%d,%d,%d,%d,%d;probes=%s",
		round.Fingerprint.Interface,
		round.Fingerprint.InterfaceIndex,
		round.Fingerprint.Gateway,
		round.Fingerprint.Addresses,
		round.Fingerprint.NetworkManagerID,
		round.Fingerprint.ServerRouteInterface,
		round.Fingerprint.ServerRouteGateway,
		round.Evidence.Mandatory.SessionOwnership,
		round.Evidence.Mandatory.CoreTUN,
		round.Evidence.Mandatory.OwnedComposition,
		round.Evidence.Mandatory.PrivacyEnvelope,
		round.Evidence.Mandatory.UplinkPath,
		round.Evidence.Mandatory.NetworkManager,
		round.Evidence.Mandatory.ResolvedDNS,
		strings.Join(probes, ","),
	)
}
