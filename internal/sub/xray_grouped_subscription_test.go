package sub

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/testfixtures"
)

func TestParseXrayJSONArrayPreservesGroupedAndSingleNativeProfiles(t *testing.T) {
	body := "[" + strings.Join([]string{
		testfixtures.GroupedProviderXrayJSON(),
		testfixtures.SingleVLESSXrayJSON("auto", "auto.edge.invalid", "tcp", "reality"),
		testfixtures.SingleVLESSXrayJSON("ai", "ai.edge.invalid", "ws", "tls"),
		testfixtures.SingleVLESSXrayJSON("tg", "tg.edge.invalid", "xhttp", "tls"),
	}, ",") + "]"

	parsed, err := ParseXrayJSONSubscription([]byte(body))
	if err != nil {
		t.Fatalf("ParseXrayJSONSubscription failed: %v", err)
	}
	if got, want := len(parsed.Profiles), 4; got != want {
		t.Fatalf("expected %d native profiles, got %d: %#v", want, got, parsed.Profiles)
	}
	if len(parsed.Unsupported) != 0 {
		t.Fatalf("expected no unsupported entries, got %#v", parsed.Unsupported)
	}

	ids := map[string]struct{}{}
	names := map[string]bool{}
	for _, p := range parsed.Profiles {
		if p.Protocol != profile.ProtocolXrayJSON || p.Source != profile.SourceSubscription {
			t.Fatalf("expected opaque subscription Xray profile, got %#v", p)
		}
		if p.Server != "" || p.Port != 0 || p.UserIdentity != "" {
			t.Fatalf("native Xray profile must not collapse to one endpoint/user: %#v", p)
		}
		if _, exists := ids[p.ID]; exists {
			t.Fatalf("duplicate profile id %q after native import", p.ID)
		}
		ids[p.ID] = struct{}{}
		names[p.Name] = true
	}
	for _, want := range []string{"Автоподбор локации", "auto", "ai", "tg"} {
		if !names[want] {
			t.Fatalf("expected profile name %q, got %#v", want, names)
		}
	}

	var grouped profile.Profile
	for _, p := range parsed.Profiles {
		if p.Name == "Автоподбор локации" {
			grouped = p
			break
		}
	}
	stored := profile.ProviderXrayConfigJSON(grouped)
	for _, want := range []string{`"tag":"auto"`, `"tag":"ai"`, `"tag":"tg"`, `"routing"`, `"balancers"`} {
		if !strings.Contains(stored, want) {
			t.Fatalf("expected grouped source to preserve %s, got %s", want, stored)
		}
	}
}

func TestParseXrayJSONObjectKeepsNativeQuicProfileOpaque(t *testing.T) {
	parsed, err := ParseXrayJSONSubscription([]byte(testfixtures.SingleVLESSXrayJSON("quic-provider", "quic.edge.invalid", "quic", "tls")))
	if err != nil {
		t.Fatalf("ParseXrayJSONSubscription failed: %v", err)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("expected one native profile, got %#v", parsed.Profiles)
	}
	p := parsed.Profiles[0]
	if p.Protocol != profile.ProtocolXrayJSON || p.Name != "quic-provider" {
		t.Fatalf("expected opaque native Xray profile, got %#v", p)
	}
	stored := profile.ProviderXrayConfigJSON(p)
	for _, want := range []string{"quic.edge.invalid", `"network":"quic"`, `"security":"tls"`} {
		if !strings.Contains(stored, want) {
			t.Fatalf("expected source to preserve %q: %s", want, stored)
		}
	}
}

func TestParseXrayJSONSubscriptionPreservesArrayEntryTypeDiagnostics(t *testing.T) {
	t.Parallel()

	_, err := ParseXrayJSONSubscription([]byte(`["not-an-object"]`))
	if err == nil {
		t.Fatal("expected unsupported array entry error")
	}
	if !strings.Contains(err.Error(), "unsupported Xray JSON array entry type string; expected object") {
		t.Fatalf("expected preserved array entry diagnostic, got %v", err)
	}
}

func TestParseSubscriptionContentPreservesTopLevelDiagnostics(t *testing.T) {
	t.Parallel()

	format, _, err := ParseSubscriptionContent([]byte(`42`))
	if format != FormatXrayJSON {
		t.Fatalf("format = %q, want %q", format, FormatXrayJSON)
	}
	if err == nil {
		t.Fatal("expected unsupported top-level type error")
	}
	if !strings.Contains(err.Error(), "unsupported subscription JSON top-level type number; expected Xray JSON object or array") {
		t.Fatalf("expected preserved top-level diagnostic, got %v", err)
	}
}
