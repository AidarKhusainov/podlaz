package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestDefaultDNSOwnershipErrorsAreTypedAndHaveExactCleanupMessages(t *testing.T) {
	cases := []struct {
		name            string
		unknown         bool
		rollback        string
		wantStatus      int
		wantPhrase      string
		forbiddenPhrase string
	}{
		{
			name: "proven preflight conflict without candidate mutation", rollback: "not-started",
			wantStatus: http.StatusConflict, wantPhrase: "No new Podlaz VPN connection changes were applied.",
			forbiddenPhrase: "were rolled back",
		},
		{
			name: "late conflict with confirmed rollback", rollback: "completed",
			wantStatus: http.StatusConflict, wantPhrase: "The attempted Podlaz-owned network changes were rolled back.",
			forbiddenPhrase: "cleanup is incomplete",
		},
		{
			name: "late conflict with unfinished cleanup", rollback: "failed",
			wantStatus: http.StatusConflict, wantPhrase: "Podlaz-owned network cleanup is incomplete",
			forbiddenPhrase: "were rolled back",
		},
		{
			name: "unreadable ownership before mutation", unknown: true, rollback: "not-started",
			wantStatus: http.StatusServiceUnavailable, wantPhrase: "could not verify who controls default DNS",
			forbiddenPhrase: "were rolled back",
		},
		{
			name: "unreadable ownership with unfinished rollback", unknown: true, rollback: "failed",
			wantStatus: http.StatusServiceUnavailable, wantPhrase: "cleanup is incomplete",
			forbiddenPhrase: "were rolled back",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			internal := errors.Join(
				&netexecutor.DefaultDNSOwnershipError{Link: "wg0", Unknown: tt.unknown},
				errors.New("private DNS server 198.51.100.53; transaction secret-123"),
			)
			err := withTunFailurePhase("dns-ownership-check", "secret-123", tt.rollback, internal)
			if status := daemonAPIHTTPStatusCode(err); status != tt.wantStatus {
				t.Fatalf("status=%d want=%d: %v", status, tt.wantStatus, err)
			}
			for _, phrase := range []string{"Unable to connect.", "default DNS", tt.wantPhrase, "podlaz debug doctor --tun"} {
				if !strings.Contains(err.Error(), phrase) {
					t.Fatalf("missing %q in user-facing error: %v", phrase, err)
				}
			}
			for _, forbidden := range []string{tt.forbiddenPhrase, "198.51.100.53", "secret-123", "HTTP 500"} {
				if forbidden != "" && strings.Contains(err.Error(), forbidden) {
					t.Fatalf("leaked or falsely claimed %q in error: %v", forbidden, err)
				}
			}
			var ownership *netexecutor.DefaultDNSOwnershipError
			if !errors.As(err, &ownership) || ownership.Unknown != tt.unknown {
				t.Fatalf("typed ownership cause was lost: %v", err)
			}
		})
	}
}

func TestConnectHandlerReturnsRedactedForeignDNSConflictInsteadOfHTTP500(t *testing.T) {
	ownership := &netexecutor.DefaultDNSOwnershipError{Link: "wg0"}
	lifecycle := &staticLifecycle{
		connectErr: withTunFailurePhase("dns-ownership-preflight", "", "not-started", ownership),
		status:     api.StatusResponse{Connection: "inactive"},
	}
	mux := http.NewServeMux()
	registerLifecycleHandlers(mux, lifecycle)

	request := httptest.NewRequest(http.MethodPost, api.ConnectPath, strings.NewReader(validConnectBody(planner.ModeTun)))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d want=409: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Another VPN or network service controls default DNS (~.)") ||
		!strings.Contains(response.Body.String(), "No new Podlaz VPN connection changes were applied.") {
		t.Fatalf("missing actionable, mutation-free conflict message: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "wg0") || strings.Contains(response.Body.String(), "HTTP 500") {
		t.Fatalf("public error included foreign identity or an internal HTTP error: %s", response.Body.String())
	}
}

func TestPrepareTunCoexistenceAllowsRetryAfterForeignDefaultDNSOwnerDisappears(t *testing.T) {
	m := NewXrayManager(t.TempDir())
	first := netsnapshot.FakeDesktopWithForeignDefaultDNSOwner()
	if _, err := m.prepareTunCoexistence(context.Background(), first, api.HandoffBlock, netsnapshot.Options{}); err == nil {
		t.Fatal("expected foreign default DNS conflict")
	}

	withoutOwner := netsnapshot.FakeResolvedDesktop()
	if _, err := m.prepareTunCoexistence(context.Background(), withoutOwner, api.HandoffBlock, netsnapshot.Options{}); err != nil {
		t.Fatalf("removing independently managed foreign default DNS should clear the blocker: %v", err)
	}
}
