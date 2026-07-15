package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestTunDiagnosticsHandlerReturnsVersionedUnavailableReport(t *testing.T) {
	mux := http.NewServeMux()
	registerTunDiagnosticsHandler(mux, NewXrayManager(t.TempDir()))

	request := httptest.NewRequest(http.MethodGet, api.TunDoctorPath, nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var report tundiag.Report
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != tundiag.SchemaVersion || report.Status != tundiag.StatusUnavailable {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.PrimaryClassification != tundiag.ClassSessionInactive {
		t.Fatalf("unexpected classification: %q", report.PrimaryClassification)
	}
}

func TestTunDiagnosticsHandlerRejectsMutationMethods(t *testing.T) {
	mux := http.NewServeMux()
	registerTunDiagnosticsHandler(mux, NewXrayManager(t.TempDir()))

	request := httptest.NewRequest(http.MethodPost, api.TunDoctorPath, nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed, got %d", response.Code)
	}
}
