package daemon

import (
	"context"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
)

func TestXrayManagerConfiguresProtectedTunRunnerWithSessionPrivacyLifecycle(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	executor := &privacyEnvelopeExecutorStub{}
	manager := &XrayManager{RuntimeDir: runtimeDir, privacyExecutor: executor}
	runner := &fullTunnelTransactionRunner{}

	if err := manager.configurePrivacyEnvelope(runner); err != nil {
		t.Fatalf("configure production privacy lifecycle: %v", err)
	}
	if !runner.requirePrivacyEnvelope {
		t.Fatal("production TUN runner must require Privacy Envelope")
	}
	if runner.armPrivacyEnvelope == nil || runner.verifyPrivacyEnvelope == nil || runner.cleanupPrivacyEnvelope == nil {
		t.Fatal("production TUN runner must receive complete Privacy Envelope lifecycle hooks")
	}

	plan := privacyLifecycleTunPlanForTest()
	if err := runner.armPrivacyEnvelope(context.Background(), plan); err != nil {
		t.Fatalf("production arm hook: %v", err)
	}
	state, exists, err := store.Load()
	if err != nil || !exists || state.Protection == nil {
		t.Fatalf("production arm hook did not persist exact authority: exists=%v state=%#v err=%v", exists, state, err)
	}
}

func TestXrayManagerProductionPrivacyExecutorUsesOSRunnerByDefault(t *testing.T) {
	manager := &XrayManager{RuntimeDir: t.TempDir()}
	runner := &fullTunnelTransactionRunner{}
	if err := manager.configurePrivacyEnvelope(runner); err != nil {
		t.Fatalf("configure default production privacy lifecycle: %v", err)
	}
	if !runner.requirePrivacyEnvelope {
		t.Fatal("default production runner must require privacy protection")
	}
	if _, ok := manager.privacyEnvelopeExecutor().(netexecutor.PrivacyEnvelopeExecutor); !ok {
		t.Fatalf("default production privacy executor has unexpected type %T", manager.privacyEnvelopeExecutor())
	}
}
