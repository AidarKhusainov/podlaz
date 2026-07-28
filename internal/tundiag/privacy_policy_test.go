package tundiag

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPublicDiagnosticPrivacyPolicyRemovesIdentifiersFromPersistenceAndRendering(t *testing.T) {
	report := Report{
		GeneratedAt: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
		Session: Session{
			State:          "verifying",
			Mode:           "tun",
			ProfileName:    "Profile Alpha 9Q",
			TransactionID:  "txn-private-42",
			CoreRunning:    true,
			Interface:      "wlan-private0",
			MetadataSource: "profile Profile Alpha 9Q on wlan-private0",
		},
		Network: Network{
			PhysicalInterface: "wlan-private0",
			SSID:              "Corp WiFi 9Q",
			Gateway:           "192.0.2.1",
			LocalAddresses:    []string{"192.0.2.10/24", "2001:db8::10/64"},
			TunInterface:      "podlaz0",
			TunMTU:            1500,
			UplinkMTU:         1420,
			DNSServers:        []string{"192.0.2.53"},
			IPv4Status:        "detected",
			IPv6Status:        "detected",
			ServerEndpoint:    "vpn.private.example.test:443",
			ServerHostname:    "vpn.private.example.test",
			ServerName:        "sni.private.example.test",
			ServerAddresses:   []string{"192.0.2.44"},
			DoHProviders:      []string{"https://doh.private.example.test/dns-query"},
		},
		Probes: []ProbeResult{{
			ID:             "route",
			Layer:          LayerRoute,
			Status:         ProbeFail,
			Classification: ClassRouteFailure,
			Target:         "vpn.private.example.test:443",
			Error:          "route to 192.0.2.44 through wlan-private0 failed for Profile Alpha 9Q",
			DependencyReason: "SSID Corp WiFi 9Q is unavailable",
			Evidence: Evidence{
				Endpoint:          "vpn.private.example.test:443",
				ResolvedAddresses: []string{"192.0.2.44", "2001:db8::44"},
				Route: &RouteEvidence{
					Destination: "192.0.2.44",
					Interface:   "wlan-private0",
					Gateway:     "192.0.2.1",
					Table:       "main-private-9q",
					Rule:        "to 192.0.2.44 lookup main-private-9q",
					Raw:         "192.0.2.44 via 192.0.2.1 dev wlan-private0",
				},
				DNS: &DNSEvidence{
					Server:       "192.0.2.53",
					Name:         "vpn.private.example.test",
					ResponseCode: 2,
					Addresses:    []string{"192.0.2.44"},
				},
				TLS: &TLSEvidence{
					Version:     "TLS1.3",
					Cipher:      "TLS_AES_128_GCM_SHA256",
					PeerSubject: "CN=vpn.private.example.test",
					PeerIssuer:  "CN=Private Issuer 9Q",
				},
				HTTP: &HTTPEvidence{
					StatusCode: 302,
					Location:   "https://login.private.example.test/session",
				},
				IPv6: &IPv6Evidence{
					State:            "detected",
					UplinkAddresses:  []string{"2001:db8::10/64"},
					TunAddresses:     []string{"2001:db8::20/64"},
					DefaultInterface: "wlan-private0",
					RouteTable:       "default via 2001:db8::1 dev wlan-private0",
				},
				Commands: []CommandEvidence{{
					Command:  "ip route get 192.0.2.44",
					Stdout:   "192.0.2.44 via 192.0.2.1 dev wlan-private0 src 192.0.2.10",
					Stderr:   "lookup vpn.private.example.test failed on Corp WiFi 9Q",
					ExitCode: 2,
				}},
				PolicyRules:   []string{"10000: from 192.0.2.10 lookup main-private-9q"},
				NftablesRules: []string{"ip daddr 192.0.2.44 accept comment SecretServer9Q"},
				Notes:         []string{"Profile Alpha 9Q uses Corp WiFi 9Q"},
			},
		}},
		Warnings: []string{"SSID Corp WiFi 9Q on wlan-private0 is unstable"},
		Errors:   []string{"dial vpn.private.example.test at 192.0.2.44 failed for Profile Alpha 9Q"},
	}

	runtimeDir := t.TempDir()
	store := Store{RuntimeDir: runtimeDir}
	if _, err := store.Save(report); err != nil {
		t.Fatalf("save public diagnostic report: %v", err)
	}
	persisted, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("read public diagnostic report: %v", err)
	}
	loaded, _, err := store.Load()
	if err != nil {
		t.Fatalf("load public diagnostic report: %v", err)
	}

	var jsonOutput bytes.Buffer
	if err := WriteJSON(&jsonOutput, loaded); err != nil {
		t.Fatalf("render public diagnostic JSON: %v", err)
	}
	humanOutput := RenderHuman(loaded, true)
	outputs := map[string]string{
		"persisted JSON": string(persisted),
		"client JSON":    jsonOutput.String(),
		"human output":   humanOutput,
	}
	sensitive := []string{
		"Profile Alpha 9Q",
		"txn-private-42",
		"wlan-private0",
		"Corp WiFi 9Q",
		"192.0.2.1",
		"192.0.2.10",
		"2001:db8::10",
		"192.0.2.53",
		"vpn.private.example.test",
		"sni.private.example.test",
		"login.private.example.test",
		"192.0.2.44",
		"2001:db8::44",
		"doh.private.example.test",
		"Private Issuer 9Q",
		"SecretServer9Q",
		"main-private-9q",
	}
	for outputName, output := range outputs {
		for _, value := range sensitive {
			if strings.Contains(output, value) {
				t.Fatalf("%s leaked sensitive value %q: %s", outputName, value, output)
			}
		}
	}

	if loaded.PrimaryClassification != ClassRouteFailure || loaded.Status != StatusUnhealthy {
		t.Fatalf("privacy policy changed diagnostic verdict: %#v", loaded)
	}
	probe, ok := loaded.Probe("route")
	if !ok || probe.Evidence.DNS == nil || probe.Evidence.DNS.ResponseCode != 2 || probe.Evidence.HTTP == nil || probe.Evidence.HTTP.StatusCode != 302 {
		t.Fatalf("privacy policy removed safe structural evidence: %#v", probe)
	}
}
