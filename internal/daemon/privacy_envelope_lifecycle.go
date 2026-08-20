package daemon

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type privacyEnvelopeLifecycleExecutor interface {
	PrivacyEnvelopeTableExists(context.Context, string, string) (bool, error)
	Exists(context.Context, netexecutor.PrivacyEnvelopePlan) (bool, error)
	Apply(context.Context, netexecutor.PrivacyEnvelopePlan) error
	Replace(context.Context, netexecutor.PrivacyEnvelopePlan, netexecutor.PrivacyEnvelopePlan) error
	Verify(context.Context, netexecutor.PrivacyEnvelopePlan) error
	Remove(context.Context, netexecutor.PrivacyEnvelopePlan) error
}

type privacyEnvelopeLifecycle struct {
	store    networkSessionStateStore
	executor privacyEnvelopeLifecycleExecutor
}

func (p privacyEnvelopeLifecycle) Arm(ctx context.Context, tunPlan planner.TunPlan) error {
	if p.executor == nil {
		return errors.New("privacy envelope lifecycle has no executor")
	}
	state, exists, err := p.store.Load()
	if err != nil {
		return fmt.Errorf("load network session before privacy envelope arm: %w", err)
	}
	if !exists {
		return errors.New("privacy envelope arm requires durable Network Session authority")
	}
	if state.Intent != networkSessionIntentResume {
		return fmt.Errorf("privacy envelope arm requires resume intent, found %q", state.Intent)
	}

	bootstrap, err := privacyEnvelopeBootstrapForTunPlan(tunPlan)
	if err != nil {
		return err
	}
	if state.Protection != nil {
		if state.Protection.State == networkSessionProtectionRemoving {
			return errors.New("privacy envelope is already being removed")
		}
		if state.Protection.TunInterface != tunPlan.TunDevice.Name {
			return errors.New("existing privacy envelope authority does not match the reconnect TUN interface")
		}
		if state.Replacement != nil && !reflect.DeepEqual(state.Protection.BootstrapIPv4, bootstrap) {
			return p.replaceProtection(ctx, *state.Protection, bootstrap, state.Protection.BootstrapIPv4)
		}
		if !reflect.DeepEqual(state.Protection.BootstrapIPv4, bootstrap) {
			return errors.New("existing privacy envelope authority does not match the reconnect data plane")
		}
		if _, err := privacyEnvelopePlanFromAuthority(*state.Protection); err != nil {
			return fmt.Errorf("reconstruct existing privacy envelope authority: %w", err)
		}
		return nil
	}

	observer := privacyEnvelopeLifecycleAllocationObserver{executor: p.executor}
	protection, plan, err := allocatePrivacyEnvelope(ctx, state.SessionID, tunPlan.TunDevice.Name, bootstrap, observer)
	if err != nil {
		return err
	}
	if err := p.store.SetProtection(&protection); err != nil {
		return fmt.Errorf("persist privacy envelope authority before nftables apply: %w", err)
	}
	if err := p.executor.Apply(ctx, plan); err != nil {
		return fmt.Errorf("apply privacy envelope: %w", err)
	}
	return nil
}

// PrepareReplacement widens an already verified session barrier to the exact
// union of the old and target bootstrap endpoints before the old Data Plane
// Generation is allowed to tear down. The nftables swap is one atomic batch.
func (p privacyEnvelopeLifecycle) PrepareReplacement(ctx context.Context, tunPlan planner.TunPlan) error {
	if p.executor == nil {
		return errors.New("privacy envelope lifecycle has no executor")
	}
	state, exists, err := p.store.Load()
	if err != nil {
		return fmt.Errorf("load network session before privacy replacement: %w", err)
	}
	if !exists || state.Replacement == nil || state.Protection == nil {
		return errors.New("privacy replacement requires durable previous session authority")
	}
	if state.Intent != networkSessionIntentResume {
		return fmt.Errorf("privacy replacement requires resume intent, found %q", state.Intent)
	}
	if state.Protection.State != networkSessionProtectionArmed {
		return fmt.Errorf("privacy replacement requires armed protection, found %q", state.Protection.State)
	}
	if state.Protection.TunInterface != tunPlan.TunDevice.Name {
		return errors.New("privacy replacement TUN interface does not match existing protection")
	}
	target, err := privacyEnvelopeBootstrapForTunPlan(tunPlan)
	if err != nil {
		return err
	}
	union, err := normalizePrivacyEnvelopeBootstrapIPv4(append(append([]string(nil), state.Protection.BootstrapIPv4...), target...))
	if err != nil {
		return err
	}
	if reflect.DeepEqual(union, state.Protection.BootstrapIPv4) {
		return nil
	}
	if err := p.replaceProtection(ctx, *state.Protection, union, state.Protection.BootstrapIPv4); err != nil {
		return fmt.Errorf("widen privacy envelope for protected replacement: %w", err)
	}
	if err := p.Verify(ctx); err != nil {
		return fmt.Errorf("verify widened privacy envelope for protected replacement: %w", err)
	}
	return nil
}

func (p privacyEnvelopeLifecycle) Verify(ctx context.Context) error {
	if p.executor == nil {
		return errors.New("privacy envelope lifecycle has no executor")
	}
	state, exists, err := p.store.Load()
	if err != nil {
		return fmt.Errorf("load network session before privacy envelope verify: %w", err)
	}
	if !exists || state.Protection == nil {
		return errors.New("privacy envelope verification requires durable exact authority")
	}
	if state.Protection.State == networkSessionProtectionRemoving || state.Protection.State == networkSessionProtectionUnarmed {
		return fmt.Errorf("privacy envelope cannot be verified from protection state %q", state.Protection.State)
	}
	plan, err := privacyEnvelopePlanFromAuthority(*state.Protection)
	if err != nil {
		return fmt.Errorf("reconstruct privacy envelope for verification: %w", err)
	}
	if err := p.executor.Verify(ctx, plan); err != nil {
		return err
	}
	if state.Protection.State == networkSessionProtectionArmed && len(state.Protection.PreviousBootstrapIPv4) == 0 {
		return nil
	}
	protection := cloneNetworkSessionProtection(*state.Protection)
	protection.State = networkSessionProtectionArmed
	protection.PreviousBootstrapIPv4 = nil
	if err := p.store.SetProtection(&protection); err != nil {
		return fmt.Errorf("persist verified privacy envelope state: %w", err)
	}
	return nil
}

// CleanupAfterFailedDataPlane restores the exact previous barrier and request
// for a failed protected generation replacement. It deliberately uses the same
// exact-composition convergence as restart recovery because an atomic nftables
// Replace may fail after durable narrowing intent has been persisted, leaving
// the previously verified union composition live. No candidate is mutated until
// one of the persisted old/union/target compositions verifies exactly.
func (p privacyEnvelopeLifecycle) CleanupAfterFailedDataPlane(ctx context.Context) error {
	if p.executor == nil {
		return errors.New("privacy envelope lifecycle has no executor")
	}
	state, exists, err := p.store.Load()
	if err != nil {
		return fmt.Errorf("load network session before replacement rollback: %w", err)
	}
	if !exists || state.Replacement == nil {
		return nil
	}
	_, _, err = reconcileProtectedNetworkSessionReplacement(ctx, p.store, state, p.executor)
	if err != nil {
		return fmt.Errorf("restore protected replacement after data-plane failure: %w", err)
	}
	return nil
}

func (p privacyEnvelopeLifecycle) replaceProtection(ctx context.Context, current networkSessionProtection, target, previous []string) error {
	currentPlan, err := privacyEnvelopePlanFromAuthority(current)
	if err != nil {
		return fmt.Errorf("reconstruct current privacy envelope: %w", err)
	}
	next := cloneNetworkSessionProtection(current)
	next.State = networkSessionProtectionArming
	next.BootstrapIPv4 = append([]string(nil), target...)
	next.PreviousBootstrapIPv4 = append([]string(nil), previous...)
	nextPlan, err := privacyEnvelopePlanFromAuthority(next)
	if err != nil {
		return fmt.Errorf("reconstruct replacement privacy envelope: %w", err)
	}
	if err := p.store.SetProtection(&next); err != nil {
		return fmt.Errorf("persist privacy replacement authority before nftables mutation: %w", err)
	}
	if err := p.executor.Replace(ctx, currentPlan, nextPlan); err != nil {
		return fmt.Errorf("atomically replace privacy envelope: %w", err)
	}
	return nil
}

func privacyEnvelopeBootstrapForTunPlan(tunPlan planner.TunPlan) ([]string, error) {
	bootstrapIP := tunRuntimeServerAddress(tunPlan)
	if bootstrapIP == "" {
		return nil, errors.New("privacy envelope requires the exact pre-resolved VPN bootstrap IPv4 endpoint")
	}
	bootstrap, err := normalizePrivacyEnvelopeBootstrapIPv4([]string{bootstrapIP})
	if err != nil {
		return nil, err
	}
	return bootstrap, nil
}

// RemoveAfterDataPlaneCleanup deliberately removes protection only after the
// caller has proved that exact Podlaz data-plane cleanup succeeded. A terminal
// decision may arrive while a replacement is between exact old/union/target
// compositions, so that transition is first converged back to one verified
// previous barrier. The live table is then re-verified before deletion so stale
// authority cannot delete a foreign replacement that reused the generated name.
func (p privacyEnvelopeLifecycle) RemoveAfterDataPlaneCleanup(ctx context.Context) error {
	if p.executor == nil {
		return errors.New("privacy envelope lifecycle has no executor")
	}
	state, exists, err := p.store.Load()
	if err != nil {
		return fmt.Errorf("load network session before privacy envelope removal: %w", err)
	}
	if !exists || state.Protection == nil {
		return nil
	}
	if state.Replacement != nil {
		identityPlan, err := privacyEnvelopePlanFromAuthority(*state.Protection)
		if err != nil {
			return fmt.Errorf("reconstruct replacement privacy envelope identity before removal: %w", err)
		}
		present, err := p.executor.Exists(ctx, identityPlan)
		if err != nil {
			return fmt.Errorf("observe replacement privacy envelope before terminal removal: %w", err)
		}
		if !present {
			if err := p.store.RestoreReplacement(); err != nil {
				return fmt.Errorf("clear replacement transition after proven envelope absence: %w", err)
			}
			if err := p.store.SetProtection(nil); err != nil {
				return fmt.Errorf("clear absent replacement privacy authority: %w", err)
			}
			return nil
		}
		if _, _, err := reconcileProtectedNetworkSessionReplacement(ctx, p.store, state, p.executor); err != nil {
			return fmt.Errorf("converge protected replacement before terminal envelope removal: %w", err)
		}
		state, exists, err = p.store.Load()
		if err != nil {
			return fmt.Errorf("reload network session after replacement convergence: %w", err)
		}
		if !exists || state.Protection == nil {
			return errors.New("replacement convergence lost privacy cleanup authority")
		}
	}

	plan, err := privacyEnvelopePlanFromAuthority(*state.Protection)
	if err != nil {
		return fmt.Errorf("reconstruct privacy envelope for removal: %w", err)
	}
	present, err := p.executor.Exists(ctx, plan)
	if err != nil {
		return fmt.Errorf("observe exact privacy envelope before removal: %w", err)
	}
	if !present {
		if err := p.store.SetProtection(nil); err != nil {
			return fmt.Errorf("clear absent privacy envelope authority: %w", err)
		}
		return nil
	}
	if err := p.executor.Verify(ctx, plan); err != nil {
		return fmt.Errorf("refuse to remove unverified privacy envelope: %w", err)
	}

	protection := cloneNetworkSessionProtection(*state.Protection)
	protection.State = networkSessionProtectionRemoving
	if err := p.store.SetProtection(&protection); err != nil {
		return fmt.Errorf("persist privacy envelope removal intent: %w", err)
	}
	if err := p.executor.Remove(ctx, plan); err != nil {
		return err
	}
	present, err = p.executor.Exists(ctx, plan)
	if err != nil {
		return fmt.Errorf("verify privacy envelope absence after removal: %w", err)
	}
	if present {
		return errors.New("privacy envelope still exists after deliberate removal")
	}
	if err := p.store.SetProtection(nil); err != nil {
		return fmt.Errorf("clear removed privacy envelope authority: %w", err)
	}
	return nil
}

type privacyEnvelopeLifecycleAllocationObserver struct {
	executor privacyEnvelopeLifecycleExecutor
}

func (o privacyEnvelopeLifecycleAllocationObserver) PrivacyEnvelopeTableExists(ctx context.Context, family, table string) (bool, error) {
	return o.executor.PrivacyEnvelopeTableExists(ctx, family, table)
}
