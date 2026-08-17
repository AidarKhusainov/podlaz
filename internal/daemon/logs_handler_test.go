package daemon

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	podlogs "github.com/AidarKhusainov/podlaz/internal/logs"
)

func TestIssue254DaemonLogsHandlerAllowedSameGroupPeer(t *testing.T) {
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
			if subject.PID != 4242 {
				t.Fatalf("peer PID=%d, want 4242", subject.PID)
			}
			return nil
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/logs?since=015m&follow=1&core=1", nil)
	req = req.WithContext(contextWithPeerSubject(req.Context(), PeerSubject{PID: 4242, UID: 1000, GID: 1000, StartTime: 99}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	want := podlogs.Options{Since: "15m", Follow: true, Core: true}
	if got != want {
		t.Fatalf("logs options=%#v, want %#v", got, want)
	}
	if response.Body.String() != "podlaz daemon logs\nfixture line\n" {
		t.Fatalf("body=%q", response.Body.String())
	}
}

func TestIssue254DaemonLogsHandlerDeniesOutsideGroupBeforeJournalRead(t *testing.T) {
	mux := http.NewServeMux()
	runCalled := false
	registerLogsHandlerWithDeps(
		mux,
		func(context.Context, io.Writer, podlogs.Options) error {
			runCalled = true
			return nil
		},
		func(PeerSubject) error { return ErrAuthorizationDenied },
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/logs", nil)
	req = req.WithContext(contextWithPeerSubject(req.Context(), PeerSubject{PID: 4242, UID: 1001, GID: 1001, StartTime: 99}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if runCalled {
		t.Fatal("journal backend ran for an unauthorized peer")
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
	}, func(PeerSubject) error { return nil })

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/logs", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
