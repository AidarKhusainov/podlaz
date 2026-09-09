package status

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestCleanupRequiredTerminalSessionIsUnknownNotReconnecting(t *testing.T) {
	report := Report{
		Connection: "active",
		Mode:       "tun",
		TUN:        "enabled (podlaz0)",
		StartupScan: &api.StartupScanStatus{
			Status: api.StartupScanStatusStale,
			NetworkSession: &api.NetworkSessionRecoveryState{
				Authority:         api.NetworkSessionRecoveryAuthorityPresent,
				Intent:            "disconnect",
				StartupGate:       api.NetworkSessionStartupGateOpen,
				LastResumeOutcome: api.NetworkSessionResumeOutcomeNotAttempted,
				CleanupAuthority:  api.NetworkSessionCleanupAuthoritySessionProtection,
				NextAction:        api.NetworkSessionRecoveryActionContinueTeardown,
			},
		},
	}
	report = WithTunHealth(report, &api.TunHealthStatus{
		State:             api.TunHealthCleanupRequired,
		NetworkGeneration: 3,
		Classification:    api.TunHealthOwnershipInvalid,
	})

	view := report.ProductView(nil)
	if view.State != ProductUnknown {
		t.Fatalf("terminal cleanup state=%q, want %q; report=%#v", view.State, ProductUnknown, report)
	}
	if report.ProductReconnecting {
		t.Fatalf("terminal cleanup was incorrectly published as reconnecting: %#v", report)
	}
	if !report.HasTerminalCleanup() {
		t.Fatalf("terminal cleanup authority was not preserved in typed status: %#v", report)
	}
}

func TestRevalidatingResumeSessionStillPublishesReconnecting(t *testing.T) {
	report := WithTunHealth(Report{Connection: "active", Mode: "tun", TUN: "enabled (podlaz0)"}, &api.TunHealthStatus{
		State:             api.TunHealthRevalidating,
		NetworkGeneration: 4,
		Classification:    api.TunHealthNetworkConverging,
	})
	if view := report.ProductView(nil); view.State != ProductReconnecting {
		t.Fatalf("normal active revalidation state=%q, want %q", view.State, ProductReconnecting)
	}
	if report.HasTerminalCleanup() {
		t.Fatalf("normal active revalidation was misclassified as terminal cleanup: %#v", report)
	}
}
