package daemon

import (
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
	runner.cleanupPrivacyEnvelope = lifecycle.RemoveAfterDataPlaneCleanup
	return nil
}
