package daemon

import (
	"context"
	"errors"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type tunReconciliationObserveFunc func(context.Context) (tunReconciliationRound, error)

type tunEvidenceRevalidationRuntime struct {
	*tunRevalidationRuntime
	observe    tunReconciliationObserveFunc
	supervisor *tunReconciliationSupervisor
}

func newTunEvidenceRevalidationRuntime(observe tunReconciliationObserveFunc, supervisor *tunReconciliationSupervisor) *tunEvidenceRevalidationRuntime {
	if observe == nil {
		observe = func(context.Context) (tunReconciliationRound, error) {
			return tunReconciliationRound{}, errors.New("missing TUN reconciliation observer")
		}
	}
	if supervisor == nil {
		supervisor = newTunReconciliationSupervisor(nil)
	}
	return &tunEvidenceRevalidationRuntime{
		tunRevalidationRuntime: newTunRevalidationRuntime(nil, nil),
		observe:                observe,
		supervisor:             supervisor,
	}
}

func (r *tunEvidenceRevalidationRuntime) RunEvidenceRound(ctx context.Context, trigger tunRevalidationTrigger, expectedMutationGeneration uint64) tunReconciliationDecision {
	if r == nil || r.tunRevalidationRuntime == nil || r.supervisor == nil {
		return tunReconciliationDecision{Kind: tunDecisionBlockedOwnership, Classification: api.TunHealthOwnershipInvalid}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	publication, hasPublication := tunRevalidationPublicationTokenFromContext(ctx)

	if trigger == tunRevalidationTriggerInitial && !r.InitialPending() {
		return tunReconciliationDecision{Kind: tunDecisionSuperseded}
	}
	round, err := r.observe(ctx)
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return tunReconciliationDecision{Kind: tunDecisionSuperseded}
	}
	if hasPublication && !publication.isCurrent() {
		return tunReconciliationDecision{Kind: tunDecisionSuperseded}
	}
	if err != nil {
		// Unexpected observation failures do not acquire mutation authority. If
		// the observer could not produce exact session identity, fail closed as
		// blocked ownership; otherwise keep the session degraded and bounded.
		if round.NetworkSessionID == "" {
			round.OwnershipBlocked = true
		}
		round.Cause = err
	}

	generation := r.prepareEvidenceGeneration(trigger, round.Fingerprint)
	round.Generation = generation
	round.ExpectedMutationGeneration = expectedMutationGeneration
	if hasPublication {
		round.PublicationRevision = publication.revision
	}
	decision := r.supervisor.RunRound(round)
	if hasPublication && !publication.isCurrent() {
		return tunReconciliationDecision{Kind: tunDecisionSuperseded}
	}
	r.publishEvidenceDecision(decision, generation, publication, hasPublication)
	return decision
}

func (r *tunEvidenceRevalidationRuntime) prepareEvidenceGeneration(trigger tunRevalidationTrigger, fingerprint tunUplinkFingerprint) uint64 {
	base := r.tunRevalidationRuntime
	base.mu.Lock()
	defer base.mu.Unlock()

	generation := currentTunGeneration(base.health)
	if trigger == tunRevalidationTriggerInitial {
		generation = 1
	}
	if !isZeroTunUplinkFingerprint(fingerprint) {
		if trigger != tunRevalidationTriggerInitial && base.hasFingerprint && fingerprint != base.fingerprint {
			generation++
		}
		base.fingerprint = fingerprint
		base.hasFingerprint = true
	}
	if generation == 0 {
		generation = 1
	}
	return generation
}

func (r *tunEvidenceRevalidationRuntime) publishEvidenceDecision(
	decision tunReconciliationDecision,
	generation uint64,
	publication tunRevalidationPublicationToken,
	hasPublication bool,
) {
	base := r.tunRevalidationRuntime
	base.mu.Lock()
	defer base.mu.Unlock()

	var health *api.TunHealthStatus
	switch decision.Kind {
	case tunDecisionVerified:
		health = &api.TunHealthStatus{State: api.TunHealthVerified, NetworkGeneration: generation}
		base.initialPending = false
	case tunDecisionRetry:
		state := api.TunHealthDegraded
		if decision.Classification == api.TunHealthNetworkConverging {
			state = api.TunHealthRevalidating
		}
		health = &api.TunHealthStatus{State: state, NetworkGeneration: generation, Classification: decision.Classification}
	case tunDecisionReconcile:
		health = &api.TunHealthStatus{State: api.TunHealthRevalidating, NetworkGeneration: generation, Classification: api.TunHealthOwnedStateReconciling}
	case tunDecisionBlockedOwnership:
		classification := decision.Classification
		if classification == "" {
			classification = api.TunHealthOwnershipInvalid
		}
		state := api.TunHealthDegraded
		if classification == api.TunHealthOwnershipInvalid {
			state = api.TunHealthCleanupRequired
		}
		health = &api.TunHealthStatus{State: state, NetworkGeneration: generation, Classification: classification}
		base.initialPending = false
	case tunDecisionTerminal:
		// Terminal is not authoritative until the coordinator has atomically
		// claimed publication revision + lifecycle generation and admitted the
		// mutation. Until that handoff succeeds, status stays degraded rather
		// than claiming cleanup already happened.
		health = &api.TunHealthStatus{State: api.TunHealthDegraded, NetworkGeneration: generation, Classification: decision.Classification}
		base.initialPending = false
	case tunDecisionSuperseded:
		return
	default:
		return
	}
	base.health = health
	base.setHealthPublicationLocked(publication, hasPublication)
}

func (r *tunRevalidationRuntime) InitialPending() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.initialPending
}

func (r *tunRevalidationRuntime) MarkAutomaticCleanupRequired(disposition tunAutomaticDisposition) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	generation := disposition.Generation
	if generation == 0 {
		generation = currentTunGeneration(r.health)
	}
	classification := disposition.Classification
	if classification == "" {
		classification = api.TunHealthOwnedStateInvalid
	}
	r.health = &api.TunHealthStatus{
		State:             api.TunHealthCleanupRequired,
		NetworkGeneration: generation,
		Classification:    classification,
	}
	r.healthPublication = tunRevalidationPublicationToken{}
	r.hasHealthPublication = false
	r.initialPending = false
}

func isZeroTunUplinkFingerprint(fingerprint tunUplinkFingerprint) bool {
	return fingerprint == (tunUplinkFingerprint{})
}
