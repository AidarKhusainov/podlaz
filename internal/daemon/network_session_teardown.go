package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type networkSessionTeardownReason string

const (
	networkSessionTeardownExplicit networkSessionTeardownReason = "explicit"
	networkSessionTeardownTerminal networkSessionTeardownReason = "terminal"
	networkSessionTeardownRestart  networkSessionTeardownReason = "restart"
)

type networkSessionTeardownCoordinator struct {
	store                  networkSessionStateStore
	cleanupDataPlane       func(context.Context) (api.LifecycleResponse, error)
	removeProtection       func(context.Context) error
	verifyRemainingNetwork func(context.Context) error
}

func (c networkSessionTeardownCoordinator) Teardown(ctx context.Context, reason networkSessionTeardownReason) (api.LifecycleResponse, error) {
	if c.cleanupDataPlane == nil {
		return api.LifecycleResponse{}, errors.New("network session teardown has no data-plane cleanup")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	state, exists, err := c.store.Load()
	if err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("load Network Session before teardown: %w", err)
	}
	if reason == networkSessionTeardownRestart {
		if exists && state.Intent != networkSessionIntentResume {
			return api.LifecycleResponse{}, fmt.Errorf("restart teardown requires resume intent, found %q", state.Intent)
		}
		return c.cleanupDataPlane(ctx)
	}

	intent, err := teardownIntent(reason)
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	if exists {
		if err := persistTeardownIntent(c.store, state.Intent, intent); err != nil {
			return api.LifecycleResponse{}, fmt.Errorf("persist Network Session teardown intent: %w", err)
		}
	}

	response, err := c.cleanupDataPlane(ctx)
	if err != nil {
		return response, fmt.Errorf("clean exact Podlaz data plane behind privacy protection: %w", err)
	}

	state, exists, err = c.store.Load()
	if err != nil {
		return response, fmt.Errorf("reload Network Session after data-plane cleanup: %w", err)
	}
	if exists && state.Protection != nil {
		if c.removeProtection == nil {
			return response, errors.New("network session teardown has no privacy protection cleanup")
		}
		if err := c.removeProtection(ctx); err != nil {
			return response, fmt.Errorf("remove exact Privacy Envelope after data-plane cleanup: %w", err)
		}
	}

	if c.verifyRemainingNetwork == nil {
		return response, errors.New("network session teardown has no post-Podlaz network verifier")
	}
	if err := c.verifyRemainingNetwork(ctx); err != nil {
		return response, fmt.Errorf("verify remaining host network after Podlaz teardown: %w", err)
	}

	state, exists, err = c.store.Load()
	if err != nil {
		return response, fmt.Errorf("reload Network Session before final authority clear: %w", err)
	}
	if exists {
		if state.Protection != nil {
			return response, errors.New("refuse to clear Network Session authority while privacy protection remains")
		}
		if err := c.store.Remove(); err != nil {
			return response, fmt.Errorf("clear converged Network Session authority: %w", err)
		}
	}
	return response, nil
}

func teardownIntent(reason networkSessionTeardownReason) (networkSessionIntent, error) {
	switch reason {
	case networkSessionTeardownExplicit:
		return networkSessionIntentDisconnect, nil
	case networkSessionTeardownTerminal:
		return networkSessionIntentTerminal, nil
	default:
		return "", fmt.Errorf("unsupported Network Session teardown reason %q", reason)
	}
}

func persistTeardownIntent(store networkSessionStateStore, current, desired networkSessionIntent) error {
	if current == desired || current == networkSessionIntentTerminal {
		return nil
	}
	if current == networkSessionIntentDisconnect && desired == networkSessionIntentTerminal {
		return store.SetIntent(networkSessionIntentTerminal)
	}
	if current != networkSessionIntentResume {
		return fmt.Errorf("cannot change Network Session intent from %q to %q", current, desired)
	}
	return store.SetIntent(desired)
}

func guardNetworkSessionCleanupStatus(store networkSessionStateStore, status api.StatusResponse) api.StatusResponse {
	if status.Connection != "inactive" {
		return status
	}
	state, exists, err := store.Load()
	if err == nil && !exists {
		return status
	}

	status.ActiveTransactionID = ""
	status.TunHealth = nil
	status.Connection = "error (network session cleanup pending)"
	warning := api.RecoveryWarning{
		Target:  "network-session cleanup",
		Message: "clean disconnected state is not proven while exact Network Session cleanup authority remains",
	}
	if err != nil {
		warning.Message = "Network Session cleanup authority could not be read; clean disconnected state is not proven"
	} else if state.Protection != nil {
		warning.Message = "Privacy Envelope cleanup is still pending; clean disconnected state is not proven"
	}
	status.InspectionWarnings = append(status.InspectionWarnings, warning)
	return status
}

type networkSessionTerminalContextKey struct{}

func withTerminalNetworkSessionTeardown(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, networkSessionTerminalContextKey{}, true)
}

func isTerminalNetworkSessionTeardown(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(networkSessionTerminalContextKey{}).(bool)
	return value
}
