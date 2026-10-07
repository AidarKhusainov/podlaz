package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestNativeXrayProxyOnlyCompositionPreservesUnknownSourceFields(t *testing.T) {
	raw := []byte(`{
	  "futureTop":{"enabled":true},
	  "inbounds":[{"tag":"provider-in","protocol":"dokodemo-door","port":1}],
	  "outbounds":[{"tag":"proxy","protocol":"future-protocol","settings":{"futureOutbound":true},"streamSettings":{"futureStream":{"v":1}}}],
	  "routing":{"rules":[{"outboundTag":"proxy","domain":["example.test"],"futureRouting":"kept"}]},
	  "balancers":[{"tag":"auto","selector":["proxy"],"futureBalancer":"kept"}]
	}`)
	p, _, err := profile.NewImportedFileProviderXrayConfig("native", raw)
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	generated, err := GenerateXrayProxyOnlyConfig(p, DefaultXrayProxyOnlyConfigOptions())
	if err != nil {
		t.Fatalf("generate proxy-only config: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(generated, &doc); err != nil {
		t.Fatalf("decode runtime config: %v", err)
	}
	text := string(generated)
	for _, want := range []string{"futureTop", "futureOutbound", "futureStream", "futureRouting", "futureBalancer"} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime composition lost %q: %s", want, text)
		}
	}
	if strings.Contains(text, "provider-in") {
		t.Fatalf("provider-owned inbound must not survive Podlaz runtime overlay: %s", text)
	}
}

func TestNativeXrayProxyOnlyRejectsProviderInboundRoutingAuthority(t *testing.T) {
	raw := []byte(`{
	  "outbounds":[{"tag":"proxy","protocol":"freedom"}],
	  "routing":{"rules":[{"inboundTag":["provider-in"],"outboundTag":"proxy"}]}
	}`)
	p, _, err := profile.NewImportedFileProviderXrayConfig("unsafe", raw)
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	err = ValidateXrayProxyOnlyProfile(p)
	if err == nil || !strings.Contains(err.Error(), "inboundTag is not supported") {
		t.Fatalf("expected structural safety rejection, got %v", err)
	}
}

func TestNativeXrayTunCompositionPreservesProviderAuthorityAndUnknownFields(t *testing.T) {
	raw := []byte(`{
	  "futureTop":{"enabled":true},
	  "inbounds":[{"tag":"provider-in","protocol":"dokodemo-door","port":1}],
	  "outbounds":[
	    {"tag":"provider-a","protocol":"vless","settings":{"futureOutbound":true},"streamSettings":{"network":"tcp","sockopt":{"tcpKeepAliveIdle":30},"futureStream":{"v":1}}},
	    {"tag":"provider-b","protocol":"freedom","settings":{"domainStrategy":"UseIPv4"}}
	  ],
	  "routing":{"rules":[{"outboundTag":"provider-a","domain":["example.test"],"futureRouting":"kept"}]},
	  "balancers":[{"tag":"auto","selector":["provider-a","provider-b"],"futureBalancer":"kept"}]
	}`)
	p, _, err := profile.NewImportedFileProviderXrayConfig("native", raw)
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	opts := DefaultXrayTunConfigOptions()
	opts.EgressMark = 20570
	generated, err := GenerateXrayTunConfig(p, opts)
	if err != nil {
		t.Fatalf("generate TUN config: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(generated, &doc); err != nil {
		t.Fatalf("decode runtime config: %v", err)
	}
	text := string(generated)
	for _, want := range []string{"futureTop", "futureOutbound", "futureStream", "futureRouting", "futureBalancer", "tcpKeepAliveIdle"} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime TUN composition lost %q: %s", want, text)
		}
	}
	if strings.Contains(text, "provider-in") {
		t.Fatalf("provider-owned inbound must not survive Podlaz TUN overlay: %s", text)
	}

	var inbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(doc["inbounds"], &inbounds); err != nil {
		t.Fatalf("decode TUN inbounds: %v", err)
	}
	if len(inbounds) != 1 || inbounds[0].Tag != "podlaz-tun" || inbounds[0].Protocol != "tun" {
		t.Fatalf("unexpected TUN overlay inbounds: %#v", inbounds)
	}

	var outbounds []map[string]any
	if err := json.Unmarshal(doc["outbounds"], &outbounds); err != nil {
		t.Fatalf("decode provider outbounds: %v", err)
	}
	if len(outbounds) != 2 {
		t.Fatalf("unexpected provider outbound count: %#v", outbounds)
	}
	for i, outbound := range outbounds {
		stream, ok := outbound["streamSettings"].(map[string]any)
		if !ok {
			t.Fatalf("outbound %d has no composed streamSettings: %#v", i, outbound)
		}
		sockopt, ok := stream["sockopt"].(map[string]any)
		if !ok || sockopt["mark"] != float64(20570) {
			t.Fatalf("outbound %d has no exact Podlaz egress mark: %#v", i, stream)
		}
	}
}

func TestNativeXrayTunCompositionRejectsConflictingProviderMark(t *testing.T) {
	raw := []byte(`{
	  "outbounds":[{"tag":"provider","protocol":"vless","streamSettings":{"sockopt":{"mark":4242}}}]
	}`)
	p, _, err := profile.NewImportedFileProviderXrayConfig("native", raw)
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	opts := DefaultXrayTunConfigOptions()
	opts.EgressMark = 20570
	_, err = GenerateXrayTunConfig(p, opts)
	if err == nil || !strings.Contains(err.Error(), "provider sockopt.mark 4242 conflicts with Podlaz egress mark 20570") {
		t.Fatalf("expected actionable provider mark conflict, got %v", err)
	}
}
