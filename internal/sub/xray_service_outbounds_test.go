package sub

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

func TestParseSubscriptionContentIgnoresXrayServiceOutbounds(t *testing.T) {
	body := xrayJSONWithServiceOutbounds(remoteXrayConfigObject("00000000-0000-0000-0000-000000000301", "service-outbounds.example", "service-outbounds", "tcp", "tls"))

	format, parsed, err := ParseSubscriptionContent([]byte(body))
	if err != nil {
		t.Fatalf("ParseSubscriptionContent failed: %v", err)
	}
	if format != FormatXrayJSON {
		t.Fatalf("expected format %q, got %q", FormatXrayJSON, format)
	}
	if len(parsed.Profiles) != 1 {
		t.Fatalf("expected one imported profile, got %#v", parsed.Profiles)
	}
	if parsed.Profiles[0].Source != profile.SourceSubscription {
		t.Fatalf("expected subscription profile source, got %#v", parsed.Profiles[0])
	}
	if len(parsed.Unsupported) != 0 {
		t.Fatalf("service outbounds must not be reported as unsupported, got %#v", parsed.Unsupported)
	}
}

func TestParseXrayJSONArraySubscriptionIgnoresServiceOutboundsPerEntry(t *testing.T) {
	body := "[" +
		xrayJSONWithServiceOutbounds(remoteXrayConfigObject("00000000-0000-0000-0000-000000000302", "array-service-one.example", "array-service-one", "tcp", "tls")) + "," +
		xrayJSONWithServiceOutbounds(remoteXrayConfigObject("00000000-0000-0000-0000-000000000303", "array-service-two.example", "array-service-two", "grpc", "reality")) +
		"]"

	parsed, err := ParseXrayJSONSubscription([]byte(body))
	if err != nil {
		t.Fatalf("ParseXrayJSONSubscription failed: %v", err)
	}
	if len(parsed.Profiles) != 2 {
		t.Fatalf("expected two imported profiles, got %#v", parsed.Profiles)
	}
	if len(parsed.Unsupported) != 0 {
		t.Fatalf("service outbounds must not be reported as unsupported, got %#v", parsed.Unsupported)
	}
}

func xrayJSONWithServiceOutbounds(body string) string {
	return strings.Replace(body, `"outbounds": [`, `"outbounds": [
    {"protocol":"freedom","tag":"direct"},
    {"protocol":"blackhole","tag":"block"},
    {"protocol":"dns","tag":"dns-out"},
    {"protocol":"loopback","tag":"loopback"},`, 1)
}
