package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type fakeAuthorizer struct {
	err         error
	requirePeer bool
	actions     []AuthorizationAction
	subjects    []PeerSubject
}

func (a *fakeAuthorizer) Authorize(_ context.Context, action AuthorizationAction, subject PeerSubject) error {
	a.actions = append(a.actions, action)
	a.subjects = append(a.subjects, subject)
	return a.err
}

func (a *fakeAuthorizer) RequiresPeerCredentials() bool {
	return a.requirePeer
}

type fakeLifecycle struct {
	connectCalls    int
	disconnectCalls int
	lastRequest     api.ConnectRequest
}

func (l *fakeLifecycle) Connect(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	l.connectCalls++
	l.lastRequest = req
	return api.LifecycleResponse{Connection: "active", Mode: req.Mode, Proxy: "listening", TUN: "disabled"}, nil
}

func (l *fakeLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	l.disconnectCalls++
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

func TestConnectAuthorizationDeniedDoesNotCallLifecycle(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	authorizer := &fakeAuthorizer{err: ErrAuthorizationDenied, requirePeer: true}
	mux := http.NewServeMux()
	registerLifecycleHandlers(mux, lifecycle, authorizer)

	body := `{"mode":"tun","profile":{"id":"profile-secret-token","name":"vpn","source":"manual","engine":"xray","server":"vpn.example","port":443,"protocol":"vless","user_identity":"11111111-1111-1111-1111-111111111111"}}`
	req := authorizedRequest(http.MethodPost, api.ConnectPath, body)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d: %s", rr.Code, rr.Body.String())
	}
	if lifecycle.connectCalls != 0 {
		t.Fatalf("connect lifecycle called %d time(s), want 0", lifecycle.connectCalls)
	}
	if len(authorizer.actions) != 1 || authorizer.actions[0] != ActionConnectTun {
		t.Fatalf("authorized actions = %v, want %s", authorizer.actions, ActionConnectTun)
	}
	assertNotContains(t, rr.Body.String(), "profile-secret-token")
	assertNotContains(t, rr.Body.String(), "11111111-1111-1111-1111-111111111111")
}

func TestConnectAuthorizationAllowCallsLifecycle(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	authorizer := &fakeAuthorizer{requirePeer: true}
	mux := http.NewServeMux()
	registerLifecycleHandlers(mux, lifecycle, authorizer)

	body := `{"mode":"proxy-only","profile":{"id":"profile-1","name":"vpn","source":"manual","engine":"xray","server":"vpn.example","port":443,"protocol":"vless"}}`
	req := authorizedRequest(http.MethodPost, api.ConnectPath, body)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected OK, got %d: %s", rr.Code, rr.Body.String())
	}
	if lifecycle.connectCalls != 1 {
		t.Fatalf("connect lifecycle called %d time(s), want 1", lifecycle.connectCalls)
	}
	if lifecycle.lastRequest.Mode != planner.ModeProxyOnly {
		t.Fatalf("connect mode = %q, want %q", lifecycle.lastRequest.Mode, planner.ModeProxyOnly)
	}
	if len(authorizer.actions) != 1 || authorizer.actions[0] != ActionConnectProxyOnly {
		t.Fatalf("authorized actions = %v, want %s", authorizer.actions, ActionConnectProxyOnly)
	}
}

func TestConnectAuthorizationUnavailableDoesNotCallLifecycle(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	authorizer := &fakeAuthorizer{err: ErrAuthorizationUnavailable, requirePeer: true}
	mux := http.NewServeMux()
	registerLifecycleHandlers(mux, lifecycle, authorizer)

	body := `{"mode":"tun","profile":{"id":"profile-1","name":"vpn","source":"manual","engine":"xray","server":"vpn.example","port":443,"protocol":"vless"}}`
	req := authorizedRequest(http.MethodPost, api.ConnectPath, body)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable, got %d: %s", rr.Code, rr.Body.String())
	}
	if lifecycle.connectCalls != 0 {
		t.Fatalf("connect lifecycle called %d time(s), want 0", lifecycle.connectCalls)
	}
}

func TestDisconnectAuthorizationDeniedDoesNotCallLifecycle(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	authorizer := &fakeAuthorizer{err: ErrAuthorizationDenied, requirePeer: true}
	mux := http.NewServeMux()
	registerLifecycleHandlers(mux, lifecycle, authorizer)

	req := authorizedRequest(http.MethodPost, api.DisconnectPath, "")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d: %s", rr.Code, rr.Body.String())
	}
	if lifecycle.disconnectCalls != 0 {
		t.Fatalf("disconnect lifecycle called %d time(s), want 0", lifecycle.disconnectCalls)
	}
	if len(authorizer.actions) != 1 || authorizer.actions[0] != ActionDisconnect {
		t.Fatalf("authorized actions = %v, want %s", authorizer.actions, ActionDisconnect)
	}
}

func TestAuthorizerRequiringPeerFailsWithoutPeerCredentials(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	authorizer := &fakeAuthorizer{requirePeer: true}
	mux := http.NewServeMux()
	registerLifecycleHandlers(mux, lifecycle, authorizer)

	body := `{"mode":"proxy-only","profile":{"id":"profile-1","name":"vpn","source":"manual","engine":"xray","server":"vpn.example","port":443,"protocol":"vless"}}`
	req := httptest.NewRequest(http.MethodPost, api.ConnectPath, strings.NewReader(body))
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(authorizer.actions) != 0 {
		t.Fatalf("authorizer was called without peer credentials: %v", authorizer.actions)
	}
	if lifecycle.connectCalls != 0 {
		t.Fatalf("connect lifecycle called %d time(s), want 0", lifecycle.connectCalls)
	}
}

func TestPolkitAuthorizerBuildsOperationSpecificPkcheckRequest(t *testing.T) {
	var gotCommand string
	var gotArgs []string
	authorizer := PolkitAuthorizer{
		AllowUserInteraction: true,
		lookupPath: func(command string) (string, error) {
			if command != "pkcheck" {
				t.Fatalf("lookup command = %q, want pkcheck", command)
			}
			return "/usr/bin/pkcheck", nil
		},
		run: func(_ context.Context, command string, args []string) error {
			gotCommand = command
			gotArgs = append([]string(nil), args...)
			return nil
		},
	}

	err := authorizer.Authorize(context.Background(), ActionConnectTun, PeerSubject{PID: 4242, UID: 1000, GID: 1000, StartTime: 987654321})
	if err != nil {
		t.Fatalf("Authorize returned error: %v", err)
	}
	if gotCommand != "/usr/bin/pkcheck" {
		t.Fatalf("command = %q, want /usr/bin/pkcheck", gotCommand)
	}
	wantArgs := []string{"--action-id", string(ActionConnectTun), "--process", "4242,987654321,1000", "--allow-user-interaction"}
	if strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestPolkitAuthorizerRejectsMissingPeerStartTime(t *testing.T) {
	authorizer := PolkitAuthorizer{
		lookupPath: func(string) (string, error) {
			return "/usr/bin/pkcheck", nil
		},
	}

	err := authorizer.Authorize(context.Background(), ActionConnectTun, PeerSubject{PID: 4242, UID: 1000, GID: 1000})
	if !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("Authorize error = %v, want ErrAuthorizationUnavailable", err)
	}
}

func TestPolkitAuthorizerReportsLookupFailureAsUnavailable(t *testing.T) {
	authorizer := PolkitAuthorizer{
		lookupPath: func(string) (string, error) {
			return "", errors.New("not found")
		},
	}

	err := authorizer.Authorize(context.Background(), ActionConnectTun, PeerSubject{PID: 4242, UID: 1000, GID: 1000, StartTime: 987654321})
	if !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("Authorize error = %v, want ErrAuthorizationUnavailable", err)
	}
}

func authorizedRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := contextWithPeerSubject(req.Context(), PeerSubject{PID: 4242, UID: 1000, GID: 1000, StartTime: 987654321})
	return req.WithContext(ctx)
}

func assertNotContains(t *testing.T, s, forbidden string) {
	t.Helper()
	if strings.Contains(s, forbidden) {
		t.Fatalf("response leaked %q: %s", forbidden, s)
	}
}
