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

func TestFetchSourceSendsTunWardenUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != subscriptionUserAgent {
			t.Fatalf("expected User-Agent %q, got %q", subscriptionUserAgent, got)
		}
		_, _ = w.Write([]byte("subscription"))
	}))
	defer server.Close()

	data, err := FetchSource(context.Background(), Source{ID: "ua", Name: "ua", URL: server.URL + "/sub", Format: FormatBase64})
	if err != nil {
		t.Fatalf("FetchSource failed: %v", err)
	}
	if string(data) != "subscription" {
		t.Fatalf("unexpected body: %q", data)
	}
}

func TestFetchSourceReplacesClientIDPlaceholder(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("hwid")
		if got == "" {
			t.Fatal("expected hwid query parameter")
		}
		if got == subscriptionClientIDPlaceholder || strings.Contains(got, "tunwarden-client-id") {
			t.Fatalf("placeholder was not replaced: %q", got)
		}
		if !validClientID(got) {
			t.Fatalf("expected generated client-id, got %q", got)
		}
		if values, ok := r.URL.Query()["empty"]; !ok || len(values) != 1 || values[0] != "" {
			t.Fatalf("expected unrelated empty query value to stay empty, got %q", r.URL.RawQuery)
		}
		seen = append(seen, got)
		_, _ = w.Write([]byte("subscription"))
	}))
	defer server.Close()

	source := Source{ID: "identity", Name: "identity", URL: server.URL + "/sub?empty=&hwid=" + subscriptionClientIDPlaceholder, Format: FormatBase64}
	for range 2 {
		if _, err := FetchSource(context.Background(), source); err != nil {
			t.Fatalf("FetchSource failed: %v", err)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(seen))
	}
	if seen[0] != seen[1] {
		t.Fatalf("expected stable client-id, got %q then %q", seen[0], seen[1])
	}

	clientIDPath := filepath.Join(stateHome, "tunwarden", clientIDFileName)
	data, err := os.ReadFile(clientIDPath)
	if err != nil {
		t.Fatalf("expected persisted client-id: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != seen[0] {
		t.Fatalf("expected persisted client-id %q, got %q", seen[0], got)
	}
	info, err := os.Stat(clientIDPath)
	if err != nil {
		t.Fatalf("stat client-id: %v", err)
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

func TestFetchSourceRejectsClientIDPlaceholderOutsideQueryValue(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not be sent")
	}))
	defer server.Close()

	tests := map[string]string{
		"host":                "https://" + subscriptionClientIDPlaceholder + ".example.com/sub?hwid=x",
		"userinfo":            "https://user:" + subscriptionClientIDPlaceholder + "@example.com/sub?hwid=x",
		"path":                server.URL + "/" + subscriptionClientIDPlaceholder + "?hwid=x",
		"fragment":            server.URL + "/sub?hwid=x#" + subscriptionClientIDPlaceholder,
		"query key":           server.URL + "/sub?" + subscriptionClientIDPlaceholder + "=x",
		"partial query value": server.URL + "/sub?hwid=prefix-" + subscriptionClientIDPlaceholder,
	}

	for name, sourceURL := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := FetchSource(context.Background(), Source{ID: name, Name: name, URL: sourceURL, Format: FormatBase64})
			if !errors.Is(err, errUnsupportedClientIDPlaceholder) {
				t.Fatalf("expected unsupported placeholder error, got %v", err)
			}
			clientIDPath := filepath.Join(stateHome, "tunwarden", clientIDFileName)
			if _, err := os.Stat(clientIDPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected no client-id file, got err=%v", err)
			}
		})
	}
}

func TestFetchSourceRedactsClientIDFromFetchErrors(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("subscription"))
	}))
	urlWithPlaceholder := server.URL + "/sub?hwid=" + subscriptionClientIDPlaceholder
	server.Close()

	_, err := FetchSource(context.Background(), Source{ID: "redacted", Name: "redacted", URL: urlWithPlaceholder, Format: FormatBase64})
	if err == nil {
		t.Fatal("expected fetch error")
	}
	clientIDPath := filepath.Join(stateHome, "tunwarden", clientIDFileName)
	data, readErr := os.ReadFile(clientIDPath)
	if readErr != nil {
		t.Fatalf("expected persisted client-id: %v", readErr)
	}
	clientID := strings.TrimSpace(string(data))
	if !validClientID(clientID) {
		t.Fatalf("expected generated client-id, got %q", clientID)
	}
	errText := err.Error()
	if strings.Contains(errText, clientID) {
		t.Fatalf("fetch error leaked client-id %q in %q", clientID, errText)
	}
	if strings.Contains(errText, "hwid=") {
		t.Fatalf("fetch error leaked identity query parameter: %q", errText)
	}
}

func TestFetchSourceDoesNotCreateClientIDWithoutPlaceholder(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "tunwarden-client-id") {
			t.Fatalf("unexpected placeholder in request: %s", r.URL.String())
		}
		_, _ = w.Write([]byte("subscription"))
	}))
	defer server.Close()

	_, err := FetchSource(context.Background(), Source{ID: "plain", Name: "plain", URL: server.URL + "/sub?token=secret", Format: FormatBase64})
	if err != nil {
		t.Fatalf("FetchSource failed: %v", err)
	}
	clientIDPath, err := DefaultClientIDPath()
	if err != nil {
		t.Fatalf("DefaultClientIDPath failed: %v", err)
	}
	if _, err := os.Stat(clientIDPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no client-id file, got err=%v", err)
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
