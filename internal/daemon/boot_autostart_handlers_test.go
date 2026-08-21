package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type recordingAutostartAuthorizer struct {
	calls  int
	action AuthorizationAction
	err    error
}

func (a *recordingAutostartAuthorizer) Authorize(context.Context, AuthorizationAction, PeerSubject) error {
	a.calls++
	return a.err
}

func (a *recordingAutostartAuthorizer) RequiresPeerCredentials() bool { return false }

func TestBootAutostartHandlersConfigureStatusAndDisable(t *testing.T) {
	store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	authorizer := &recordingAutostartAuthorizer{}
	mux := http.NewServeMux()
	registerBootAutostartHandlers(mux, store, authorizer)

	config := testBootAutostartConfig()
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, api.AutostartConfigurePath, bytesReader(body))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("configure status = %d, body = %q", res.Code, res.Body.String())
	}
	if authorizer.calls != 1 || authorizer.action != ActionConfigureAutostart {
		t.Fatalf("configure authorization = calls %d action %q", authorizer.calls, authorizer.action)
	}
	var enabled api.AutostartStatusResponse
	if err := json.NewDecoder(res.Body).Decode(&enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled || enabled.Mode != config.Mode || enabled.ProfileName != config.Profile.Name {
		t.Fatalf("configure response = %+v", enabled)
	}

	authorizer.calls = 0
	res = httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, api.AutostartStatusPath, nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %q", res.Code, res.Body.String())
	}
	if authorizer.calls != 0 {
		t.Fatalf("read-only status unexpectedly requested mutation authorization %d times", authorizer.calls)
	}
	var status api.AutostartStatusResponse
	if err := json.NewDecoder(res.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status != enabled {
		t.Fatalf("status response = %+v, want %+v", status, enabled)
	}

	res = httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, api.AutostartConfigurePath, nil))
	if res.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %q", res.Code, res.Body.String())
	}
	if authorizer.calls != 1 || authorizer.action != ActionConfigureAutostart {
		t.Fatalf("disable authorization = calls %d action %q", authorizer.calls, authorizer.action)
	}
	var disabled api.AutostartStatusResponse
	if err := json.NewDecoder(res.Body).Decode(&disabled); err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled {
		t.Fatalf("disable response = %+v", disabled)
	}
}

func TestBootAutostartConfigureAuthorizationDenialDoesNotMutatePolicy(t *testing.T) {
	store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	authorizer := &recordingAutostartAuthorizer{err: ErrAuthorizationDenied}
	mux := http.NewServeMux()
	registerBootAutostartHandlers(mux, store, authorizer)

	body, err := json.Marshal(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, api.AutostartConfigurePath, bytesReader(body)))
	if res.Code != http.StatusForbidden {
		t.Fatalf("configure denial status = %d, want 403", res.Code)
	}
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("denied configure mutated manifest: exists=%v err=%v", exists, err)
	}
}

func TestBootAutostartStatusFailsClosedOnMalformedPersistentState(t *testing.T) {
	stateDir := t.TempDir()
	store := newBootAutostartManifestStore(stateDir, fixedBootID(testBootConfigured))
	if err := os.WriteFile(store.path(), []byte(`{"schema_version":"broken","secret":"must-not-leak"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerBootAutostartHandlers(mux, store, AllowAuthorizer{})
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, api.AutostartStatusPath, nil))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("malformed status code = %d, want 500", res.Code)
	}
	if strings.Contains(res.Body.String(), "must-not-leak") || strings.Contains(res.Body.String(), "schema_version") {
		t.Fatalf("malformed persistent state leaked detail: %q", res.Body.String())
	}
}

func TestBootAutostartHandlersRejectUnsupportedMethods(t *testing.T) {
	store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	mux := http.NewServeMux()
	registerBootAutostartHandlers(mux, store, AllowAuthorizer{})
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPatch, api.AutostartConfigurePath},
		{http.MethodPost, api.AutostartStatusPath},
	} {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, nil))
		if res.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s status = %d, want 405", tc.method, tc.path, res.Code)
		}
	}
}

func bytesReader(data []byte) *bytes.Reader { return bytes.NewReader(data) }

var _ = errors.Is
