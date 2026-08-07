package api

import (
	"strings"
	"testing"
)

func TestValidateStatusResponseRequiresSupportedService(t *testing.T) {
	base := StatusResponse{
		Daemon:           "running",
		Service:          ServiceSystemd,
		Connection:       "inactive",
		RuntimeDirectory: "present",
		Proxy:            "inactive",
		TUN:              "disabled",
	}

	if err := ValidateStatusResponse(base); err != nil {
		t.Fatalf("valid response failed validation: %v", err)
	}

	missingService := base
	missingService.Service = ""
	if err := ValidateStatusResponse(missingService); err == nil || !strings.Contains(err.Error(), "missing service field") {
		t.Fatalf("expected missing service validation error, got %v", err)
	}

	invalidService := base
	invalidService.Service = "launchd"
	if err := ValidateStatusResponse(invalidService); err == nil || !strings.Contains(err.Error(), "invalid service field") {
		t.Fatalf("expected invalid service validation error, got %v", err)
	}
}

func TestValidateStatusResponseRestrictsActiveTransactionID(t *testing.T) {
	valid := StatusResponse{
		Daemon:              "running",
		Service:             ServiceSystemd,
		Connection:          "active",
		Mode:                "tun",
		ProfileID:           "profile-test",
		RuntimeDirectory:    "present",
		RuntimeConfigPath:   "/run/podlaz/generated/xray.json",
		ActiveTransactionID: "tun-active",
		Proxy:               "active",
		TUN:                 "enabled (podlaz0)",
	}
	if err := ValidateStatusResponse(valid); err != nil {
		t.Fatalf("valid active TUN response failed validation: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*StatusResponse)
	}{
		{name: "error state", mutate: func(status *StatusResponse) { status.Connection = "error (core exited)" }},
		{name: "non-TUN mode", mutate: func(status *StatusResponse) { status.Mode = "proxy-only" }},
		{name: "missing profile", mutate: func(status *StatusResponse) { status.ProfileID = "" }},
		{name: "missing runtime config", mutate: func(status *StatusResponse) { status.RuntimeConfigPath = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := valid
			tc.mutate(&status)
			if err := ValidateStatusResponse(status); err == nil || !strings.Contains(err.Error(), "active_transaction_id") {
				t.Fatalf("expected active_transaction_id validation error, got %v", err)
			}
		})
	}
}

func TestValidateStatusResponseValidatesInspectionWarnings(t *testing.T) {
	base := StatusResponse{
		Daemon:           "running",
		Service:          ServiceSystemd,
		Connection:       "inactive",
		RuntimeDirectory: "present",
		Proxy:            "inactive",
		TUN:              "disabled",
		InspectionWarnings: []RecoveryWarning{{
			Target:  "transaction state",
			Message: "cannot inspect transaction fixture",
		}},
	}
	if err := ValidateStatusResponse(base); err != nil {
		t.Fatalf("valid inspection warning failed validation: %v", err)
	}

	missingTarget := base
	missingTarget.InspectionWarnings = []RecoveryWarning{{Message: "cannot inspect transaction fixture"}}
	if err := ValidateStatusResponse(missingTarget); err == nil || !strings.Contains(err.Error(), "missing inspection warning target") {
		t.Fatalf("expected inspection warning target validation error, got %v", err)
	}

	missingMessage := base
	missingMessage.InspectionWarnings = []RecoveryWarning{{Target: "transaction state"}}
	if err := ValidateStatusResponse(missingMessage); err == nil || !strings.Contains(err.Error(), "missing inspection warning message") {
		t.Fatalf("expected inspection warning message validation error, got %v", err)
	}
}

func TestValidateTransactionStatusRequiresKnownState(t *testing.T) {
	valid := TransactionStatus{
		ID:                "tx-1",
		State:             "applying",
		RollbackAvailable: true,
		RequiresCleanup:   true,
		Path:              "/run/podlaz/transactions/tx-1.json",
	}
	if err := ValidateTransactionStatus(valid); err != nil {
		t.Fatalf("valid transaction status failed validation: %v", err)
	}

	invalid := valid
	invalid.State = "banana"
	if err := ValidateTransactionStatus(invalid); err == nil || !strings.Contains(err.Error(), "invalid transaction state") {
		t.Fatalf("expected invalid transaction state error, got %v", err)
	}
}

func TestServiceFromEnv(t *testing.T) {
	t.Setenv(ServiceEnv, ServiceSystemd)
	if got := ServiceFromEnv(); got != ServiceSystemd {
		t.Fatalf("expected %q service, got %q", ServiceSystemd, got)
	}

	t.Setenv(ServiceEnv, "unexpected")
	if got := ServiceFromEnv(); got != ServiceManual {
		t.Fatalf("expected unsupported service env to fall back to %q, got %q", ServiceManual, got)
	}
}
