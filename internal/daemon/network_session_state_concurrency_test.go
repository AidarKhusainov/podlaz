package daemon

import (
	"testing"
	"time"
)

func TestNetworkSessionStateMutationsSerializeIntentAndProtection(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}

	protection := networkSessionProtection{
		State:              networkSessionProtectionArming,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}

	protectionReadEntered := make(chan struct{})
	releaseProtectionRead := make(chan struct{})
	protectionStore := newNetworkSessionStateStore(runtimeDir, func() (string, error) {
		select {
		case <-protectionReadEntered:
		default:
			close(protectionReadEntered)
			<-releaseProtectionRead
		}
		return "boot-a", nil
	})

	protectionDone := make(chan error, 1)
	go func() {
		protectionDone <- protectionStore.SetProtection(&protection)
	}()
	<-protectionReadEntered

	intentDone := make(chan error, 1)
	go func() {
		intentDone <- store.SetIntent(networkSessionIntentDisconnect)
	}()

	intentCompletedWhileProtectionHeldStaleState := false
	var earlyIntentErr error
	select {
	case earlyIntentErr = <-intentDone:
		intentCompletedWhileProtectionHeldStaleState = true
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseProtectionRead)
	if err := <-protectionDone; err != nil {
		t.Fatalf("persist privacy protection: %v", err)
	}
	if !intentCompletedWhileProtectionHeldStaleState {
		if err := <-intentDone; err != nil {
			t.Fatalf("persist disconnect intent: %v", err)
		}
	} else if earlyIntentErr != nil {
		t.Fatalf("persist disconnect intent: %v", earlyIntentErr)
	}

	state, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load final network session state: exists=%v err=%v", exists, err)
	}
	if intentCompletedWhileProtectionHeldStaleState {
		t.Fatalf("disconnect intent mutation completed while SetProtection held a stale resume snapshot; final state=%#v", state)
	}
	if state.Intent != networkSessionIntentDisconnect {
		t.Fatalf("concurrent protection persistence resurrected resume intent: %q", state.Intent)
	}
	if state.Protection == nil || state.Protection.Table != protection.Table {
		t.Fatalf("concurrent intent persistence lost privacy authority: %#v", state.Protection)
	}
}
