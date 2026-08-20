package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	e2eTunReconciliationSoftFailureEnv    = "PODLAZ_E2E_TUN_RECONCILIATION_SOFT_FAILURE"
	e2eTunReconciliationSoftFailureMarker = "reconciliation-soft-provider.injected"
)

// maybeInjectE2ETunReconciliationSoftFailure is an installed-package-only seam
// behind the existing terminal-revalidation E2E gate. It changes exactly one
// Cloudflare TLS observation into a soft failure and persists only a fixed
// one-shot marker; no endpoint, profile, address, or host identity is written.
func maybeInjectE2ETunReconciliationSoftFailure(probes []tunProbeEvidence) []tunProbeEvidence {
	marker, enabled := e2eTunReconciliationSoftFailureMarkerPath()
	if !enabled {
		return probes
	}
	if _, err := os.Stat(marker); err == nil {
		return probes
	} else if !errors.Is(err, os.ErrNotExist) {
		return probes
	}

	copy := append([]tunProbeEvidence(nil), probes...)
	for i := range copy {
		if copy[i].Provider != "cloudflare" || copy[i].Group != "tls" {
			continue
		}
		copy[i].Success = false
		copy[i].Cause = errors.New("controlled E2E soft provider failure")
		_ = os.WriteFile(marker, []byte("injected\n"), 0o600)
		return copy
	}
	return probes
}

func e2eTunReconciliationSoftFailureMarkerPath() (string, bool) {
	if !e2eTunTerminalFailureEnabled() || !envFlagEnabled(e2eTunReconciliationSoftFailureEnv) {
		return "", false
	}
	dir := strings.TrimSpace(os.Getenv(e2eTunTerminalFailureDirEnv))
	if dir == "" {
		return "", false
	}
	return filepath.Join(filepath.Clean(dir), e2eTunReconciliationSoftFailureMarker), true
}

func envFlagEnabled(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	return value == "1" || strings.EqualFold(value, "true")
}
