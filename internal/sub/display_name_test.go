package sub

import (
	"strings"
	"testing"
)

func TestProviderSubscriptionDisplayNameUsesSafeXrayJSONTitle(t *testing.T) {
	content := []byte(xrayObjectWithTopLevelField(
		remoteXrayConfigObject("00000000-0000-0000-0000-000000000201", "title.example", "title-profile", "tcp", "tls"),
		`"title":"My Provider"`,
	))

	name, warnings := ProviderSubscriptionDisplayName(FormatXrayJSON, content)
	if name != "My Provider" {
		t.Fatalf("expected provider subscription name, got %q", name)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", warnings)
	}
}

func TestProviderSubscriptionDisplayNameRejectsUnsafeXrayJSONTitle(t *testing.T) {
	content := []byte(xrayObjectWithTopLevelField(
		remoteXrayConfigObject("00000000-0000-0000-0000-000000000202", "unsafe-title.example", "unsafe-profile", "tcp", "tls"),
		`"title":"https://provider.example/sub?token=secret"`,
	))

	name, warnings := ProviderSubscriptionDisplayName(FormatXrayJSON, content)
	if name != "" {
		t.Fatalf("expected unsafe provider subscription name to be rejected, got %q", name)
	}
	if len(warnings) != 1 || warnings[0].Message != SubscriptionDisplayNameRejectedWarning {
		t.Fatalf("expected redacted rejection warning, got %#v", warnings)
	}
	if strings.Contains(warnings[0].Message, "provider.example") || strings.Contains(warnings[0].Message, "token") {
		t.Fatalf("warning leaked unsafe provider name: %#v", warnings)
	}
}

func TestProviderSubscriptionDisplayNameIgnoresUnsupportedClientRemarks(t *testing.T) {
	content := []byte(xrayObjectWithTopLevelField(
		remoteXrayConfigObject("00000000-0000-0000-0000-000000000203", "unsupported-client.example", "dummy-profile", "tcp", "tls"),
		`"remarks":"App not supported"`,
	))

	name, warnings := ProviderSubscriptionDisplayName(FormatXrayJSON, content)
	if name != "" || len(warnings) != 0 {
		t.Fatalf("expected unsupported-client remarks to be ignored as a subscription name, got name=%q warnings=%#v", name, warnings)
	}
}

func TestRefreshProviderDisplayNameUpdatesProviderOwnedNameWithoutChangingID(t *testing.T) {
	source := NewSource("", "https://provider.example/subscriptions/personal?token=secret")
	oldID := source.ID

	refreshed := RefreshProviderDisplayName(source, "My Provider")
	if refreshed.ID != oldID {
		t.Fatalf("expected subscription ID to stay stable, before=%q after=%q", oldID, refreshed.ID)
	}
	if refreshed.Name != "My Provider" {
		t.Fatalf("expected provider display name to refresh, got %#v", refreshed)
	}
	if strings.Contains(refreshed.Name, "token") || strings.Contains(refreshed.Name, "https://") {
		t.Fatalf("refreshed name leaked source URL data: %#v", refreshed)
	}
}

func TestRefreshProviderDisplayNamePreservesExplicitName(t *testing.T) {
	source := NewSource("Explicit", "https://provider.example/subscriptions/personal?token=secret")

	refreshed := RefreshProviderDisplayName(source, "My Provider")
	if refreshed.Name != "Explicit" || refreshed.ID != "explicit" {
		t.Fatalf("expected explicit subscription name to be preserved, got %#v", refreshed)
	}
}
