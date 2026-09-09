package status

import (
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

// WithTunHealth projects the daemon's current TUN evidence into the existing
// diagnostic status model while also retaining a typed product-level signal.
// The detailed API/report fields remain unchanged for doctor/recovery tooling.
func WithTunHealth(report Report, health *api.TunHealthStatus) Report {
	if health == nil {
		return report
	}
	parts := []string{
		strings.TrimSpace(report.TUN),
		fmt.Sprintf("current health=%s", health.State),
		fmt.Sprintf("network generation=%d", health.NetworkGeneration),
	}
	if health.Classification != "" {
		parts = append(parts, fmt.Sprintf("classification=%s", health.Classification))
	}
	nonEmpty := parts[:0]
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	report.TUN = strings.Join(nonEmpty, "; ")
	if terminalCleanupRequired(report, health) {
		report.ProductReconnecting = false
		report.Connection = "unknown (terminal cleanup incomplete)"
		return report
	}
	report.ProductReconnecting = report.Connection == "active" && health.State != api.TunHealthVerified
	if report.ProductReconnecting {
		report.Connection = fmt.Sprintf("active (%s: %s)", health.State, health.Classification)
	}
	return report
}

func terminalCleanupRequired(report Report, health *api.TunHealthStatus) bool {
	if health == nil || health.State != api.TunHealthCleanupRequired || report.StartupScan == nil || report.StartupScan.NetworkSession == nil {
		return false
	}
	session := report.StartupScan.NetworkSession
	if session.NextAction != api.NetworkSessionRecoveryActionContinueTeardown {
		return false
	}
	switch strings.TrimSpace(session.Intent) {
	case "disconnect", "terminal":
		return true
	default:
		return false
	}
}
