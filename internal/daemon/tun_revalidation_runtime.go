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

	mu             sync.RWMutex
	health         *api.TunHealthStatus
	fingerprint    tunUplinkFingerprint
	hasFingerprint bool
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

// Initialize establishes generation 1 from one fresh observation and verifies
// that exact observation before publishing verified health. Connect-time
// verification proves the pre-commit state, but it cannot authorize a later
// fingerprint if the underlying uplink changes between commit and publication.
func (r *tunRevalidationRuntime) Initialize(ctx context.Context) {
	if r == nil {
		return
	}
	observation, err := r.inspect(ctx)
	if err != nil {
		r.mu.Lock()
		r.fingerprint = tunUplinkFingerprint{}
		r.hasFingerprint = false
		r.health = healthForObservationError(1, err)
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	r.fingerprint = observation.fingerprint
	r.hasFingerprint = true
	r.health = &api.TunHealthStatus{
		State:             api.TunHealthRevalidating,
		NetworkGeneration: 1,
		Classification:    api.TunHealthUplinkRevalidating,
	}
	r.mu.Unlock()

	err = r.verify(ctx, observation)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.health = &api.TunHealthStatus{State: api.TunHealthVerified, NetworkGeneration: 1}
		return
	}
	r.health = healthForVerificationError(1, err)
}

func (r *tunRevalidationRuntime) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.health = nil
	r.fingerprint = tunUplinkFingerprint{}
	r.hasFingerprint = false
	r.mu.Unlock()
}

func (r *tunRevalidationRuntime) Health() *api.TunHealthStatus {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.health == nil {
		return nil
	}
	copy := *r.health
	return &copy
}

func (r *tunRevalidationRuntime) Revalidate(ctx context.Context, trigger tunRevalidationTrigger) {
	if r == nil {
		return
	}
	observation, err := r.inspect(ctx)
	if err != nil {
		r.mu.Lock()
		generation := currentTunGeneration(r.health)
		r.health = healthForObservationError(generation, err)
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	sameFingerprint := r.hasFingerprint && observation.fingerprint == r.fingerprint
	mustReproveCurrentGeneration := trigger == tunRevalidationTriggerResume || r.health == nil || r.health.State != api.TunHealthVerified
	if sameFingerprint && !mustReproveCurrentGeneration {
		r.mu.Unlock()
		return
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
	r.mu.Unlock()

	err = r.verify(ctx, observation)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.health = &api.TunHealthStatus{State: api.TunHealthVerified, NetworkGeneration: generation}
		return
	}
	r.health = healthForVerificationError(generation, err)
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
