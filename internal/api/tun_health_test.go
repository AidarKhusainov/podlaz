package api

import "testing"

func TestValidateStatusResponseAcceptsTypedActiveTunHealth(t *testing.T) {
	status := StatusResponse{
		Daemon:            "running",
		Service:           ServiceSystemd,
		Connection:        "active",
		Mode:              "tun",
		ProfileID:         "profile-test",
		RuntimeDirectory:  "present",
		RuntimeConfigPath: "/run/podlaz/generated/xray.json",
		Proxy:             "active",
		TUN:               "enabled (podlaz0)",
		TunHealth: &TunHealthStatus{
			State:             TunHealthVerified,
			NetworkGeneration: 1,
		},
	}
	if err := ValidateStatusResponse(status); err != nil {
		t.Fatalf("valid TUN health failed validation: %v", err)
	}
}

func TestValidateStatusResponseRejectsInvalidTunHealth(t *testing.T) {
	base := StatusResponse{
		Daemon:            "running",
		Service:           ServiceSystemd,
		Connection:        "active",
		Mode:              "tun",
		ProfileID:         "profile-test",
		RuntimeDirectory:  "present",
		RuntimeConfigPath: "/run/podlaz/generated/xray.json",
		Proxy:             "active",
		TUN:               "enabled (podlaz0)",
		TunHealth: &TunHealthStatus{
			State:             TunHealthVerified,
			NetworkGeneration: 1,
		},
	}

	tests := []struct {
		name   string
		mutate func(*StatusResponse)
	}{
		{name: "inactive connection", mutate: func(s *StatusResponse) { s.Connection = "inactive" }},
		{name: "non TUN mode", mutate: func(s *StatusResponse) { s.Mode = "proxy-only" }},
		{name: "zero generation", mutate: func(s *StatusResponse) { s.TunHealth.NetworkGeneration = 0 }},
		{name: "unknown state", mutate: func(s *StatusResponse) { s.TunHealth.State = "mystery" }},
		{name: "degraded without classification", mutate: func(s *StatusResponse) {
			s.TunHealth.State = TunHealthDegraded
			s.TunHealth.Classification = ""
		}},
		{name: "verified with failure classification", mutate: func(s *StatusResponse) {
			s.TunHealth.State = TunHealthVerified
			s.TunHealth.Classification = TunHealthOwnedStateInvalid
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status := base
			health := *base.TunHealth
			status.TunHealth = &health
			tc.mutate(&status)
			if err := ValidateStatusResponse(status); err == nil {
				t.Fatal("expected invalid TUN health to fail validation")
			}
		})
	}
}

func TestTunHealthClassificationsAreStable(t *testing.T) {
	classifications := []TunHealthClassification{
		TunHealthUplinkRevalidating,
		TunHealthUplinkChanged,
		TunHealthUplinkFingerprintUnavailable,
		TunHealthOwnershipInvalid,
		TunHealthOwnedStateInvalid,
		TunHealthConnectivityFailed,
		TunHealthRevalidationTimeout,
		TunHealthRevalidationInterrupted,
	}
	seen := make(map[TunHealthClassification]struct{}, len(classifications))
	for _, classification := range classifications {
		if classification == "" {
			t.Fatal("classification must not be empty")
		}
		if _, exists := seen[classification]; exists {
			t.Fatalf("duplicate classification %q", classification)
		}
		seen[classification] = struct{}{}
	}
}
