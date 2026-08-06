package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestLifecycleConnectHandlerRejectsMalformedRequest(t *testing.T) {
	mux := http.NewServeMux()
	registerLifecycleHandlers(mux, NewXrayManager(t.TempDir()))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, api.ConnectPath, strings.NewReader(`{"mode":"proxy-only","profile":{"id":"test"},"unexpected":true}`))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d with body %q", http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "invalid JSON request body") {
		t.Fatalf("expected invalid JSON message, got %q", recorder.Body.String())
	}
}

func TestLifecycleConnectHandlerRejectsUnsupportedMode(t *testing.T) {
	mux := http.NewServeMux()
	registerLifecycleHandlers(mux, NewXrayManager(t.TempDir()))

	req := connectRequestForTest()
	req.Mode = "wireguard"
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, api.ConnectPath, bytes.NewReader(body))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d with body %q", http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "unsupported connect mode") {
		t.Fatalf("expected unsupported mode message, got %q", recorder.Body.String())
	}
}

func TestLifecycleConnectHandlerDelegatesReplacePodlazActiveTUNToLifecycle(t *testing.T) {
	lifecycle := &recordingLifecycle{status: api.StatusResponse{Connection: "active", Mode: planner.ModeTun, TUN: "enabled"}}
	mux := http.NewServeMux()
	registerLifecycleHandlers(mux, lifecycle)

	req := connectRequestForTest()
	req.Mode = planner.ModeTun
	req.Handoff = api.HandoffReplacePodlaz
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, api.ConnectPath, bytes.NewReader(body))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d with body %q", http.StatusOK, recorder.Code, recorder.Body.String())
	}
	if !lifecycle.called {
		t.Fatal("expected lifecycle.Connect to receive replace-podlaz request")
	}
	if lifecycle.request.Mode != planner.ModeTun || lifecycle.request.Handoff != api.HandoffReplacePodlaz {
		t.Fatalf("unexpected delegated request: %#v", lifecycle.request)
	}
}

func TestLifecycleConnectHandlerLogsSanitizedFailureSummary(t *testing.T) {
	var logs bytes.Buffer
	oldOutput := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldOutput)
		log.SetFlags(oldFlags)
	})

	mux := http.NewServeMux()
	failure := withTunFailurePhase("network-apply", "tun-20260709T120000Z", "completed", errors.New("raw detail vpn.example.test 203.0.113.10 must not be logged"))
	registerLifecycleHandlers(mux, failingLifecycle{err: failure})

	req := connectRequestForTest()
	req.Mode = "tun"
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, api.ConnectPath, bytes.NewReader(body))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d with body %q", http.StatusInternalServerError, recorder.Code, recorder.Body.String())
	}
	logged := logs.String()
	for _, want := range []string{"connect request failed", "mode=tun", "phase=network-apply", "transaction_id=tun-20260709T120000Z", "rollback_status=completed"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected log to contain %q, got:\n%s", want, logged)
		}
	}
	for _, forbidden := range []string{"vpn.example.test", "203.0.113.10", "raw detail"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("connect failure log leaked %q:\n%s", forbidden, logged)
		}
	}
}

type recordingLifecycle struct {
	status  api.StatusResponse
	request api.ConnectRequest
	called  bool
}

func (r *recordingLifecycle) Connect(_ context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	r.called = true
	r.request = request
	return api.LifecycleResponse{Connection: "active", Mode: request.Mode, Proxy: "inactive", TUN: "enabled"}, nil
}

func (r *recordingLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

func (r *recordingLifecycle) Status(context.Context) api.StatusResponse {
	return r.status
}

type failingLifecycle struct {
	err error
}

func (f failingLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, f.err
}

func (f failingLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}
