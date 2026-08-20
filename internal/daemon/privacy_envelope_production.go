package daemon

import (
	"context"
	"errors"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
)

func (m *XrayManager) configurePrivacyEnvelope(runner *fullTunnelTransactionRunner) error {
	return configurePrivacyEnvelopeWithExecutor(m.runtimeDir(), runner, netexecutor.PrivacyEnvelopeExecutor{})
}

func configurePrivacyEnvelopeWithExecutor(runtimeDir string, runner *fullTunnelTransactionRunner, executor privacyEnvelopeLifecycleExecutor) error {
	if runner == nil {
		return errors.New("cannot configure Privacy Envelope on a nil TUN runner")
	}
	if executor == nil {
		return errors.New("cannot configure Privacy Envelope without an executor")
	}
	lifecycle := privacyEnvelopeLifecycle{
		store:    newNetworkSessionStateStore(runtimeDir, nil),
		executor: executor,
	}
	runner.requirePrivacyEnvelope = true
	runner.armPrivacyEnvelope = lifecycle.Arm
	runner.verifyPrivacyEnvelope = lifecycle.Verify
	runner.cleanupPrivacyEnvelope = func(ctx context.Context) error {
		state, exists, err := lifecycle.store.Load()
		if err != nil {
			return err
		}
		if exists && state.Replacement != nil {
			return lifecycle.CleanupAfterFailedDataPlane(ctx)
		}
		return lifecycle.RemoveAfterDataPlaneCleanup(ctx)
	}
	return nil
}
