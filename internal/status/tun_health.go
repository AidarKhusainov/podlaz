package status

import (
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

// WithTunHealth projects the daemon's current TUN evidence into the existing
// diagnostic status model while also retaining a typed product-level signal.
// Durable terminal Network Session intent is authoritative even when current
// health observation is unavailable.
func WithTunHealth(report Report, health *api.TunHealthStatus) Report {
	if report.HasTerminalCleanup() {
		report.ProductReconnecting = false
		report.Connection = "unknown (terminal cleanup incomplete)"
		return report
	}
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
	report.ProductReconnecting = report.Connection == "active" && health.State != api.TunHealthVerified
	if report.ProductReconnecting {
		report.Connection = fmt.Sprintf("active (%s: %s)", health.State, health.Classification)
	}
	return report
}

// HasTerminalCleanup reports durable current-boot Network Session teardown work.
// It is deliberately separate from generic stale-candidate/warning classification:
// terminal session authority is a typed lifecycle condition even when there are
// no standalone recovery candidates or inspection warnings.
func (r Report) HasTerminalCleanup() bool {
	if r.StartupScan == nil || r.StartupScan.NetworkSession == nil {
		return false
	}
	session := r.StartupScan.NetworkSession
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
