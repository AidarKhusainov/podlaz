package daemon

import (
	"context"
	"testing"
)

func TestConfigurePrivacyEnvelopeBindsSessionLifecycleToProtectedRunner(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, nil)
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	executor := &privacyEnvelopeExecutorStub{}
	runner := &fullTunnelTransactionRunner{}

	if err := configurePrivacyEnvelopeWithExecutor(runtimeDir, runner, executor); err != nil {
		t.Fatalf("configure privacy lifecycle: %v", err)
	}
	if !runner.requirePrivacyEnvelope {
		t.Fatal("production TUN runner must require Privacy Envelope")
	}
	if runner.armPrivacyEnvelope == nil || runner.verifyPrivacyEnvelope == nil || runner.cleanupPrivacyEnvelope == nil {
		t.Fatal("production TUN runner must receive complete Privacy Envelope lifecycle hooks")
	}

	plan := privacyLifecycleTunPlanForTest()
	if err := runner.armPrivacyEnvelope(context.Background(), plan); err != nil {
		t.Fatalf("configured arm hook: %v", err)
	}
	state, exists, err := store.Load()
	if err != nil || !exists || state.Protection == nil {
		t.Fatalf("configured arm hook did not persist exact authority: exists=%v state=%#v err=%v", exists, state, err)
	}
}

func TestXrayManagerProductionPrivacyConfigurationRequiresProtection(t *testing.T) {
	manager := &XrayManager{RuntimeDir: t.TempDir()}
	runner := &fullTunnelTransactionRunner{}
	if err := manager.configurePrivacyEnvelope(runner); err != nil {
		t.Fatalf("configure default production privacy lifecycle: %v", err)
	}
	if !runner.requirePrivacyEnvelope || runner.armPrivacyEnvelope == nil || runner.verifyPrivacyEnvelope == nil || runner.cleanupPrivacyEnvelope == nil {
		t.Fatalf("default production privacy configuration is incomplete: %#v", runner)
	}
}
