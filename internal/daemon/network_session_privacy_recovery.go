package daemon

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type networkSessionProtectionRecoveryExecutor interface {
	Exists(context.Context, netexecutor.PrivacyEnvelopePlan) (bool, error)
	Apply(context.Context, netexecutor.PrivacyEnvelopePlan) error
	Verify(context.Context, netexecutor.PrivacyEnvelopePlan) error
}

type networkSessionProtectionReplacementRecoveryExecutor interface {
	networkSessionProtectionRecoveryExecutor
	Replace(context.Context, netexecutor.PrivacyEnvelopePlan, netexecutor.PrivacyEnvelopePlan) error
}

// reconcileNetworkSessionProtection restores or verifies the exact persisted
// Privacy Envelope before any old Data Plane Generation is rolled back. The
// persisted Network Session state is the only authority: generated table names,
// comments, or historical values never grant permission to adopt or replace an
// unexpected live object.
func reconcileNetworkSessionProtection(
	ctx context.Context,
	store networkSessionStateStore,
	executor networkSessionProtectionRecoveryExecutor,
) (networkSessionState, bool, error) {
	state, exists, err := store.Load()
	if err != nil {
		return networkSessionState{}, false, fmt.Errorf("load Network Session privacy authority: %w", err)
	}
	if !exists || state.Protection == nil {
		return state, false, nil
	}
	if executor == nil {
		return state, true, errors.New("Network Session privacy reconciliation has no executor")
	}
	if state.Intent != networkSessionIntentResume {
		return state, true, fmt.Errorf("Network Session privacy protection requires %q convergence, not resume recovery", state.Intent)
	}
	if state.Protection.State != networkSessionProtectionArming && state.Protection.State != networkSessionProtectionArmed {
		return state, true, fmt.Errorf("Network Session privacy protection cannot resume from state %q", state.Protection.State)
	}
	if state.Replacement != nil {
		replacementExecutor, ok := executor.(networkSessionProtectionReplacementRecoveryExecutor)
		if !ok {
			return state, true, errors.New("Network Session replacement recovery requires atomic Privacy Envelope replacement support")
		}
		return reconcileProtectedNetworkSessionReplacement(ctx, store, state, replacementExecutor)
	}

	plan, err := privacyEnvelopePlanFromAuthority(*state.Protection)
	if err != nil {
		return state, true, fmt.Errorf("reconstruct exact Network Session privacy envelope: %w", err)
	}

	present, err := executor.Exists(ctx, plan)
	if err != nil {
		return state, true, fmt.Errorf("observe exact Network Session privacy envelope: %w", err)
	}
	if present {
		if err := executor.Verify(ctx, plan); err != nil {
			return state, true, fmt.Errorf("verify exact Network Session privacy envelope: %w", err)
		}
	} else {
		// The table identity is already durable and exact. Recovery recreates
		// that exact resource rather than allocating a new candidate or inferring
		// ownership from whatever currently exists on the host.
		if err := executor.Apply(ctx, plan); err != nil {
			return state, true, fmt.Errorf("recreate exact Network Session privacy envelope: %w", err)
		}
		if err := executor.Verify(ctx, plan); err != nil {
			return state, true, fmt.Errorf("verify recreated Network Session privacy envelope: %w", err)
		}
	}

	if state.Protection.State != networkSessionProtectionArmed {
		protection := cloneNetworkSessionProtection(*state.Protection)
		protection.State = networkSessionProtectionArmed
		if err := store.SetProtection(&protection); err != nil {
			return state, true, fmt.Errorf("persist reconciled Network Session privacy envelope: %w", err)
		}
		state.Protection = &protection
	}
	return cloneNetworkSessionState(state), true, nil
}

func reconcileProtectedNetworkSessionReplacement(
	ctx context.Context,
	store networkSessionStateStore,
	state networkSessionState,
	executor networkSessionProtectionReplacementRecoveryExecutor,
) (networkSessionState, bool, error) {
	if state.Replacement == nil || state.Replacement.PreviousProtection == nil || state.Protection == nil {
		return state, true, errors.New("protected Network Session replacement recovery lacks durable rollback authority")
	}

	previousPlan, err := privacyEnvelopePlanFromAuthority(*state.Replacement.PreviousProtection)
	if err != nil {
		return state, true, fmt.Errorf("reconstruct previous Privacy Envelope for replacement recovery: %w", err)
	}
	present, err := executor.Exists(ctx, previousPlan)
	if err != nil {
		return state, true, fmt.Errorf("observe Privacy Envelope during replacement recovery: %w", err)
	}
	if !present {
		if err := executor.Apply(ctx, previousPlan); err != nil {
			return state, true, fmt.Errorf("recreate previous Privacy Envelope after replacement crash: %w", err)
		}
		if err := executor.Verify(ctx, previousPlan); err != nil {
			return state, true, fmt.Errorf("verify recreated previous Privacy Envelope after replacement crash: %w", err)
		}
		return restoreProtectedReplacementAuthority(store, state)
	}

	candidates, err := replacementRecoveryPlans(state)
	if err != nil {
		return state, true, err
	}
	var matched *netexecutor.PrivacyEnvelopePlan
	for i := range candidates {
		candidate := candidates[i]
		if err := executor.Verify(ctx, candidate); err != nil {
			continue
		}
		if matched != nil {
			return state, true, errors.New("multiple persisted Privacy Envelope compositions matched during replacement recovery")
		}
		matched = &candidate
	}
	if matched == nil {
		return state, true, errors.New("live Privacy Envelope does not match any exact persisted replacement composition")
	}

	if !reflect.DeepEqual(*matched, previousPlan) {
		if err := executor.Replace(ctx, *matched, previousPlan); err != nil {
			return state, true, fmt.Errorf("restore previous Privacy Envelope after replacement crash: %w", err)
		}
		if err := executor.Verify(ctx, previousPlan); err != nil {
			return state, true, fmt.Errorf("verify restored previous Privacy Envelope after replacement crash: %w", err)
		}
	}
	return restoreProtectedReplacementAuthority(store, state)
}

func replacementRecoveryPlans(state networkSessionState) ([]netexecutor.PrivacyEnvelopePlan, error) {
	if state.Replacement == nil || state.Replacement.PreviousProtection == nil || state.Protection == nil {
		return nil, errors.New("replacement recovery plans require previous and current protection authority")
	}

	authorities := []networkSessionProtection{
		cloneNetworkSessionProtection(*state.Replacement.PreviousProtection),
		cloneNetworkSessionProtection(*state.Protection),
	}
	if len(state.Protection.PreviousBootstrapIPv4) != 0 {
		transition := cloneNetworkSessionProtection(*state.Protection)
		transition.State = networkSessionProtectionArmed
		transition.BootstrapIPv4 = append([]string(nil), state.Protection.PreviousBootstrapIPv4...)
		transition.PreviousBootstrapIPv4 = nil
		authorities = append(authorities, transition)
	}

	plans := make([]netexecutor.PrivacyEnvelopePlan, 0, len(authorities))
	for _, authority := range authorities {
		plan, err := privacyEnvelopePlanFromAuthority(authority)
		if err != nil {
			return nil, fmt.Errorf("reconstruct persisted replacement Privacy Envelope composition: %w", err)
		}
		duplicate := false
		for _, existing := range plans {
			if reflect.DeepEqual(existing, plan) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			plans = append(plans, plan)
		}
	}
	return plans, nil
}

func restoreProtectedReplacementAuthority(store networkSessionStateStore, fallback networkSessionState) (networkSessionState, bool, error) {
	if err := store.RestoreReplacement(); err != nil {
		return fallback, true, fmt.Errorf("restore previous Network Session replacement authority: %w", err)
	}
	restored, exists, err := store.Load()
	if err != nil {
		return fallback, true, fmt.Errorf("reload restored Network Session replacement authority: %w", err)
	}
	if !exists || restored.Protection == nil || restored.Replacement != nil {
		return fallback, true, errors.New("restored Network Session replacement authority is incomplete")
	}
	return restored, true, nil
}

func reconcileProductionNetworkSessionProtection(ctx context.Context, store networkSessionStateStore) error {
	_, _, err := reconcileNetworkSessionProtection(ctx, store, netexecutor.PrivacyEnvelopeExecutor{})
	return err
}

// networkSessionBootstrapServer returns the exact concrete VPN transport
// endpoint already authorized by the current protected Network Session. It is
// intentionally limited to the matching same-boot resume session so terminal
// convergence and unrelated connects cannot reuse stale bootstrap authority.
func networkSessionBootstrapServer(store networkSessionStateStore, profileID string) (string, bool, error) {
	state, exists, err := store.Load()
	if err != nil {
		return "", false, fmt.Errorf("load Network Session bootstrap authority: %w", err)
	}
	if !exists || state.Intent != networkSessionIntentResume || state.Protection == nil {
		return "", false, nil
	}
	if strings.TrimSpace(profileID) == "" || state.Request.Mode != planner.ModeTun || state.Request.Profile.ID != profileID {
		return "", false, nil
	}
	if state.Protection.State != networkSessionProtectionArming && state.Protection.State != networkSessionProtectionArmed {
		return "", false, nil
	}
	if _, err := privacyEnvelopePlanFromAuthority(*state.Protection); err != nil {
		return "", false, fmt.Errorf("validate Network Session bootstrap authority: %w", err)
	}
	if len(state.Protection.BootstrapIPv4) == 0 {
		return "", false, errors.New("Network Session privacy authority has no bootstrap IPv4 endpoint")
	}
	return state.Protection.BootstrapIPv4[0], true, nil
}
