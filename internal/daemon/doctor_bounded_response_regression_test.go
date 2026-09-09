package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestDoctorHandlerReturnsTypedIncompleteBeforeClientDeadline(t *testing.T) {
	runtimeDir := t.TempDir()
	manager := NewXrayManager(runtimeDir)
	runtime := &daemonRuntime{
		runtimeDir:    runtimeDir,
		lifecycle:     manager,
		authorizer:    AllowAuthorizer{},
		operationLock: newLifecycleOperationLock(),
		currentStatus: func(context.Context) api.StatusResponse {
			return api.StatusResponse{
				Daemon:           "running",
				Connection:       "inactive",
				RuntimeDirectory: "present",
				Proxy:            "inactive",
				TUN:              "disabled",
			}
		},
	}

	doctorEntered := make(chan struct{})
	server := (Server{Doctor: func(ctx context.Context) api.DoctorResponse {
		close(doctorEntered)
		<-ctx.Done()
		return api.DoctorResponse{
			Source: api.DoctorSourceDaemon,
			Checks: []api.DoctorCheck{{
				Name:     "resolver-inspection",
				Severity: "WARN",
				Message:  "resolver inspection incomplete: " + ctx.Err().Error(),
			}},
		}
	}}).newHTTPServer(runtime, newBootAutostartManifestStore(t.TempDir(), fixedBootID("boot-a")))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, api.DoctorPath, nil)
	started := time.Now()
	server.Handler.ServeHTTP(recorder, request)
	elapsed := time.Since(started)

	select {
	case <-doctorEntered:
	default:
		t.Fatal("daemon doctor function was not invoked")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("doctor status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if elapsed >= 700*time.Millisecond {
		t.Fatalf("daemon doctor response exceeded bounded response margin: %s", elapsed)
	}
	if elapsed < 400*time.Millisecond {
		t.Fatalf("doctor fixture did not exercise the server-side observation budget: %s", elapsed)
	}

	var response api.DoctorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode doctor response: %v", err)
	}
	if err := api.ValidateDoctorResponse(response); err != nil {
		t.Fatalf("bounded doctor response is invalid: %v; response=%#v", err, response)
	}
	foundIncomplete := false
	for _, check := range response.Checks {
		if check.Name == "resolver-inspection" && check.Severity == "WARN" {
			foundIncomplete = true
			break
		}
	}
	if !foundIncomplete {
		t.Fatalf("bounded doctor response lost typed incomplete resolver evidence: %#v", response.Checks)
	}
}
