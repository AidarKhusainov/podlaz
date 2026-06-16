package sub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFetchSourceSendsStableClientHeaderWithoutPlaceholder(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != subscriptionUserAgent {
			t.Fatalf("expected User-Agent %q, got %q", subscriptionUserAgent, got)
		}
		id := r.Header.Get(subscriptionClientHeader)
		if !validClientID(id) {
			t.Fatalf("expected generated %s header, got %q", subscriptionClientHeader, id)
		}
		if strings.Contains(r.URL.RawQuery, id) || strings.Contains(r.URL.RawQuery, "tunwarden-client-id") {
			t.Fatalf("generated identity leaked into URL query: %q", r.URL.RawQuery)
		}
		seen = append(seen, id)
		_, _ = w.Write([]byte("subscription"))
	}))
	defer server.Close()

	source := Source{ID: "plain", Name: "plain", URL: server.URL + "/sub?token=secret", Format: FormatBase64}
	for range 2 {
		data, err := FetchSource(context.Background(), source)
		if err != nil {
			t.Fatalf("FetchSource failed: %v", err)
		}
		if string(data) != "subscription" {
			t.Fatalf("unexpected body: %q", data)
		}
	}
	if len(seen) != 2 || seen[0] != seen[1] {
		t.Fatalf("expected stable client identity across requests, got %#v", seen)
	}
	clientIDPath := filepath.Join(stateHome, "tunwarden", clientIDFileName)
	data, err := os.ReadFile(clientIDPath)
	if err != nil {
		t.Fatalf("expected persisted client-id: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != seen[0] {
		t.Fatalf("expected persisted client-id %q, got %q", seen[0], got)
	}
}

func TestFetchSourceReplacesClientIDPlaceholderAndUsesSameHeader(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryID := r.URL.Query().Get("hwid")
		if !validClientID(queryID) {
			t.Fatalf("expected generated query client-id, got %q", queryID)
		}
		if headerID := r.Header.Get(subscriptionClientHeader); headerID != queryID {
			t.Fatalf("expected %s header %q, got %q", subscriptionClientHeader, queryID, headerID)
		}
		if values, ok := r.URL.Query()["empty"]; !ok || len(values) != 1 || values[0] != "" {
			t.Fatalf("expected unrelated empty query value to stay empty, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte("subscription"))
	}))
	defer server.Close()

	source := Source{ID: "identity", Name: "identity", URL: server.URL + "/sub?empty=&hwid=" + subscriptionClientIDPlaceholder, Format: FormatBase64}
	if _, err := FetchSource(context.Background(), source); err != nil {
		t.Fatalf("FetchSource failed: %v", err)
	}
	clientIDPath := filepath.Join(stateHome, "tunwarden", clientIDFileName)
	info, err := os.Stat(clientIDPath)
	if err != nil {
		t.Fatalf("expected persisted client-id: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected client-id mode 0600, got %04o", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(clientIDPath))
	if err != nil {
		t.Fatalf("stat client-id directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("expected client-id directory mode 0700, got %04o", got)
	}
}

func TestFetchSourceRejectsInvalidClientIDPlaceholderURLsBeforeCreatingIdentity(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not be sent")
	}))
	defer server.Close()

	tests := map[string]struct {
		sourceURL          string
		wantPlaceholderErr bool
	}{
		"host parse validation":     {sourceURL: "https://" + subscriptionClientIDPlaceholder + ".example.com/sub?hwid=x"},
		"userinfo parse validation": {sourceURL: "https://user:" + subscriptionClientIDPlaceholder + "@example.com/sub?hwid=x"},
		"path":                      {sourceURL: server.URL + "/" + subscriptionClientIDPlaceholder + "?hwid=x", wantPlaceholderErr: true},
		"fragment":                  {sourceURL: server.URL + "/sub?hwid=x#" + subscriptionClientIDPlaceholder, wantPlaceholderErr: true},
		"query key":                 {sourceURL: server.URL + "/sub?" + subscriptionClientIDPlaceholder + "=x", wantPlaceholderErr: true},
		"partial query value":       {sourceURL: server.URL + "/sub?hwid=prefix-" + subscriptionClientIDPlaceholder, wantPlaceholderErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := FetchSource(context.Background(), Source{ID: name, Name: name, URL: tc.sourceURL, Format: FormatBase64})
			if err == nil {
				t.Fatal("expected invalid placeholder URL to fail")
			}
			if tc.wantPlaceholderErr && !errors.Is(err, errUnsupportedClientIDPlaceholder) {
				t.Fatalf("expected unsupported placeholder error, got %v", err)
			}
			clientIDPath := filepath.Join(stateHome, "tunwarden", clientIDFileName)
			if _, err := os.Stat(clientIDPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected no client-id file, got err=%v", err)
			}
		})
	}
}

func TestFetchSourceRedactsClientIdentityAndSubscriptionURLFromFetchErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		urlSuffix string
	}{
		{name: "placeholder", urlSuffix: "/sub?hwid=" + subscriptionClientIDPlaceholder},
		{name: "plain token", urlSuffix: "/sub?token=secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateHome := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateHome)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("subscription"))
			}))
			sourceURL := server.URL + tc.urlSuffix
			server.Close()

			_, err := FetchSource(context.Background(), Source{ID: "redacted", Name: "redacted", URL: sourceURL, Format: FormatBase64})
			if err == nil {
				t.Fatal("expected fetch error")
			}
			data, readErr := os.ReadFile(filepath.Join(stateHome, "tunwarden", clientIDFileName))
			if readErr != nil {
				t.Fatalf("expected persisted client-id: %v", readErr)
			}
			clientID := strings.TrimSpace(string(data))
			errText := err.Error()
			if strings.Contains(errText, clientID) {
				t.Fatalf("fetch error leaked client-id %q in %q", clientID, errText)
			}
			if strings.Contains(errText, sourceURL) || strings.Contains(errText, "token=secret") || strings.Contains(errText, "hwid=") {
				t.Fatalf("fetch error leaked subscription URL details: %q", errText)
			}
			if !strings.Contains(errText, "redacted subscription URL") {
				t.Fatalf("expected redacted URL error, got %q", errText)
			}
		})
	}
}

func TestLoadOrCreateClientIDRejectsInvalidStoredID(t *testing.T) {
	clientIDPath := filepath.Join(t.TempDir(), clientIDFileName)
	if err := os.WriteFile(clientIDPath, []byte("not-a-client-id\n"), 0o600); err != nil {
		t.Fatalf("write invalid client-id: %v", err)
	}
	_, err := LoadOrCreateClientID(clientIDPath)
	if err == nil {
		t.Fatal("expected invalid stored client-id to fail")
	}
	if !strings.Contains(err.Error(), "invalid client-id") {
		t.Fatalf("expected invalid client-id error, got %v", err)
	}
}

func TestLoadOrCreateClientIDConcurrentCreateReturnsStableID(t *testing.T) {
	clientIDPath := filepath.Join(t.TempDir(), clientIDFileName)
	const workers = 32
	var wg sync.WaitGroup
	ids := make(chan string, workers)
	errs := make(chan error, workers)

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := LoadOrCreateClientID(clientIDPath)
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("LoadOrCreateClientID failed: %v", err)
		}
	}
	var first string
	for id := range ids {
		if !validClientID(id) {
			t.Fatalf("expected generated client-id, got %q", id)
		}
		if first == "" {
			first = id
			continue
		}
		if id != first {
			t.Fatalf("expected stable client-id %q, got %q", first, id)
		}
	}
	data, err := os.ReadFile(clientIDPath)
	if err != nil {
		t.Fatalf("read persisted client-id: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != first {
		t.Fatalf("expected persisted client-id %q, got %q", first, got)
	}
}
