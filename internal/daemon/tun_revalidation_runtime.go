package daemon

import (
	"context"
	"errors"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type tunRevalidationObservation struct {
	fingerprint tunUplinkFingerprint
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

// Initialize captures generation 1 after connect-time verification has already
// succeeded. It performs no repair and never treats an event as evidence.
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
	r.health = &api.TunHealthStatus{State: api.TunHealthVerified, NetworkGeneration: 1}
	r.mu.Unlock()
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

func (r *tunRevalidationRuntime) Revalidate(ctx context.Context, _ tunRevalidationTrigger) {
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
	if r.hasFingerprint && observation.fingerprint == r.fingerprint {
		r.mu.Unlock()
		return
	}
	generation := currentTunGeneration(r.health)
	if r.hasFingerprint {
		generation++
	}
	if generation == 0 {
		generation = 1
	}
	r.fingerprint = observation.fingerprint
	r.hasFingerprint = true
	r.health = &api.TunHealthStatus{
		State:             api.TunHealthRevalidating,
		NetworkGeneration: generation,
		Classification:    api.TunHealthUplinkChanged,
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
