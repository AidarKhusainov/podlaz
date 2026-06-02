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
