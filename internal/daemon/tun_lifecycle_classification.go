package daemon

import (
	"context"
	"errors"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func appendTunLifecycleFailureProbe(report tundiag.Report, phase string, cause error) tundiag.Report {
	classification := tunLifecycleFailureClassification(phase, cause)
	if classification == "" {
		classification = tundiag.ClassInternalDiagnosticError
	}
	id := "lifecycle-failure"
	if trimmed := strings.TrimSpace(phase); trimmed != "" {
		id += "-" + trimmed
	}
	if _, exists := report.Probe(id); exists {
		return report
	}
	report.Probes = append(report.Probes, tundiag.ProbeResult{
		ID:             id,
		Layer:          tundiag.LayerSession,
		Status:         tundiag.ProbeFail,
		Classification: classification,
		Error:          "TUN lifecycle failed before commit",
	})
	return report
}

func tunLifecycleFailureClassification(phase string, cause error) tundiag.Classification {
	switch {
	case errors.Is(cause, context.Canceled):
		return tundiag.ClassCancelled
	case errors.Is(cause, context.DeadlineExceeded):
		return tundiag.ClassTimeout
	}
	switch strings.TrimSpace(phase) {
	case "network-apply":
		return tundiag.ClassNetworkApplyFailure
	case "network-verify":
		return tundiag.ClassNetworkVerifyFailure
	default:
		return tundiag.ClassInternalDiagnosticError
	}
}
