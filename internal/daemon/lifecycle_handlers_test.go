package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
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
