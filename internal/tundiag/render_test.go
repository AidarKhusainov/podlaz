package tundiag

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestHumanAndJSONRenderersUseSamePrivateModel(t *testing.T) {
	report := Report{
		Session: Session{State: "active", ProfileName: "office token=profile-secret"},
		Network: Network{ServerEndpoint: "vpn.example.test:443?token=query-secret"},
		Probes: []ProbeResult{{
			ID: "https", Layer: LayerHTTPS, Status: ProbeFail, Classification: ClassHTTPSFailure,
			Error:    "password=probe-secret",
			Evidence: Evidence{Commands: []CommandEvidence{{Command: "curl token=command-secret", Stdout: "123e4567-e89b-12d3-a456-426614174000"}}},
		}},
	}
	human := RenderHuman(report, true)
	var machine bytes.Buffer
	if err := WriteJSON(&machine, report); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{human, machine.String()} {
		for _, secret := range []string{
			"office",
			"vpn.example.test",
			"profile-secret",
			"query-secret",
			"probe-secret",
			"command-secret",
			"123e4567-e89b-12d3-a456-426614174000",
		} {
			if strings.Contains(output, secret) {
				t.Fatalf("output leaked %q: %s", secret, output)
			}
		}
	}
	var decoded Report
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != SchemaVersion || decoded.PrimaryClassification != ClassHTTPSFailure || decoded.Session.ProfileName != privacyProfileName {
		t.Fatalf("unexpected JSON model: %#v", decoded)
	}
}

func TestRenderHumanShowsPrivateSkippedDependency(t *testing.T) {
	text := RenderHuman(Report{Probes: []ProbeResult{{ID: "tls", Layer: LayerTLS, Status: ProbeSkipped, DependencyReason: "dependency tcp-443 status is fail"}}}, false)
	if !strings.Contains(text, "SKIPPED") || !strings.Contains(text, privacyDiagnosticText) {
		t.Fatalf("missing private skipped reason: %s", text)
	}
	if strings.Contains(text, "tcp-443") {
		t.Fatalf("skipped dependency identifier leaked: %s", text)
	}
}

func TestRenderHumanCompactShowsPrivateDNSAndIPState(t *testing.T) {
	report := Report{
		Session: Session{State: "active", Mode: "tun", Interface: "podlaz0"},
		Network: Network{
			TunInterface: "podlaz0",
			TunMTU:       1500,
			DNSServers:   []string{"1.1.1.1"},
			IPv4Status:   "through_tun",
			IPv6Status:   "possible_leak",
		},
	}
	text := RenderHuman(report, false)
	for _, want := range []string{"DNS           [address]", "IPv4=through_tun", "IPv6=possible_leak"} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact output missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "1.1.1.1") {
		t.Fatalf("compact output leaked DNS address: %s", text)
	}
}

func TestRenderHumanVerboseShowsTypedPrivacyPlaceholders(t *testing.T) {
	report := Report{
		Session: Session{State: "active", Mode: "tun", ProfileName: "Private profile", Interface: "podlaz0"},
		Network: Network{
			PhysicalInterface: "wlan0",
			SSID:              "Example Wi-Fi",
			Gateway:           "192.0.2.1",
			LocalAddresses:    []string{"192.0.2.20/24"},
			TunInterface:      "podlaz0",
			DNSServers:        []string{"1.1.1.1"},
			ServerEndpoint:    "vpn.example.test:443",
			ServerAddresses:   []string{"203.0.113.10"},
			DoHProviders:      []string{"https://dns.example.test/dns-query"},
		},
	}
	text := RenderHuman(report, true)
	for _, want := range []string{
		"Profile       [profile]",
		"Uplink        [interface] ([network])",
		"Gateway       [address]",
		"Local IPs     [address]",
		"VPN server    [endpoint]",
		"Server IPs    [address]",
		"DoH targets   [endpoint]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("verbose output missing %q: %s", want, text)
		}
	}
	for _, sensitive := range []string{"Private profile", "wlan0", "Example Wi-Fi", "192.0.2.1", "192.0.2.20", "1.1.1.1", "vpn.example.test", "203.0.113.10", "dns.example.test"} {
		if strings.Contains(text, sensitive) {
			t.Fatalf("verbose output leaked %q: %s", sensitive, text)
		}
	}
}
