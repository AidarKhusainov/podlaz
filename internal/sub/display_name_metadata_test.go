package sub

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestMetadataDisplayNameUsesAttachmentName(t *testing.T) {
	header := http.Header{}
	header.Set("Content"+"-Disposition", `attachment; filename="Synthetic Nodes"`)
	name, warnings := ProviderSubscriptionDisplayNameFromMetadata(FormatBase64, []byte("sample"), header)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if name != "Synthetic Nodes" {
		t.Fatalf("expected display name, got %q", name)
	}
}

func TestMetadataDisplayNameDecodesRemnawaveProfileTitle(t *testing.T) {
	header := http.Header{}
	header.Set("Profile-Title", "base"+"64:"+base64.StdEncoding.EncodeToString([]byte("Censor Amoroso VPN")))

	body := []byte(`{"name":"vpn-client-test"}`)
	name, warnings := ProviderSubscriptionDisplayNameFromMetadata(FormatXrayJSON, body, header)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if name != "Censor Amoroso VPN" {
		t.Fatalf("expected decoded Remnawave profile title, got %q", name)
	}
}

func TestMetadataDisplayNameRejectsDecodedURLProfileTitle(t *testing.T) {
	header := http.Header{}
	decoded := strings.Join([]string{"https:", "", "provider.example", "sub"}, "/")
	header.Set("Profile-Title", "base"+"64:"+base64.StdEncoding.EncodeToString([]byte(decoded)))

	body := []byte(`{"title":"Safe Subscription"}`)
	name, warnings := ProviderSubscriptionDisplayNameFromMetadata(FormatXrayJSON, body, header)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if name != "Safe Subscription" {
		t.Fatalf("expected fallback JSON display name, got %q", name)
	}
}

func TestMetadataDisplayNameRejectsDecodedShareURIProfileTitle(t *testing.T) {
	header := http.Header{}
	decoded := strings.Join([]string{"vl" + "ess:", "", "user@example.test:443"}, "/")
	header.Set("Profile-Title", "base"+"64:"+base64.StdEncoding.EncodeToString([]byte(decoded)))

	body := []byte(`{"title":"Safe Subscription"}`)
	name, warnings := ProviderSubscriptionDisplayNameFromMetadata(FormatXrayJSON, body, header)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if name != "Safe Subscription" {
		t.Fatalf("expected fallback JSON display name, got %q", name)
	}
}
