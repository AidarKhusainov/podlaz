package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestE2ETunReconciliationSoftFailureIsGatedTriggeredAndOneShot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(e2eTunTerminalFailureEnv, "true")
	t.Setenv(e2eTunTerminalFailureDirEnv, dir)
	t.Setenv(e2eTunReconciliationSoftFailureEnv, "true")

	base := []tunProbeEvidence{
		{Group: "dns-udp", Provider: "session-resolver", Success: true},
		{Group: "tls", Provider: "cloudflare", Success: true},
		{Group: "https", Provider: "google", Success: true},
	}
	if got := maybeInjectE2ETunReconciliationSoftFailure(base); !got[1].Success {
		t.Fatal("soft failure injected before trigger marker existed")
	}
	if err := os.WriteFile(filepath.Join(dir, e2eTunReconciliationSoftFailureTrigger), []byte("trigger\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := maybeInjectE2ETunReconciliationSoftFailure(base)
	if first[1].Success || first[1].Cause == nil {
		t.Fatalf("controlled Cloudflare TLS observation was not failed: %#v", first[1])
	}
	if !first[0].Success || !first[2].Success || !base[1].Success {
		t.Fatalf("independent or caller-owned evidence changed unexpectedly: first=%#v base=%#v", first, base)
	}
	if _, err := os.Stat(filepath.Join(dir, e2eTunReconciliationSoftFailureInjected)); err != nil {
		t.Fatalf("injected marker missing: %v", err)
	}
	second := maybeInjectE2ETunReconciliationSoftFailure(base)
	if !second[1].Success || second[1].Cause != nil {
		t.Fatalf("controlled soft failure was injected more than once: %#v", second[1])
	}
}

func TestE2ETunReconciliationResolvedUnknownIsOneShot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(e2eTunTerminalFailureEnv, "true")
	t.Setenv(e2eTunTerminalFailureDirEnv, dir)
	t.Setenv(e2eTunReconciliationResolvedUnknownEnv, "true")
	if err := os.WriteFile(filepath.Join(dir, e2eTunReconciliationResolvedUnknownTrig), []byte("trigger\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evidence := issue262ProvenMandatoryEvidence()
	first := maybeInjectE2ETunReconciliationResolvedUnknown(evidence)
	if first.ResolvedDNS != tunLocalProofUnknown {
		t.Fatalf("resolved state = %v, want unknown", first.ResolvedDNS)
	}
	second := maybeInjectE2ETunReconciliationResolvedUnknown(evidence)
	if second.ResolvedDNS != tunLocalProofProven {
		t.Fatalf("resolved state was injected twice: %v", second.ResolvedDNS)
	}
}

func TestE2ETunReconciliationRebuildPauseRequiresExplicitContinue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(e2eTunTerminalFailureEnv, "true")
	t.Setenv(e2eTunTerminalFailureDirEnv, dir)
	t.Setenv(e2eTunReconciliationRebuildPauseEnv, "true")
	t.Setenv(e2eTunHookTimeoutSecondsEnv, "2")

	done := make(chan error, 1)
	go func() { done <- maybePauseE2ETunReconciliationRebuild(context.Background()) }()
	ready := filepath.Join(dir, e2eTunReconciliationRebuildReady)
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rebuild ready marker did not appear")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, e2eTunReconciliationRebuildContinue), []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("rebuild pause failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("rebuild pause did not resume")
	}
}

func TestE2ETunReconciliationHooksRequireExistingGate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(e2eTunTerminalFailureEnv, "false")
	t.Setenv(e2eTunTerminalFailureDirEnv, dir)
	t.Setenv(e2eTunReconciliationSoftFailureEnv, "true")
	_ = os.WriteFile(filepath.Join(dir, e2eTunReconciliationSoftFailureTrigger), []byte("trigger\n"), 0o600)
	probes := []tunProbeEvidence{{Group: "tls", Provider: "cloudflare", Success: true}}
	got := maybeInjectE2ETunReconciliationSoftFailure(probes)
	if !got[0].Success || got[0].Cause != nil {
		t.Fatalf("soft-failure hook escaped the existing E2E gate: %#v", got[0])
	}
}
