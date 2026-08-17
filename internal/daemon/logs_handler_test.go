package daemon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	podlogs "github.com/AidarKhusainov/podlaz/internal/logs"
)

func TestIssue254DaemonLogsHandlerAllowsAuthenticatedLocalPeer(t *testing.T) {
	mux := http.NewServeMux()
	var got podlogs.Options
	registerLogsHandlerWithDeps(
		mux,
		func(_ context.Context, dst io.Writer, opts podlogs.Options) error {
			got = opts
			_, err := io.WriteString(dst, "podlaz daemon logs\nfixture line\n")
			return err
		},
		func(subject PeerSubject) error {
			if subject.PID != 4242 || subject.StartTime != 99 {
				t.Fatalf("peer=%#v, want authenticated fixture peer", subject)
			}
			return nil
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/logs?since=036h&follow=1&core=1", nil)
	req = req.WithContext(contextWithPeerSubject(req.Context(), PeerSubject{PID: 4242, UID: 1000, GID: 1000, StartTime: 99}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	want := podlogs.Options{Since: "36h", Follow: true, Core: true}
	if got != want {
		t.Fatalf("logs options=%#v, want %#v", got, want)
	}
	if response.Body.String() != "podlaz daemon logs\nfixture line\n" {
		t.Fatalf("body=%q", response.Body.String())
	}
}

func TestIssue254DaemonAndCoreLogsShareValidatedSinceContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		core bool
	}{
		{name: "daemon", path: "/v1/logs?since=036h", core: false},
		{name: "core", path: "/v1/logs?since=036h&core=1", core: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			var got podlogs.Options
			registerLogsHandlerWithDeps(
				mux,
				func(_ context.Context, dst io.Writer, opts podlogs.Options) error {
					got = opts
					_, err := io.WriteString(dst, "fixture\n")
					return err
				},
				func(PeerSubject) error { return nil },
			)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req = req.WithContext(contextWithPeerSubject(req.Context(), PeerSubject{PID: 4242, UID: 1000, GID: 1000, StartTime: 99}))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if got.Since != "36h" || got.Core != tc.core || got.Follow {
				t.Fatalf("options=%#v, want since=36h core=%t follow=false", got, tc.core)
			}
		})
	}
}

func TestIssue254DaemonLogsHandlerFailsClosedWhenPeerAuthorizationFails(t *testing.T) {
	mux := http.NewServeMux()
	runCalled := false
	registerLogsHandlerWithDeps(
		mux,
		func(context.Context, io.Writer, podlogs.Options) error {
			runCalled = true
			return nil
		},
		func(PeerSubject) error { return ErrAuthorizationUnavailable },
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/logs", nil)
	req = req.WithContext(contextWithPeerSubject(req.Context(), PeerSubject{PID: 4242, UID: 1001, GID: 1001, StartTime: 99}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if runCalled {
		t.Fatal("journal backend ran for an unauthenticated peer")
	}
}

func TestIssue254DaemonLogsHandlerRejectsInvalidQueryBeforeJournalRead(t *testing.T) {
	mux := http.NewServeMux()
	runCalled := false
	registerLogsHandlerWithDeps(
		mux,
		func(context.Context, io.Writer, podlogs.Options) error {
			runCalled = true
			return nil
		},
		func(PeerSubject) error { return nil },
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/logs?since=1day", nil)
	req = req.WithContext(contextWithPeerSubject(req.Context(), PeerSubject{PID: 4242, UID: 1000, GID: 1000, StartTime: 99}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if runCalled {
		t.Fatal("journal backend ran for invalid query")
	}
}

func TestIssue254DaemonLogsHandlerRequiresPeerCredentials(t *testing.T) {
	mux := http.NewServeMux()
	registerLogsHandlerWithDeps(mux, func(context.Context, io.Writer, podlogs.Options) error {
		t.Fatal("journal backend ran without peer credentials")
		return nil
	}, nil)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/logs", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestIssue254DaemonLogsHandlerReportsLateBackendFailureInTrailer(t *testing.T) {
	mux := http.NewServeMux()
	registerLogsHandlerWithDeps(
		mux,
		func(_ context.Context, dst io.Writer, _ podlogs.Options) error {
			_, _ = io.WriteString(dst, "podlaz daemon logs\n")
			return errors.New("fixture backend failure with private detail")
		},
		func(PeerSubject) error { return nil },
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/logs", nil)
	req = req.WithContext(contextWithPeerSubject(req.Context(), PeerSubject{PID: 4242, UID: 1000, GID: 1000, StartTime: 99}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	result := response.Result()
	defer result.Body.Close()

	if result.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", result.StatusCode)
	}
	if got := result.Trailer.Get(api.LogsErrorTrailer); got != "backend-failed" {
		t.Fatalf("logs error trailer=%q, want backend-failed", got)
	}
	if response.Body.String() != "podlaz daemon logs\n" {
		t.Fatalf("body=%q", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private detail") {
		t.Fatal("backend error detail leaked into the client stream")
	}
}
