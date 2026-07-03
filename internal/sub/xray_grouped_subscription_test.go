package sub

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/testfixtures"
)

func TestParseXrayJSONArrayImportsGroupedProviderProfileBesideDuplicateLocationEntries(t *testing.T) {
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
		t.Fatalf("expected %d profiles, got %d: %#v", want, got, parsed.Profiles)
	}
	if got := len(parsed.Unsupported); got != 0 {
		t.Fatalf("expected no unsupported entries, got %d: %#v", got, parsed.Unsupported)
	}

	ids := map[string]struct{}{}
	var grouped *profile.Profile
	var xhttpOrdinary *profile.Profile
	ordinaryByTag := map[string]profile.Profile{}
	for i := range parsed.Profiles {
		p := parsed.Profiles[i]
		if _, exists := ids[p.ID]; exists {
			t.Fatalf("duplicate profile id %q after grouped import", p.ID)
		}
		ids[p.ID] = struct{}{}
		if p.Protocol == profile.ProtocolXrayJSON {
			grouped = &p
			continue
		}
		ordinaryByTag[p.Name] = p
		if p.Transport == "xhttp" {
			xhttpOrdinary = &p
		}
	}

	if grouped == nil {
		t.Fatalf("expected one grouped provider profile, got %#v", parsed.Profiles)
	}
	if grouped.Name != "Автоподбор локации" {
		t.Fatalf("expected grouped profile display name, got %q", grouped.Name)
	}
	if grouped.Server != "" || grouped.Port != 0 || grouped.UserIdentity != "" {
		t.Fatalf("expected grouped profile not to collapse to one endpoint/user, got %#v", *grouped)
	}
	if !strings.HasPrefix(grouped.ID, "xray-json-") {
		t.Fatalf("expected deterministic grouped xray-json id, got %q", grouped.ID)
	}
	stored := profile.ProviderXrayConfigJSON(*grouped)
	for _, want := range []string{`"tag":"auto"`, `"tag":"ai"`, `"tag":"tg"`, `"routing"`, `"balancers"`} {
		if !strings.Contains(stored, want) {
			t.Fatalf("expected grouped provider config to preserve %s, got %s", want, stored)
		}
	}

	for _, want := range []string{"auto", "ai", "tg"} {
		if _, ok := ordinaryByTag[want]; !ok {
			t.Fatalf("expected ordinary single-location profile %q beside grouped profile, got %#v", want, ordinaryByTag)
		}
	}
	if xhttpOrdinary == nil {
		t.Fatalf("expected an ordinary xhttp variant beside grouped profile, got %#v", parsed.Profiles)
	}
	if xhttpOrdinary.Server != "tg.edge.invalid" || xhttpOrdinary.UserIdentity != testfixtures.GroupedXrayUserID || xhttpOrdinary.Transport != "xhttp" || xhttpOrdinary.Security != "tls" || xhttpOrdinary.Path != "/xhttp" {
		t.Fatalf("unexpected xhttp ordinary profile: %#v", *xhttpOrdinary)
	}
}

func TestParseXrayJSONObjectKeepsNormalizedQuicProfile(t *testing.T) {
	parsed, err := ParseXrayJSONSubscription([]byte(testfixtures.SingleVLESSXrayJSON("quic-provider", "quic.edge.invalid", "quic", "tls")))
	if err != nil {
		t.Fatalf("ParseXrayJSONSubscription failed: %v", err)
	}
	if got, want := len(parsed.Profiles), 1; got != want {
		t.Fatalf("expected %d normalized profile, got %d: %#v", want, got, parsed.Profiles)
	}
	p := parsed.Profiles[0]
	if p.Protocol != "vless" {
		t.Fatalf("expected normalized VLESS profile, got %#v", p)
	}
	if p.Server != "quic.edge.invalid" || p.Transport != "quic" || p.Security != "tls" || p.Name != "quic-provider" {
		t.Fatalf("unexpected normalized quic profile: %#v", p)
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
