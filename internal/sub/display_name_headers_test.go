package sub

import (
	"net/http"
	"strings"
	"testing"
)

func TestProviderSubscriptionDisplayNameUsesSafeHTTPHeader(t *testing.T) {
	header := http.Header{}
	header.Set("X-Subscription-Title", "Remnawave Nodes")

	name, warnings := ProviderSubscriptionDisplayNameFromMetadata(FormatBase64, []byte("ignored"), header)
	if name != "Remnawave Nodes" {
		t.Fatalf("expected subscription name from header, got %q", name)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", warnings)
	}
}

func TestProviderSubscriptionDisplayNameUsesContentDispositionFilename(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Disposition", `attachment; filename="Provider Nodes.json"`)

	name, warnings := ProviderSubscriptionDisplayNameFromMetadata(FormatBase64, []byte("ignored"), header)
	if name != "Provider Nodes.json" {
		t.Fatalf("expected subscription name from Content-Disposition filename, got %q", name)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", warnings)
	}
}

func TestProviderSubscriptionDisplayNameRejectsUnsafeHTTPHeader(t *testing.T) {
	header := http.Header{}
	header.Set("Subscription-Title", "https://provider.example/subscription")

	name, warnings := ProviderSubscriptionDisplayNameFromMetadata(FormatBase64, []byte("ignored"), header)
	if name != "" {
		t.Fatalf("expected unsafe provider header to be rejected, got %q", name)
	}
	if len(warnings) != 1 || warnings[0].Message != SubscriptionDisplayNameRejectedWarning {
		t.Fatalf("expected redacted rejection warning, got %#v", warnings)
	}
	if strings.Contains(warnings[0].Message, "provider.example") {
		t.Fatalf("warning leaked unsafe provider header: %#v", warnings)
	}
}
