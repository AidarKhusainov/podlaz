package daemon

import "testing"

func TestE2ETunReconciliationSoftFailureIsGatedAndOneShot(t *testing.T) {
	t.Setenv(e2eTunHookGateEnv, "true")
	t.Setenv(e2eTunHookDirEnv, t.TempDir())
	t.Setenv(e2eTunReconciliationSoftFailureEnv, "true")

	base := []tunProbeEvidence{
		{Group: "dns-udp", Provider: "session-resolver", Success: true},
		{Group: "tls", Provider: "cloudflare", Success: true},
		{Group: "https", Provider: "google", Success: true},
	}
	first := maybeInjectE2ETunReconciliationSoftFailure(base)
	if first[1].Success || first[1].Cause == nil {
		t.Fatalf("first controlled Cloudflare TLS observation was not failed: %#v", first[1])
	}
	if !first[0].Success || !first[2].Success {
		t.Fatalf("independent evidence changed unexpectedly: %#v", first)
	}
	if !base[1].Success {
		t.Fatal("E2E injection mutated caller-owned evidence in place")
	}

	second := maybeInjectE2ETunReconciliationSoftFailure(base)
	if !second[1].Success || second[1].Cause != nil {
		t.Fatalf("controlled soft failure was injected more than once: %#v", second[1])
	}
}

func TestE2ETunReconciliationSoftFailureRequiresExistingGate(t *testing.T) {
	t.Setenv(e2eTunHookGateEnv, "false")
	t.Setenv(e2eTunReconciliationSoftFailureEnv, "true")
	probes := []tunProbeEvidence{{Group: "tls", Provider: "cloudflare", Success: true}}
	got := maybeInjectE2ETunReconciliationSoftFailure(probes)
	if !got[0].Success || got[0].Cause != nil {
		t.Fatalf("soft-failure hook escaped the existing E2E gate: %#v", got[0])
	}
}
