package engine

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestNMOwnedTunConfigRequiresProtectedTransportMark(t *testing.T) {
	p := proxyOnlyRealityProfile()
	opts := DefaultXrayTunConfigOptions()

	_, err := GenerateNMOwnedXrayTunConfig(p, opts)
	if err == nil || !strings.Contains(err.Error(), "non-zero Podlaz egress mark") {
		t.Fatalf("unmarked NM-owned TUN must be rejected, got %v", err)
	}

	native, _, err := profile.NewImportedFileProviderXrayConfig("native", []byte(`{"outbounds":[{"tag":"one","protocol":"freedom"}]}`))
	if err != nil {
		t.Fatalf("create native profile: %v", err)
	}
	_, err = GenerateNMOwnedXrayTunConfig(native, opts)
	if err == nil || !strings.Contains(err.Error(), "non-zero Podlaz egress mark") {
		t.Fatalf("unmarked native NM-owned TUN must be rejected, got %v", err)
	}
}

func TestNMOwnedTunConfigMarksVLESSWithoutChangingDefaultBackend(t *testing.T) {
	p := proxyOnlyRealityProfile()
	opts := DefaultXrayTunConfigOptions()
	opts.EgressMark = 20570

	before, err := GenerateXrayTunConfig(p, opts)
	if err != nil {
		t.Fatalf("generate current TUN config: %v", err)
	}
	nm, err := GenerateNMOwnedXrayTunConfig(p, opts)
	if err != nil {
		t.Fatalf("generate NM-owned TUN config: %v", err)
	}
	after, err := GenerateXrayTunConfig(p, opts)
	if err != nil {
		t.Fatalf("regenerate current TUN config: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("NM-owned generation changed the default TUN config")
	}

	var current, proposed struct {
		Inbounds []map[string]any `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(before, &current); err != nil {
		t.Fatalf("decode current config: %v", err)
	}
	if err := json.Unmarshal(nm, &proposed); err != nil {
		t.Fatalf("decode NM config: %v", err)
	}
	if len(current.Inbounds) != 1 || len(proposed.Inbounds) != 1 || len(proposed.Outbounds) != 1 {
		t.Fatalf("unexpected TUN topology: current=%d proposed=%d", len(current.Inbounds), len(proposed.Inbounds))
	}
	if !jsonObjectEqual(t, current.Inbounds[0], proposed.Inbounds[0]) {
		t.Fatal("NM composition must not change the TUN inbound schema")
	}
	currentOutbound := current.Outbounds[0]
	nmOutbound := proposed.Outbounds[0]
	oldStream := currentOutbound["streamSettings"].(map[string]any)
	newStream := nmOutbound["streamSettings"].(map[string]any)
	sockopt, ok := newStream["sockopt"].(map[string]any)
	if !ok || len(sockopt) != 1 || sockopt["mark"] != float64(opts.EgressMark) {
		t.Fatalf("NM VLESS transport must have exactly the requested mark: %#v", newStream)
	}
	delete(newStream, "sockopt")
	if !jsonObjectEqual(t, oldStream, newStream) {
		t.Fatal("NM marking must preserve REALITY authentication and transport settings")
	}
	delete(currentOutbound, "streamSettings")
	delete(nmOutbound, "streamSettings")
	if !jsonObjectEqual(t, currentOutbound, nmOutbound) {
		t.Fatal("NM marking must preserve VLESS credentials, provider address and routing")
	}
	if _, present := oldStream["sockopt"]; present {
		t.Fatal("current VLESS TUN output unexpectedly acquired a mark")
	}
}

func TestNMOwnedTunConfigMarksEverySupportedTypedProtocol(t *testing.T) {
	const uuid = "00000000-0000-0000-0000-000000000002"
	for _, tc := range []struct {
		name string
		uri  string
	}{
		{"vless", "vless://" + uuid + "@example.com:443?security=none&type=tcp#vless"},
		{"trojan", "trojan://example-password@example.com:443?security=tls&type=tcp#trojan"},
		{"shadowsocks", "ss://aes-128-gcm:example-password@example.com:443#shadowsocks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, err := profile.ImportShareURI(tc.uri)
			if err != nil {
				t.Fatalf("import profile: %v", err)
			}
			opts := DefaultXrayTunConfigOptions()
			opts.EgressMark = 20570
			got, err := GenerateNMOwnedXrayTunConfig(p, opts)
			if err != nil {
				t.Fatalf("generate NM config: %v", err)
			}
			var runtime struct {
				Outbounds []struct {
					StreamSettings struct {
						Sockopt struct {
							Mark uint32 `json:"mark"`
						} `json:"sockopt"`
					} `json:"streamSettings"`
				} `json:"outbounds"`
			}
			if err := json.Unmarshal(got, &runtime); err != nil {
				t.Fatalf("decode NM config: %v", err)
			}
			if len(runtime.Outbounds) != 1 || runtime.Outbounds[0].StreamSettings.Sockopt.Mark != opts.EgressMark {
				t.Fatalf("missing exact protected transport mark for %s", tc.name)
			}
		})
	}
}

func TestNMOwnedTunConfigPreservesGroupedProviderAuthority(t *testing.T) {
	source := []byte(`{
	  "futureTop":{"enabled":true},
	  "outbounds":[
	    {"tag":"first","protocol":"vless","streamSettings":{"network":"raw","sockopt":{"tcpKeepAliveIdle":30}}},
	    {"tag":"second","protocol":"trojan","streamSettings":{"network":"raw"}}
	  ],
	  "routing":{"rules":[{"outboundTag":"first","domain":["example.test"]}]},
	  "balancers":[{"tag":"choice","selector":["first","second"]}]
	}`)
	p, _, err := profile.NewImportedFileProviderXrayConfig("native", source)
	if err != nil {
		t.Fatalf("create native profile: %v", err)
	}
	opts := DefaultXrayTunConfigOptions()
	opts.EgressMark = 20570
	got, err := GenerateNMOwnedXrayTunConfig(p, opts)
	if err != nil {
		t.Fatalf("generate grouped NM config: %v", err)
	}
	if string(source) != profile.ProviderXrayConfigJSON(p) {
		// The imported source is stored independently of runtime overlays.
		// Verify preservation against the same source instead of any selected outbound.
		var stored, original any
		if json.Unmarshal([]byte(profile.ProviderXrayConfigJSON(p)), &stored) != nil || json.Unmarshal(source, &original) != nil || !jsonObjectEqual(t, stored, original) {
			t.Fatal("NM runtime composition modified provider source")
		}
	}
	var runtime struct {
		Routing json.RawMessage `json:"routing"`
		Balancers json.RawMessage `json:"balancers"`
		FutureTop json.RawMessage `json:"futureTop"`
		Outbounds []struct {
			Tag string `json:"tag"`
			StreamSettings struct {
				Sockopt struct {
					Mark uint32 `json:"mark"`
					KeepAlive int `json:"tcpKeepAliveIdle"`
				} `json:"sockopt"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(got, &runtime); err != nil {
		t.Fatalf("decode NM grouped config: %v", err)
	}
	if len(runtime.Outbounds) != 2 || runtime.Outbounds[0].Tag != "first" || runtime.Outbounds[1].Tag != "second" {
		t.Fatal("NM runtime must retain provider outbound ordering and identities")
	}
	for _, outbound := range runtime.Outbounds {
		if outbound.StreamSettings.Sockopt.Mark != opts.EgressMark {
			t.Fatalf("unmarked provider outbound %s", outbound.Tag)
		}
	}
	if runtime.Outbounds[0].StreamSettings.Sockopt.KeepAlive != 30 {
		t.Fatal("provider socket options must survive transport marking")
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal(source, &original); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"routing", "balancers", "futureTop"} {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(got, &doc); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original[key], doc[key]) {
			// JSON whitespace of preserved raw fragments is not significant.
			var a, b any
			if json.Unmarshal(original[key], &a) != nil || json.Unmarshal(doc[key], &b) != nil || !jsonObjectEqual(t, a, b) {
				t.Fatalf("NM composition modified provider-owned %s", key)
			}
		}
	}
}

func TestNMOwnedTunConfigRejectsConflictingNativeMarks(t *testing.T) {
	p, _, err := profile.NewImportedFileProviderXrayConfig("native", []byte(`{"outbounds":[{"tag":"provider","protocol":"vless","streamSettings":{"sockopt":{"mark":4242}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultXrayTunConfigOptions()
	opts.EgressMark = 20570
	_, err = GenerateNMOwnedXrayTunConfig(p, opts)
	if err == nil || !strings.Contains(err.Error(), "conflicts with Podlaz egress mark") {
		t.Fatalf("expected a fail-closed provider mark conflict, got %v", err)
	}
}

func jsonObjectEqual(t *testing.T, a, b any) bool {
	t.Helper()
	left, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(left, right)
}
