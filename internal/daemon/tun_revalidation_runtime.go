package daemon

import (
	"context"
	"errors"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type tunRevalidationObservation struct {
	fingerprint tunUplinkFingerprint
	plan        planner.TunPlan
}

type tunRevalidationInspectFunc func(context.Context) (tunRevalidationObservation, error)
type tunRevalidationVerifyFunc func(context.Context, tunRevalidationObservation) error

type tunRevalidationOutcome struct {
	terminal       bool
	cause          error
	plan           planner.TunPlan
	generation     uint64
	classification api.TunHealthClassification
}

func (o tunRevalidationOutcome) needsLifecycleCleanup() bool {
	return o.terminal && o.cause != nil
}

type tunRevalidationObservationError struct {
	classification api.TunHealthClassification
	cause          error
}

func (e *tunRevalidationObservationError) Error() string {
	if e == nil || e.cause == nil {
		return "TUN revalidation observation failed"
	}
	return e.cause.Error()
}

func (e *tunRevalidationObservationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type tunRevalidationVerificationError struct {
	classification api.TunHealthClassification
	cause          error
}

func (e *tunRevalidationVerificationError) Error() string {
	if e == nil || e.cause == nil {
		return "TUN revalidation verification failed"
	}
	return e.cause.Error()
}

func (e *tunRevalidationVerificationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newTunRevalidationObservationError(classification api.TunHealthClassification, cause error) error {
	return &tunRevalidationObservationError{classification: classification, cause: cause}
}

func newTunRevalidationVerificationError(classification api.TunHealthClassification, cause error) error {
	return &tunRevalidationVerificationError{classification: classification, cause: cause}
}

type tunRevalidationRuntime struct {
	inspect tunRevalidationInspectFunc
	verify  tunRevalidationVerifyFunc

	mu                   sync.RWMutex
	health               *api.TunHealthStatus
	healthPublication    tunRevalidationPublicationToken
	hasHealthPublication bool
	fingerprint          tunUplinkFingerprint
	hasFingerprint       bool
	initialPending       bool
}

func newTunRevalidationRuntime(inspect tunRevalidationInspectFunc, verify tunRevalidationVerifyFunc) *tunRevalidationRuntime {
	if inspect == nil {
		inspect = func(context.Context) (tunRevalidationObservation, error) {
			return tunRevalidationObservation{}, errors.New("missing TUN revalidation inspector")
		}
	}
	if verify == nil {
		verify = func(context.Context, tunRevalidationObservation) error {
			return errors.New("missing TUN revalidation verifier")
		}
	}
	return &tunRevalidationRuntime{inspect: inspect, verify: verify}
}

// PrepareInitialize invalidates any old fingerprint and publishes fail-closed
// generation-one health synchronously with the successful connect lifecycle.
// The expensive proof itself is scheduled through the coordinator after the
// lifecycle mutation releases its authority token.
func (r *tunRevalidationRuntime) PrepareInitialize() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.fingerprint = tunUplinkFingerprint{}
	r.hasFingerprint = false
	r.initialPending = true
	r.health = &api.TunHealthStatus{
		State:             api.TunHealthRevalidating,
		NetworkGeneration: 1,
		Classification:    api.TunHealthUplinkRevalidating,
	}
	r.hasHealthPublication = false
	r.mu.Unlock()
}

// InitializePending runs generation-one proof only while the current lifecycle
// still requires it. Disconnect clears the pending bit before the coordinator
// can execute a requeued stale initial trigger. Mutation cancellation keeps the
// bit set so a failed/replaced lifecycle can obtain one fresh post-mutation proof.
func (r *tunRevalidationRuntime) InitializePending(ctx context.Context) tunRevalidationOutcome {
	if r == nil {
		return tunRevalidationOutcome{}
	}
	r.mu.RLock()
	pending := r.initialPending
	r.mu.RUnlock()
	if !pending {
		return tunRevalidationOutcome{}
	}

	outcome := r.initialize(ctx)
	r.mu.Lock()
	if !errors.Is(outcome.cause, context.Canceled) {
		r.initialPending = false
	}
	r.mu.Unlock()
	return outcome
}

// Initialize establishes generation 1 from one fresh observation and verifies
// that exact observation before publishing verified health. Connect-time
// verification proves the pre-commit state, but it cannot authorize a later
// fingerprint if the underlying uplink changes between commit and publication.
func (r *tunRevalidationRuntime) Initialize(ctx context.Context) error {
	return r.initialize(ctx).cause
}

func (r *tunRevalidationRuntime) initialize(ctx context.Context) tunRevalidationOutcome {
	if r == nil {
		return tunRevalidationOutcome{}
	}
	publication, hasPublication := tunRevalidationPublicationTokenFromContext(ctx)
	observation, err := r.inspect(ctx)
	if errors.Is(ctx.Err(), context.Canceled) {
		return tunRevalidationOutcome{cause: context.Canceled}
	}
	if hasPublication && !publication.isCurrent() {
		return tunRevalidationOutcome{}
	}
	if err != nil {
		r.mu.Lock()
		r.fingerprint = tunUplinkFingerprint{}
		r.hasFingerprint = false
		r.health = healthForObservationError(1, err)
		r.setHealthPublicationLocked(publication, hasPublication)
		r.mu.Unlock()
		return tunRevalidationOutcome{cause: err}
	}

	r.mu.Lock()
	r.fingerprint = observation.fingerprint
	r.hasFingerprint = true
	r.health = &api.TunHealthStatus{
		State:             api.TunHealthRevalidating,
		NetworkGeneration: 1,
		Classification:    api.TunHealthUplinkRevalidating,
	}
	r.setHealthPublicationLocked(publication, hasPublication)
	r.mu.Unlock()

	err = r.verify(ctx, observation)
	if errors.Is(ctx.Err(), context.Canceled) {
		return tunRevalidationOutcome{cause: context.Canceled}
	}
	if hasPublication && !publication.isCurrent() {
		return tunRevalidationOutcome{}
	}
	r.mu.Lock()
	if err == nil {
		r.health = &api.TunHealthStatus{State: api.TunHealthVerified, NetworkGeneration: 1}
		r.setHealthPublicationLocked(publication, hasPublication)
		r.mu.Unlock()
		return tunRevalidationOutcome{}
	}
	health := healthForVerificationError(1, err)
	r.health = health
	r.setHealthPublicationLocked(publication, hasPublication)
	r.mu.Unlock()
	return terminalTunRevalidationOutcome(observation.plan, health, err)
}

func (r *tunRevalidationRuntime) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.health = nil
	r.healthPublication = tunRevalidationPublicationToken{}
	r.hasHealthPublication = false
	r.fingerprint = tunUplinkFingerprint{}
	r.hasFingerprint = false
	r.initialPending = false
	r.mu.Unlock()
}

func (r *tunRevalidationRuntime) Health() *api.TunHealthStatus {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	if r.health == nil {
		r.mu.RUnlock()
		return nil
	}
	copy := *r.health
	publication := r.healthPublication
	hasPublication := r.hasHealthPublication
	r.mu.RUnlock()

	// A result can race with Notify after its last pre-publication revision
	// check. Keep that result private by rendering fail-closed revalidating health
	// until the coordinator consumes the newest hint.
	if hasPublication && !publication.isCurrent() {
		return &api.TunHealthStatus{
			State:             api.TunHealthRevalidating,
			NetworkGeneration: currentTunGeneration(&copy),
			Classification:    api.TunHealthUplinkRevalidating,
		}
	}
	return &copy
}

func (r *tunRevalidationRuntime) Revalidate(ctx context.Context, trigger tunRevalidationTrigger) tunRevalidationOutcome {
	if r == nil {
		return tunRevalidationOutcome{}
	}
	publication, hasPublication := tunRevalidationPublicationTokenFromContext(ctx)
	observation, err := r.inspect(ctx)
	if errors.Is(ctx.Err(), context.Canceled) {
		return tunRevalidationOutcome{cause: context.Canceled}
	}
	if hasPublication && !publication.isCurrent() {
		return tunRevalidationOutcome{}
	}
	if err != nil {
		r.mu.Lock()
		generation := currentTunGeneration(r.health)
		r.health = healthForObservationError(generation, err)
		r.setHealthPublicationLocked(publication, hasPublication)
		r.mu.Unlock()
		return tunRevalidationOutcome{cause: err}
	}

	r.mu.Lock()
	sameFingerprint := r.hasFingerprint && observation.fingerprint == r.fingerprint
	mustReproveCurrentGeneration := trigger == tunRevalidationTriggerResume || trigger == tunRevalidationTriggerSourceResync || r.health == nil || r.health.State != api.TunHealthVerified
	if sameFingerprint && !mustReproveCurrentGeneration {
		// The fresh observation proves that this ordinary link/address/route hint
		// did not invalidate the verified generation. Advance the publication
		// token so Health does not remain fail-closed after a harmless duplicate.
		r.setHealthPublicationLocked(publication, hasPublication)
		r.mu.Unlock()
		return tunRevalidationOutcome{}
	}
	generation := currentTunGeneration(r.health)
	classification := api.TunHealthUplinkRevalidating
	if r.hasFingerprint && !sameFingerprint {
		generation++
		classification = api.TunHealthUplinkChanged
	}
	if generation == 0 {
		generation = 1
	}
	r.fingerprint = observation.fingerprint
	r.hasFingerprint = true
	r.health = &api.TunHealthStatus{
		State:             api.TunHealthRevalidating,
		NetworkGeneration: generation,
		Classification:    classification,
	}
	r.setHealthPublicationLocked(publication, hasPublication)
	r.mu.Unlock()

	err = r.verify(ctx, observation)
	if errors.Is(ctx.Err(), context.Canceled) {
		return tunRevalidationOutcome{cause: context.Canceled}
	}
	if hasPublication && !publication.isCurrent() {
		return tunRevalidationOutcome{}
	}
	r.mu.Lock()
	if err == nil {
		r.health = &api.TunHealthStatus{State: api.TunHealthVerified, NetworkGeneration: generation}
		r.setHealthPublicationLocked(publication, hasPublication)
		r.mu.Unlock()
		return tunRevalidationOutcome{}
	}
	health := healthForVerificationError(generation, err)
	r.health = health
	r.setHealthPublicationLocked(publication, hasPublication)
	r.mu.Unlock()
	return terminalTunRevalidationOutcome(observation.plan, health, err)
}

func (r *tunRevalidationRuntime) MarkCleanupRequired(outcome tunRevalidationOutcome) {
	if r == nil || !outcome.needsLifecycleCleanup() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	generation := outcome.generation
	if generation == 0 {
		generation = currentTunGeneration(r.health)
	}
	classification := outcome.classification
	if classification == "" && r.health != nil {
		classification = r.health.Classification
	}
	if classification == "" {
		classification = api.TunHealthOwnedStateInvalid
	}
	r.health = &api.TunHealthStatus{
		State:             api.TunHealthCleanupRequired,
		NetworkGeneration: generation,
		Classification:    classification,
	}
	// Terminal cleanup has already crossed the coordinator's revision claim.
	// A later hint cannot revoke an authoritative cleanup-required result.
	r.healthPublication = tunRevalidationPublicationToken{}
	r.hasHealthPublication = false
}

func (r *tunRevalidationRuntime) setHealthPublicationLocked(publication tunRevalidationPublicationToken, present bool) {
	if !present {
		r.healthPublication = tunRevalidationPublicationToken{}
		r.hasHealthPublication = false
		return
	}
	r.healthPublication = publication
	r.hasHealthPublication = true
}

func terminalTunRevalidationOutcome(plan planner.TunPlan, health *api.TunHealthStatus, err error) tunRevalidationOutcome {
	if err == nil || errors.Is(err, context.Canceled) {
		return tunRevalidationOutcome{cause: err}
	}
	outcome := tunRevalidationOutcome{
		terminal: true,
		cause:    err,
		plan:     plan,
	}
	if health != nil {
		outcome.generation = health.NetworkGeneration
		outcome.classification = health.Classification
	}
	return outcome
}

func currentTunGeneration(health *api.TunHealthStatus) uint64 {
	if health == nil || health.NetworkGeneration == 0 {
		return 1
	}
	return health.NetworkGeneration
}

func healthForObservationError(generation uint64, err error) *api.TunHealthStatus {
	if generation == 0 {
		generation = 1
	}
	classification := api.TunHealthUplinkFingerprintUnavailable
	state := api.TunHealthDegraded
	var observationErr *tunRevalidationObservationError
	if errors.As(err, &observationErr) && observationErr.classification != "" {
		classification = observationErr.classification
	}
	if classification == api.TunHealthOwnershipInvalid {
		state = api.TunHealthCleanupRequired
	}
	return &api.TunHealthStatus{State: state, NetworkGeneration: generation, Classification: classification}
}

func healthForVerificationError(generation uint64, err error) *api.TunHealthStatus {
	classification := api.TunHealthOwnedStateInvalid
	state := api.TunHealthDegraded
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		classification = api.TunHealthRevalidationTimeout
	case errors.Is(err, context.Canceled):
		classification = api.TunHealthRevalidationInterrupted
	default:
		var verificationErr *tunRevalidationVerificationError
		if errors.As(err, &verificationErr) && verificationErr.classification != "" {
			classification = verificationErr.classification
		}
	}
	if classification == api.TunHealthOwnershipInvalid {
		state = api.TunHealthCleanupRequired
	}
	return &api.TunHealthStatus{State: state, NetworkGeneration: generation, Classification: classification}
}
