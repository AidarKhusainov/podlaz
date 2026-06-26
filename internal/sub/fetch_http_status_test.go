package sub

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchSourceWithMetadataKeepsSuccessfulHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "title=Provider Demo")
		_, _ = w.Write([]byte("dmxlc3M6Ly8wMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDFAZXhhbXBsZS5jb206NDQzP3R5cGU9dGNwJnNlY3VyaXR5PXRscyNvbkU="))
	}))
	defer server.Close()

	result, err := FetchSourceWithMetadata(context.Background(), Source{ID: "headers", Name: "headers", URL: server.URL + "/sub", Format: FormatBase64})
	if err != nil {
		t.Fatalf("FetchSourceWithMetadata failed: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected response content")
	}
	if got := result.Header.Get("Subscription-Userinfo"); got != "title=Provider Demo" {
		t.Fatalf("expected response header to be preserved, got %q", got)
	}
}

func TestFetchSourceRejectsUnexpectedHTTPStatus(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "subscription unavailable", status)
			}))
			defer server.Close()

			_, err := FetchSource(context.Background(), Source{ID: "status", Name: "status", URL: server.URL + "/sub", Format: FormatBase64})
			if err == nil {
				t.Fatal("expected status error")
			}
			if !strings.Contains(err.Error(), "unexpected HTTP status") || !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
