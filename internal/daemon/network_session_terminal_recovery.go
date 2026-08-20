package daemon

import (
	"context"
	"errors"
	"fmt"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
)

// continuePersistedNetworkSessionTeardown resumes an already-declared
// disconnect/terminal convergence after exact data-plane recovery has finished.
// It never recreates protection: terminal convergence can only verify/remove the
// exact persisted envelope, prove the remaining host network, and clear session
// authority.
func continuePersistedNetworkSessionTeardown(ctx context.Context, store networkSessionStateStore) error {
	remainingNetwork := newPostPodlazNetworkVerifier()
	return continuePersistedNetworkSessionTeardownWith(
		ctx,
		store,
		netexecutor.PrivacyEnvelopeExecutor{},
		remainingNetwork.Verify,
	)
}

func continuePersistedNetworkSessionTeardownWith(
	ctx context.Context,
	store networkSessionStateStore,
	executor privacyEnvelopeLifecycleExecutor,
	verifyRemainingNetwork func(context.Context) error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	state, exists, err := store.Load()
	if err != nil {
		return fmt.Errorf("load persisted teardown authority: %w", err)
	}
	if !exists {
		return nil
	}
	if state.Intent != networkSessionIntentDisconnect && state.Intent != networkSessionIntentTerminal {
		return fmt.Errorf("persisted teardown requires disconnect/terminal intent, found %q", state.Intent)
	}

	if state.Protection != nil {
		if executor == nil {
			return errors.New("persisted teardown has no privacy envelope executor")
		}
		protection := privacyEnvelopeLifecycle{store: store, executor: executor}
		if err := protection.RemoveAfterDataPlaneCleanup(ctx); err != nil {
			return fmt.Errorf("remove exact persisted Privacy Envelope: %w", err)
		}
	}

	if verifyRemainingNetwork == nil {
		return errors.New("persisted teardown has no remaining-network verifier")
	}
	if err := verifyRemainingNetwork(ctx); err != nil {
		return fmt.Errorf("verify remaining host network after persisted teardown: %w", err)
	}

	state, exists, err = store.Load()
	if err != nil {
		return fmt.Errorf("reload persisted teardown authority before clear: %w", err)
	}
	if !exists {
		return nil
	}
	if state.Intent != networkSessionIntentDisconnect && state.Intent != networkSessionIntentTerminal {
		return fmt.Errorf("persisted teardown intent changed unexpectedly to %q", state.Intent)
	}
	if state.Protection != nil {
		return errors.New("refuse to clear persisted teardown authority while privacy protection remains")
	}
	if err := store.Remove(); err != nil {
		return fmt.Errorf("clear converged persisted teardown authority: %w", err)
	}
	return nil
}
