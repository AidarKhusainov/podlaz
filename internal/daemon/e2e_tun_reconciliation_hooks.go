package daemon

import (
	"errors"
	"os"
	"strings"
)

const (
	e2eTunReconciliationSoftFailureEnv   = "PODLAZ_E2E_TUN_RECONCILIATION_SOFT_FAILURE"
	e2eTunReconciliationSoftFailureEvent = "reconciliation-soft-provider-injected"
)

// maybeInjectE2ETunReconciliationSoftFailure is an installed-package-only seam
// behind the existing PODLAZ_E2E_TUN_HOOKS gate. It changes exactly one
// Cloudflare TLS observation into a soft failure and records only a fixed marker;
// no endpoint, profile, address, or host identity is written to artifacts.
func maybeInjectE2ETunReconciliationSoftFailure(probes []tunProbeEvidence) []tunProbeEvidence {
	if !e2eTunHooksEnabled() || !envFlagEnabled(e2eTunReconciliationSoftFailureEnv) || e2eTunHookEventRecorded(e2eTunReconciliationSoftFailureEvent) {
		return probes
	}
	copy := append([]tunProbeEvidence(nil), probes...)
	for i := range copy {
		if copy[i].Provider != "cloudflare" || copy[i].Group != "tls" {
			continue
		}
		copy[i].Success = false
		copy[i].Cause = errors.New("controlled E2E soft provider failure")
		recordE2ETunHookEvent(e2eTunReconciliationSoftFailureEvent)
		return copy
	}
	return probes
}

func envFlagEnabled(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	return value == "1" || strings.EqualFold(value, "true")
}
